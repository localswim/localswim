//go:build windows && amd64

package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
	"golang.org/x/sys/windows"
)

const preferencesRoute = "/api/v1/workspace/preferences"

func preferenceBrowser(t *testing.T, r *Runtime) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	t.Cleanup(c.CloseIdleConnections)
	if code, _ := testHTTP(t, r, "POST", "/api/v1/workspace/bootstrap", map[string]any{}, c); code != 200 {
		t.Fatal("workspace browser bootstrap")
	}
	return c
}
func readPins(t *testing.T, r *Runtime, c *http.Client) T.WorkspacePreferences {
	t.Helper()
	code, b := testHTTP(t, r, "GET", preferencesRoute, nil, c)
	out, e := T.Decode[T.WorkspacePreferences](b)
	if code != 200 || e != nil {
		t.Fatalf("preferences read status %d decode %v", code, e)
	}
	return out
}
func pinRequest(action string, revision uint64, board string) T.WorkspacePreferencesRequest {
	q := T.WorkspacePreferencesRequest{APIVersion: 1, ExpectedPreferencesRevision: A.Positive(revision), Action: T.WorkspacePreferenceAction(action), Order: []T.WorkspacePinReference{}}
	if action != "reorder" {
		q.Pin = &T.WorkspacePinReference{ProjectKey: "harbor", BoardKey: T.WorkspaceKey(board)}
	}
	return q
}
func writePins(t *testing.T, r *Runtime, c *http.Client, q T.WorkspacePreferencesRequest) T.WorkspacePreferences {
	t.Helper()
	code, b := testHTTP(t, r, "POST", preferencesRoute, q, c)
	out, e := T.Decode[T.WorkspacePreferences](b)
	if code != 200 || e != nil {
		t.Fatalf("preferences write status %d decode %v", code, e)
	}
	return out
}

func TestPreferencesHTTPPersistenceOrderRevisionAndChannels(t *testing.T) {
	base := testBase(t)
	root := filepath.Join(base, "w")
	r := testStart(t, root, testPort(t))
	fixture := createFixture(t, r, filepath.Join(base, "p"))
	second := fixture
	second.ExpectedConfigRevision, second.BoardKey, second.DataDir = 3, "second", T.WorkspacePath(filepath.Join(base, "p", "second"))
	if code, _ := testHTTP(t, r, "POST", "/api/v1/workspace/boards", second, nil); code != 200 {
		t.Fatal("second board")
	}
	browser := preferenceBrowser(t, r)
	otherWindow := preferenceBrowser(t, r)
	if p := readPins(t, r, nil); p.PreferencesRevision != 1 || len(p.Pins) != 0 {
		t.Fatal("initial pins")
	}
	configBefore, e := r.files.Read("workspace.json", ConfigLimit)
	if e != nil {
		t.Fatal(e)
	}
	before := readCatalog(t, r, nil)
	if code, b := testHTTP(t, r, "POST", preferencesRoute, pinRequest("pin", 1, "main"), nil); code != 403 {
		t.Fatalf("automation write accepted %d (%d bytes)", code, len(b))
	}
	writePins(t, r, browser, pinRequest("pin", 1, "main"))
	if code, b := testHTTP(t, r, "POST", preferencesRoute, pinRequest("pin", 1, "second"), otherWindow); code != 412 {
		t.Fatal("stale preference write accepted")
	} else if refusal, e := T.Decode[T.WorkspacePreferencesError](b); e != nil || refusal.Error.Code != "PREFERENCES_CHANGED" || refusal.Preferences == nil || refusal.Preferences.PreferencesRevision != 2 || len(refusal.Preferences.Pins) != 1 {
		t.Fatal("stale response omitted current state")
	}
	writePins(t, r, otherWindow, pinRequest("pin", 2, "second"))
	q := pinRequest("reorder", 3, "")
	q.Order = []T.WorkspacePinReference{{ProjectKey: "harbor", BoardKey: "second"}, {ProjectKey: "harbor", BoardKey: "main"}}
	writePins(t, r, browser, q)
	if p := readPins(t, r, otherWindow); p.PreferencesRevision != 4 || p.Pins[0].BoardKey != "second" {
		t.Fatal("other window did not observe order")
	}
	for _, invalid := range []T.WorkspacePreferencesRequest{pinRequest("pin", 4, "main"), pinRequest("unpin", 4, "unknown"), pinRequest("pin", 4, "unknown"), pinRequest("reorder", 4, "")} {
		if code, _ := testHTTP(t, r, "POST", preferencesRoute, invalid, browser); code != 400 {
			t.Fatal("invalid membership preference accepted")
		}
	}
	configAfter, e := r.files.Read("workspace.json", ConfigLimit)
	if e != nil || !bytes.Equal(configBefore, configAfter) {
		t.Fatal("pins mutated membership")
	}
	after := readCatalog(t, r, nil)
	for i := range before.Entries {
		if *before.Entries[i].BoardRevision != *after.Entries[i].BoardRevision {
			t.Fatal("pins mutated board revision")
		}
	}
	public, _ := json.Marshal(readPins(t, r, nil))
	for _, secret := range []string{base, "installationId", "incarnation", "identity", "automationToken"} {
		if strings.Contains(string(public), secret) {
			t.Fatal("preference projection leaked private fact")
		}
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	r = testStart(t, root, 0)
	if p := readPins(t, r, nil); p.PreferencesRevision != 4 || p.Pins[0].BoardKey != "second" {
		t.Fatal("saved order lost at restart")
	}
	// Old workspace cookie and automation token epochs are rejected after restart.
	if code, _ := testHTTP(t, r, "GET", preferencesRoute, nil, browser); code != 401 {
		t.Fatal("old workspace cookie accepted")
	}
	fresh := preferenceBrowser(t, r)
	writePins(t, r, fresh, pinRequest("unpin", 4, "second"))
	if p := readPins(t, r, nil); p.PreferencesRevision != 5 || len(p.Pins) != 1 || p.Pins[0].BoardKey != "main" {
		t.Fatal("unpin order/revision")
	}
}

func TestPreferencesHTTPConcurrentWindowsHaveOneCASWinner(t *testing.T) {
	base := testBase(t)
	r := testStart(t, filepath.Join(base, "w"), testPort(t))
	createFixture(t, r, filepath.Join(base, "p"))
	clients := []*http.Client{preferenceBrowser(t, r), preferenceBrowser(t, r)}
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c *http.Client) {
			defer wg.Done()
			code, _ := testHTTP(t, r, "POST", preferencesRoute, pinRequest("pin", 1, "main"), c)
			results <- code
		}(c)
	}
	wg.Wait()
	close(results)
	wins, stale := 0, 0
	for code := range results {
		if code == 200 {
			wins++
		}
		if code == 412 {
			stale++
		}
	}
	if wins != 1 || stale != 1 || readPins(t, r, nil).PreferencesRevision != 2 {
		t.Fatal("concurrent writes did not preserve single preference CAS winner")
	}
}

