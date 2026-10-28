//go:build windows && amd64

package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
)

func readCatalog(t *testing.T, r *Runtime, client *http.Client) T.WorkspaceCatalog {
	t.Helper()
	code, b := testHTTP(t, r, "GET", "/api/v1/workspace/catalog", nil, client)
	out, e := T.Decode[T.WorkspaceCatalog](b)
	if code != 200 || e != nil {
		t.Fatalf("catalog status %d decode %v", code, e)
	}
	return out
}

func TestCatalogHTTPHungProbeDoesNotDelayHealthyMembersOrDuplicate(t *testing.T) {
	base := testBase(t)
	r := testStart(t, filepath.Join(base, "w"), testPort(t))
	first := createFixture(t, r, filepath.Join(base, "p"))
	for i := 0; i < 7; i++ {
		v := first
		v.ExpectedConfigRevision = A.Positive(3 + i)
		v.BoardKey = T.WorkspaceKey("extra-" + string(rune('a'+i)))
		v.Label = T.WorkspaceLabel("Extra " + string(rune('A'+i)))
		v.DataDir = T.WorkspacePath(filepath.Join(base, "p", string(v.BoardKey)))
		code, b := testHTTP(t, r, "POST", "/api/v1/workspace/boards", v, nil)
		if code != 200 {
			t.Fatalf("fixture registration status %d (%d bytes)", code, len(b))
		}
	}
	r.mu.Lock()
	slow := r.states["harbor/main"].engine
	r.mu.Unlock()
	release := make(chan struct{})
	defer close(release)
	var calls, active, maximum atomic.Int32
	r.catalogProbe = func(ctx context.Context, engine *L.Runtime, channel string) (L.CatalogSnapshot, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if old >= n || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		if engine == slow {
			calls.Add(1)
			<-release
			return L.CatalogSnapshot{}, ctx.Err()
		}
		return engine.CatalogSnapshot(ctx, channel)
	}
	for poll := 0; poll < 2; poll++ {
		start := time.Now()
		out := readCatalog(t, r, nil)
		if time.Since(start) > 2500*time.Millisecond {
			t.Fatal("catalog exceeded response budget")
		}
		fresh := 0
		for _, row := range out.Entries {
			if row.BoardKey == "main" {
				if row.Identity != nil || row.BoardRevision != nil || row.Attention.ReadyForReview.Value != nil {
					t.Fatal("hung authorization disclosed facts")
				}
			} else if row.Freshness == "fresh" && row.Attention.ReadyForReview.Value != nil && *row.Attention.ReadyForReview.Value == 0 {
				fresh++
			}
		}
		if fresh != 7 {
			t.Fatal("healthy members did not independently complete")
		}
	}
	if calls.Load() != 1 || maximum.Load() > 4 {
		t.Fatal("hung probe duplicated or outstanding limit exceeded")
	}
}

func TestCatalogHTTPCurrentCommittedMutationsAndChannels(t *testing.T) {
	base := testBase(t)
	r := testStart(t, filepath.Join(base, "w"), testPort(t))
	fixture := createFixture(t, r, filepath.Join(base, "p"))
	before := readCatalog(t, r, nil)
	if len(before.Entries) != 1 || before.Entries[0].Freshness != "fresh" || before.Entries[0].Attention.ReadyForReview.Value == nil || *before.Entries[0].Attention.ReadyForReview.Value != 0 {
		t.Fatal("healthy zero not observed")
	}
	info, e := L.ReadClient(string(fixture.DataDir))
	if e != nil {
		t.Fatal(e)
	}
	mutate := func(method, path, card string, revision, cardRevision int, input any) T.Terminal {
		t.Helper()
		var target, cr any
		if card != "" {
			target = map[string]string{"cardId": card}
			cr = cardRevision
		}
		body, e := json.Marshal(map[string]any{"incarnation": info.Incarnation, "opId": uuid.NewString(), "target": target, "expectedRevision": revision, "expectedCardRevision": cr, "input": input})
		if e != nil {
			t.Fatal(e)
		}
		q, e := http.NewRequest(method, r.Origin()+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		q.Header.Set("Authorization", "Bearer "+string(info.Token))
		q.Header.Set("Content-Type", "application/json")
		client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
		defer client.CloseIdleConnections()
		reply, e := client.Do(q)
		if e != nil {
			t.Fatal(e)
		}
		defer reply.Body.Close()
		b, e := io.ReadAll(reply.Body)
		if e != nil {
			t.Fatal(e)
		}
		terminal, e := T.Decode[T.Terminal](b)
		if e != nil || terminal.Outcome != "committed" {
			t.Fatalf("mutation %d decode %v", reply.StatusCode, e)
		}
		return terminal
	}
	path := "/api/v1/boards/" + info.BoardID + "/cards"
	created := mutate("POST", path, "", 0, 0, map[string]any{"title": "Catalog synthetic", "description": "Normal HTTP evidence"})
	card := ""
	for _, ref := range created.ResultRefs {
		if ref.Kind == "card" {
			card = string(ref.ID)
		}
	}
	if card == "" {
		t.Fatal("created card reference")
	}
	mutate("PATCH", path+"/"+card, card, 1, 1, map[string]any{"lane": "In Progress"})
	mutate("POST", path+"/"+card+"/review-handoff", card, 2, 2, map[string]any{})
	after := readCatalog(t, r, nil)
	row := after.Entries[0]
	if row.BoardRevision == nil || *row.BoardRevision != 3 || row.Attention.ReadyForReview.Value == nil || *row.Attention.ReadyForReview.Value != 1 || row.Activity.LastCommittedAt == nil || row.Activity.Coverage == nil {
		t.Fatal("next catalog missed current committed mutation")
	}
	if row.Attention.Blocked.State != "not_configured" || row.Attention.NeedsTerry.State != "not_configured" {
		t.Fatal("M02 counts fabricated")
	}
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	defer browser.CloseIdleConnections()
	code, _ := testHTTP(t, r, "POST", "/api/v1/workspace/bootstrap", map[string]any{}, browser)
	if code != 200 {
		t.Fatal("browser workspace bootstrap")
	}
	if readCatalog(t, r, browser).Entries[0].Freshness != "fresh" {
		t.Fatal("browser configured read failed")
	}
	code, _ = testHTTP(t, r, "GET", "/api/v1/workspace/catalog?private=1", nil, nil)
	if code != 400 {
		t.Fatal("query accepted")
	}
	b, _ := json.Marshal(after)
	if strings.Contains(string(b), base) || strings.Contains(string(b), string(info.Token)) {
		t.Fatal("catalog disclosed private data")
	}
	q, _ := http.NewRequest("GET", r.Origin()+"/api/v1/workspace/catalog", nil)
	response, e := http.DefaultClient.Do(q)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 401 && response.StatusCode != 403 {
		t.Fatal("unauthenticated catalog accepted")
	}
}
