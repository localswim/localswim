package v2

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestM36WorkspacePublishingPatchIsAtomicAndHumanOnly(t *testing.T) {
	app, human := attachmentHTTPServer(t)
	bot := human
	bot.token = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	raw, _ := human.call("POST", "/boards", `{"name":"Workspace preferences"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	raw, initialHeaders := human.call("GET", "/workspace", "", "", 200)
	initial := turnOneDecode[workspace](t, raw)
	if initial.Publishing.Enabled || initial.Publishing.Pending || initial.Publishing.LastSuccessAt != nil || initial.Publishing.ErrorCode != nil {
		t.Fatalf("unexpected opt-in defaults: %+v", initial.Publishing)
	}
	initialTag := initialHeaders.Get("ETag")
	pins := fmt.Sprintf(`{"pinned_board_ids":[%q]}`, strings.ToUpper(b.ID))
	_, headers := bot.call("PATCH", "/workspace", pins, initialTag, 200)
	pinsTag := headers.Get("ETag")
	enable := `{"publishing":{"enabled":true}}`
	bot.call("PATCH", "/workspace", enable, pinsTag, 403)
	human.call("PATCH", "/workspace", enable, "", 428)
	human.call("PATCH", "/workspace", enable, initialTag, 412)

	for _, body := range []string{
		`{"pinned_board_ids":[],"publishing":{"enabled":true,"pending":true}}`,
		`{"pinned_board_ids":[],"publishing":{"enabled":"true"}}`,
		`{"pinned_board_ids":[],"publishing":{"enabled":true,"repository":"not-origin"}}`,
		`{"pinned_board_ids":null,"publishing":{"enabled":true}}`,
		`{"publishing":{}}`,
		`{"publishing":null}`,
		`{"unknown":true}`,
	} {
		human.call("PATCH", "/workspace", body, pinsTag, 400)
	}
	raw, headers = human.call("GET", "/workspace", "", "", 200)
	unchanged := turnOneDecode[workspace](t, raw)
	if headers.Get("ETag") != pinsTag || unchanged.Publishing.Enabled || !slices.Equal(unchanged.PinnedBoardIDs, []string{b.ID}) {
		t.Fatal("refused request changed the workspace")
	}

	raw, headers = human.call("PATCH", "/workspace", enable, pinsTag, 200)
	enabled := turnOneDecode[workspace](t, raw)
	enabledTag := headers.Get("ETag")
	if !enabled.Publishing.Enabled || !enabled.Publishing.Pending || enabled.Revision != unchanged.Revision+1 ||
		enabledTag == pinsTag || !slices.Equal(enabled.PinnedBoardIDs, []string{b.ID}) {
		t.Fatalf("enable did not preserve pins or advance one revision: %+v", enabled)
	}
	_, headers = human.call("PATCH", "/workspace", enable, enabledTag, 200)
	if headers.Get("ETag") != enabledTag {
		t.Fatal("unchanged settings advanced the workspace validator")
	}
	captured, err := app.workspace.capturePublication()
	if err != nil || len(captured.files) != 1 || captured.files[0].path != workspaceFileName {
		t.Fatal("workspace did not capture one preference artifact", err)
	}
	enabledBytes, err := os.ReadFile(app.workspace.path)
	if err != nil {
		t.Fatal(err)
	}
	m34Write(t, app.workspace.path, []byte(`{"revision":10,"pinned_board_ids":[],"publishing":{"enabled":true,"repository":null}}`))
	afterReplacement, err := app.workspace.capturePublication()
	if err != nil {
		t.Fatal(err)
	}
	replacementCapture, err := afterReplacement.files[0].read()
	if err != nil || !bytes.Equal(replacementCapture, enabledBytes) {
		t.Fatal("external replacement entered a queued workspace publication", err)
	}
	m34Write(t, app.workspace.path, enabledBytes)

	raw, headers = human.call("PATCH", "/workspace", `{"pinned_board_ids":[],"publishing":{"enabled":false}}`, enabledTag, 200)
	disabled := turnOneDecode[workspace](t, raw)
	if disabled.Publishing.Enabled || disabled.Publishing.Pending || len(disabled.PinnedBoardIDs) != 0 ||
		disabled.Revision != enabled.Revision+1 {
		t.Fatalf("pins and settings did not change atomically: %+v", disabled)
	}
	immutableBytes, err := captured.files[0].read()
	if err != nil || !bytes.Equal(immutableBytes, enabledBytes) {
		t.Fatal("in-flight capture followed a later preference write", err)
	}
	if _, err := app.workspace.capturePublication(); err == nil {
		t.Fatal("disabled preferences remained capturable")
	}
	reopened := newWorkspace(app.store.root)
	defer reopened.closePublisher()
	persisted, err := reopened.read()
	if err != nil || persisted.Publishing.Enabled || persisted.entityTag() != headers.Get("ETag") {
		t.Fatal("disabled preference and revision did not persist", err)
	}
}

func TestM36WorkspacePublishesExactPreferencesAfterBoardLockRelease(t *testing.T) {
	fixture := m34PublicationFixture(t)
	editor := fixture.editor
	baseline := strings.TrimSpace(string(m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")))
	m34Write(t, filepath.Join(fixture.repository, "unrelated.txt"), []byte("staged work\n"))
	m34Git(t, fixture.repository, "add", "--", "unrelated.txt")
	indexPath := filepath.Join(fixture.repository, ".git", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	// Hold the same lease acquired by publishVerifiedBackup across the workspace
	// worker's first attempt and observe it refuse rather than touch any refs.
	lease, err := acquirePublicationRepository(fixture.repository, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if lease != nil {
			releasePublicationRepository(lease)
		}
	}()
	_, headers := editor.call("GET", "/workspace", "", "", 200)
	_, headers = editor.call("PATCH", "/workspace", `{"publishing":{"enabled":true}}`, headers.Get("ETag"), 200)
	pins := fmt.Sprintf(`{"pinned_board_ids":[%q]}`, fixture.state.Board.ID)
	raw, headers := editor.call("PATCH", "/workspace", pins, headers.Get("ETag"), 200)
	expectedRevision := turnOneDecode[workspace](t, raw).Revision
	workspaceBytes, err := os.ReadFile(fixture.app.workspace.path)
	if err != nil {
		t.Fatal(err)
	}
	waitWorkspacePublishing(t, editor, func(status publishing) bool {
		return status.Pending && status.ErrorCode != nil && *status.ErrorCode == "repository_busy"
	})
	if head := strings.TrimSpace(string(m34Git(t, fixture.remote, "rev-parse", "refs/heads/main"))); head != baseline {
		t.Fatal("workspace worker published while the board repository lease was held")
	}
	releasePublicationRepository(lease)
	lease = nil
	done := waitWorkspacePublishing(t, editor, func(status publishing) bool {
		return !status.Pending && status.ErrorCode == nil && status.LastSuccessAt != nil
	})
	if done.Revision != expectedRevision || !slices.Equal(done.PinnedBoardIDs, []string{fixture.state.Board.ID}) {
		t.Fatal("publishing status altered preferences")
	}
	req, err := http.NewRequest(http.MethodGet, editor.url+Prefix+"/workspace", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+editor.token)
	req.Header.Set("If-None-Match", headers.Get("ETag"))
	response, err := editor.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("ETag") != headers.Get("ETag") {
		t.Fatal("a conditional GET hid the new publishing outcome or changed the workspace validator")
	}
	expectedPath := fixture.prefix + "/" + workspaceFileName
	remotePaths := m34Git(t, fixture.remote, "diff-tree", "--no-commit-id", "--name-only", "-r", "-z", baseline, "refs/heads/main")
	if string(remotePaths) != expectedPath+"\x00" {
		t.Fatalf("workspace publication changed unexpected paths: %q", remotePaths)
	}
	if got := m34Git(t, fixture.remote, "show", "refs/heads/main:"+expectedPath); !bytes.Equal(got, workspaceBytes) {
		t.Fatal("published workspace artifact differs from the committed preference bytes")
	}
	after, err := os.ReadFile(fixture.app.workspace.path)
	if err != nil || !bytes.Equal(after, workspaceBytes) {
		t.Fatal("workspace publishing rewrote the accepted file", err)
	}
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(indexAfter, indexBefore) {
		t.Fatal("workspace publishing changed the caller's index", err)
	}
	boardBytes, err := os.ReadFile(fixture.app.store.snapshotPath(fixture.state.Board.ID))
	if err != nil || !bytes.Equal(boardBytes, fixture.contents["boards/"+fixture.state.Board.ID+".json"]) {
		t.Fatal("workspace publishing changed the board snapshot", err)
	}
}

func waitWorkspacePublishing(t *testing.T, editor turnOneClient, ready func(publishing) bool) workspace {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var value workspace
	for time.Now().Before(deadline) {
		data, _ := editor.call("GET", "/workspace", "", "", 200)
		value = turnOneDecode[workspace](t, data)
		if ready(value.Publishing) {
			return value
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("workspace publishing did not reach the expected state: %+v", value.Publishing)
	return workspace{}
}