func TestPreferencesHTTPByteCASInterferenceAndDeletionPreserveEvidence(t *testing.T) {
	base := testBase(t)
	r := testStart(t, filepath.Join(base, "w"), testPort(t))
	createFixture(t, r, filepath.Join(base, "p"))
	browser := preferenceBrowser(t, r)
	writePins(t, r, browser, pinRequest("pin", 1, "main"))
	original, e := r.files.Read("workspace-preferences.json", PreferencesLimit)
	if e != nil {
		t.Fatal(e)
	}
	p, e := decodePreferences(original, r.snapshot())
	if e != nil {
		t.Fatal(e)
	}
	p.PreferencesRevision = 3
	interference, e := preferencesBytes(p, r.snapshot())
	if e != nil {
		t.Fatal(e)
	}
	r.preferencesFault = func(label string) error {
		if label == "before-preferences-publication" {
			return r.files.CompareAndSwap("workspace-preferences.json", original, interference, PreferencesLimit)
		}
		return nil
	}
	code, b := testHTTP(t, r, "POST", preferencesRoute, pinRequest("unpin", 2, "main"), browser)
	refusal, e := T.Decode[T.WorkspacePreferencesError](b)
	if code != 412 || e != nil || refusal.Error.Code != "PREFERENCES_CHANGED" || refusal.Preferences == nil || refusal.Preferences.PreferencesRevision != 3 || len(refusal.Preferences.Pins) != 1 {
		t.Fatal("byte CAS replacement lost current state")
	}
	r.preferencesFault = nil
	actual, e := r.files.Read("workspace-preferences.json", PreferencesLimit)
	if e != nil || !bytes.Equal(actual, interference) {
		t.Fatal("failed CAS overwrote interference")
	}
	if e = os.Remove(filepath.Join(r.files.Root(), "workspace-preferences.json")); e != nil {
		t.Fatal(e)
	}
	if code, _ = testHTTP(t, r, "GET", preferencesRoute, nil, browser); code != 503 {
		t.Fatal("observed preference deletion silently reset")
	}
	if code, _ = testHTTP(t, r, "POST", preferencesRoute, pinRequest("pin", 1, "main"), browser); code != 503 {
		t.Fatal("deleted preferences overwritten")
	}
	if _, e = os.Stat(filepath.Join(r.files.Root(), "workspace-preferences.json")); !os.IsNotExist(e) {
		t.Fatal("missing preference bytes recreated")
	}
}

func TestPreferencesHTTPNonPrivateSavedFileRefusedWithoutOverwrite(t *testing.T) {
	base := testBase(t)
	r := testStart(t, filepath.Join(base, "w"), testPort(t))
	createFixture(t, r, filepath.Join(base, "p"))
	browser := preferenceBrowser(t, r)
	writePins(t, r, browser, pinRequest("pin", 1, "main"))
	path := filepath.Join(r.files.Root(), "workspace-preferences.json")
	original, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	for _, method := range []string{"GET", "POST"} {
		var value any
		if method == "POST" {
			value = pinRequest("unpin", 2, "main")
		}
		if code, _ := testHTTP(t, r, method, preferencesRoute, value, browser); code != 503 {
			t.Fatal("nonprivate saved preferences accepted")
		}
	}
	if actual, e := os.ReadFile(path); e != nil || !bytes.Equal(original, actual) {
		t.Fatal("nonprivate saved bytes overwritten")
	}
}

func TestPreferencesHTTPPublicationUncertaintyRequiresExplicitRead(t *testing.T) {
	for _, point := range []string{"before-preferences-publication", "after-preferences-publication"} {
		t.Run(point, func(t *testing.T) {
			base := testBase(t)
			r := testStart(t, filepath.Join(base, "w"), testPort(t))
			createFixture(t, r, filepath.Join(base, "p"))
			browser := preferenceBrowser(t, r)
			r.preferencesFault = func(label string) error {
				if label == point {
					return errors.New("synthetic publication interruption")
				}
				return nil
			}
			code, b := testHTTP(t, r, "POST", preferencesRoute, pinRequest("pin", 1, "main"), browser)
			refusal, e := T.Decode[T.WorkspacePreferencesError](b)
			if code != 503 || e != nil || refusal.Preferences != nil {
				t.Fatal("failed publication claimed persisted state")
			}
			r.preferencesFault = nil
			expected := A.Positive(1)
			if point == "after-preferences-publication" {
				expected = 2
			}
			if p := readPins(t, r, browser); p.PreferencesRevision != expected || len(p.Pins) != int(expected-1) {
				t.Fatal("explicit read did not reconcile publication")
			}
			if point == "after-preferences-publication" {
				if code, _ := testHTTP(t, r, "POST", preferencesRoute, pinRequest("pin", 1, "main"), browser); code != 412 {
					t.Fatal("uncertain command replayed")
				}
				if raw, e := r.files.Read("workspace-preferences.json", PreferencesLimit); e != nil || !bytes.Contains(raw, []byte(`"identity"`)) {
					t.Fatal("durable tuple bytes not retained")
				}
			}
		})
	}
}

func TestPreferencesHTTPMalformedSavedBytesStayUnavailableAndHealthyHostServes(t *testing.T) {
	for _, bad := range []string{`{`, `{"schemaVersion":2}`, `{"schemaVersion":1,"unknown":true}`, strings.Repeat(" ", int(PreferencesLimit)+1)} {
		t.Run("bad", func(t *testing.T) {
			base := testBase(t)
			root := filepath.Join(base, "w")
			r := testStart(t, root, testPort(t))
			createFixture(t, r, filepath.Join(base, "p"))
			if e := r.Close(); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(root, "workspace-preferences.json")
			if e := os.WriteFile(path, []byte(bad), 0600); e != nil {
				t.Fatal(e)
			}
			r = testStart(t, root, 0)
			browser := preferenceBrowser(t, r)
			for _, method := range []string{"GET", "POST"} {
				var value any
				if method == "POST" {
					value = pinRequest("pin", 1, "main")
				}
				code, b := testHTTP(t, r, method, preferencesRoute, value, browser)
				if refusal, e := T.Decode[T.WorkspacePreferencesError](b); code != 503 || e != nil || refusal.Preferences != nil {
					t.Fatal("bad saved preferences not refused")
				}
			}
			if code, _ := testHTTP(t, r, "GET", "/api/v1/workspace/projects", nil, browser); code != 200 {
				t.Fatal("bad preference file prevented healthy host")
			}
			if actual, e := os.ReadFile(path); e != nil || string(actual) != bad {
				t.Fatal("malformed preference bytes reset or overwritten")
			}
		})
	}
}

func TestPreferencesHTTPOpaquePinsRemovableAndReplacedTupleRefused(t *testing.T) {
	base := testBase(t)
	root := filepath.Join(base, "w")
	r := testStart(t, root, testPort(t))
	fixture := createFixture(t, r, filepath.Join(base, "p"))
	browser := preferenceBrowser(t, r)
	writePins(t, r, browser, pinRequest("pin", 1, "main"))
	// Negative technical authority fixture; no new grant-management operation.
	db, e := sql.Open("sqlite", filepath.Join(string(fixture.DataDir), "board", "board.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("UPDATE grant_record SET enabled=0 WHERE actor_id IN (SELECT actor_id FROM actor WHERE kind='human')"); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	row := readCatalog(t, r, browser).Entries[0]
	if row.Identity != nil || row.BoardRevision != nil {
		t.Fatal("read grant removal disclosed member")
	}
	if p := readPins(t, r, browser); len(p.Pins) != 1 {
		t.Fatal("opaque saved pin was silently pruned")
	}
	writePins(t, r, browser, pinRequest("unpin", 2, "main"))
	// Pinning has no read grant prerequisite and exposes only known keys.
	writePins(t, r, browser, pinRequest("pin", 3, "main"))
	identityPath := filepath.Join(string(fixture.DataDir), "identity.json")
	original, e := os.ReadFile(identityPath)
	if e != nil {
		t.Fatal(e)
	}
	var identity map[string]any
	if e = json.Unmarshal(original, &identity); e != nil {
		t.Fatal(e)
	}
	identity["incarnation"] = uuid.NewString()
	replaced, _ := json.Marshal(identity)
	if e = os.WriteFile(identityPath, replaced, 0600); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := os.WriteFile(identityPath, original, 0600); e != nil {
			t.Error(e)
		}
	}()
	q := pinRequest("reorder", 4, "")
	q.Order = []T.WorkspacePinReference{{ProjectKey: "harbor", BoardKey: "main"}}
	if code, _ := testHTTP(t, r, "POST", preferencesRoute, q, browser); code != 503 {
		t.Fatal("replacement tuple accepted during reorder")
	}
	writePins(t, r, browser, pinRequest("unpin", 4, "main"))
	if code, _ := testHTTP(t, r, "POST", preferencesRoute, pinRequest("pin", 5, "main"), browser); code != 503 {
		t.Fatal("replacement tuple adopted by pin")
	}
}

func TestPreferencesHTTPWorkspaceTokenCannotBecomeBoardAuthority(t *testing.T) {
	base := testBase(t)
	r := testStart(t, filepath.Join(base, "w"), testPort(t))
	fixture := createFixture(t, r, filepath.Join(base, "p"))
	browser := preferenceBrowser(t, r)
	boardInfo, e := L.ReadClient(string(fixture.DataDir))
	if e != nil {
		t.Fatal(e)
	}
	q, e := http.NewRequestWithContext(context.Background(), "GET", r.Origin()+preferencesRoute, nil)
	if e != nil {
		t.Fatal(e)
	}
	q.Header.Set("Authorization", "Bearer "+string(boardInfo.Token))
	out, e := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(q)
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, out.Body)
	out.Body.Close()
	if out.StatusCode != 401 {
		t.Fatal("board token accepted for preferences")
	}
	if code, _ := testHTTP(t, r, "POST", "/api/v1/workspace/projects", projectRequest(filepath.Join(base, "other"), 3), browser); code != 403 {
		t.Fatal("preferences browser gained config authority")
	}
	if code, _ := testHTTP(t, r, "POST", "/api/v1/boards/"+boardInfo.BoardID+"/cards", map[string]any{}, browser); code != 401 {
		t.Fatal("workspace browser cookie authorized a board mutation")
	}
	info, e := ReadClient(r.files.Root())
	if e != nil {
		t.Fatal(e)
	}
	q, e = http.NewRequest("GET", r.Origin()+"/api/v1/boards/"+boardInfo.BoardID+"/session", nil)
	if e != nil {
		t.Fatal(e)
	}
	q.Header.Set("Authorization", "Bearer "+string(info.Token))
	out, e = (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(q)
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, out.Body)
	out.Body.Close()
	if out.StatusCode != 401 {
		t.Fatal("workspace token accepted as board authority")
	}
}
