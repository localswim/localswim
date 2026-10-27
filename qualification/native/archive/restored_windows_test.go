//go:build nativequalification && windows && amd64

package archivequalification

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func restored(t *testing.T, ctx context.Context, attempt, archive, source, label string) (*L.Runtime, string, L.ClientInfo) {
	t.Helper()
	root := filepath.Join(attempt, label)
	// Explicit local restored-copy API is supplied by the production adapter.
	response := L.Restore(ctx, L.RestoreOptions{SourceArchive: archive, DataDir: root, RequestID: id(), ProtectedRoots: []string{source}, Port: port(t)})
	if response.Exit != 0 || response.Status.Outcome != "confirmed" {
		t.Fatal("normal restored-copy adapter did not confirm")
	}
	before := hashTree(t, root)
	status := L.RestoreStatus(ctx, root, responseRequest(t, root))
	if status.Exit != 0 || status.Status.Outcome != "confirmed" || !reflect.DeepEqual(before, hashTree(t, root)) {
		t.Fatal("local restored status failed or modified store")
	}
	r, e := L.Start(ctx, L.Options{DataDir: root})
	if e != nil {
		t.Fatal("restored copy cannot start normally")
	}
	t.Cleanup(func() {
		if e := r.Close(); e != nil {
			t.Error("restored shutdown failed")
		}
	})
	info, e := L.ReadClient(root)
	if e != nil {
		t.Fatal("restored bot metadata unavailable")
	}
	return r, root, info
}
func responseRequest(t *testing.T, root string) A.UUID {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join(root, "restore-receipt.json"))
	if e != nil {
		t.Fatal("restore receipt unavailable")
	}
	var receipt A.Receipt
	if json.Unmarshal(raw, &receipt) != nil {
		t.Fatal("restore receipt malformed")
	}
	return receipt.RequestID
}
func bootstrap(t *testing.T, r *L.Runtime) *http.Cookie {
	t.Helper()
	q, e := http.NewRequest("POST", r.Origin()+"/api/v1/local/bootstrap", strings.NewReader("{}"))
	if e != nil {
		t.Fatal(e)
	}
	q.Header.Set("Origin", r.Origin())
	q.Header.Set("X-Localswim-Client", "browser")
	q.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer c.CloseIdleConnections()
	out, e := c.Do(q)
	if e != nil {
		t.Fatal("normal restored human bootstrap failed")
	}
	defer out.Body.Close()
	cookies := out.Cookies()
	if out.StatusCode != 200 || len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatal("normal restored human cookie unavailable")
	}
	return cookies[0]
}
func testRestored(t *testing.T, ctx context.Context, attempt, archive, source string, original L.ClientInfo, before T.BoardRead, blobs [][]byte) {
	t.Helper()
	r, root, info := restored(t, ctx, attempt, archive, source, "copy")
	if info.InstallationID == original.InstallationID || info.Incarnation == original.Incarnation || info.ActorID == original.ActorID || info.GrantID == original.GrantID || info.Token == original.Token {
		t.Fatal("restored runtime inherited source authority or identity")
	}
	b := snapshot(t, r, info)
	if len(b.Result.Cards) != len(before.Result.Cards) || len(b.Result.Attachments) != 2 {
		t.Fatal("snapshot current state changed")
	}
	assigned := ""
	for _, card := range b.Result.Cards {
		if card.OwnerID != nil {
			if string(*card.OwnerID) != original.ActorID || string(*card.OwnerID) == info.ActorID {
				t.Fatal("restored owner changed to fresh bootstrap identity")
			}
			assigned = string(card.CardID)
		}
	}
	if assigned == "" {
		t.Fatal("restored HTTP read lost current assignment")
	}
	historyOut := call(t, r, info, "GET", "/api/v1/boards/"+info.BoardID+"/cards/"+assigned+"/history", nil, nil, "")
	history, e := T.Decode[T.HistoryRead](historyOut.raw)
	if e != nil || historyOut.code != 200 {
		t.Fatal("restored normal HTTP retained history unavailable")
	}
	ownedHistory := false
	for _, record := range history.Result.Records {
		if record.State != nil && record.State.OwnerID != nil && string(*record.State.OwnerID) == original.ActorID {
			ownedHistory = true
		}
	}
	if !ownedHistory {
		t.Fatal("restored retained history lost assigned historical actor UUID")
	}
	for _, actor := range b.Result.Actors {
		if string(actor.ActorID) == info.ActorID && actor.Kind != "automation" {
			t.Fatal("bot identity kind changed")
		}
	}
	for n, a := range b.Result.Attachments {
		out := call(t, r, info, "GET", "/api/v1/boards/"+info.BoardID+"/attachments/"+string(a.AttachmentID), nil, nil, "")
		if out.code != 200 {
			t.Fatal("restored attachment unavailable")
		}
		match := false
		for _, raw := range blobs {
			if string(a.SHA256) == fmt.Sprintf("%x", sha256.Sum256(raw)) && int(a.ByteLength) == len(raw) && bytes.Equal(out.raw, raw) {
				match = true
			}
		}
		if !match {
			t.Fatalf("restored attachment %d changed", n)
		}
	}
	cookie := bootstrap(t, r)
	humanOut := call(t, r, info, "GET", "/api/v1/session", nil, cookie, "")
	human, e := T.Decode[T.SessionRead](humanOut.raw)
	if e != nil || human.Kind != "human" || string(human.ActorID) == original.ActorID {
		t.Fatal("restored configured human unavailable")
	}
	canApprove := false
	for _, right := range human.Grant.Rights {
		if right == "approve" {
			canApprove = true
		}
	}
	if !canApprove {
		t.Fatal("restored human lacks configured approval right")
	}
	if stale := call(t, r, original, "GET", "/api/v1/session", nil, nil, ""); stale.code != 401 {
		t.Fatal("source bot capability accepted by restored copy")
	}
	botOut := call(t, r, info, "GET", "/api/v1/session", nil, nil, "")
	bot, e := T.Decode[T.SessionRead](botOut.raw)
	if e != nil || bot.Kind != "automation" {
		t.Fatal("restored automation session unavailable")
	}
	for _, right := range bot.Grant.Rights {
		if right == "approve" || right == "grant-admin" {
			t.Fatal("restored bot gained authority")
		}
	}
	// Restored copy is usable, not merely a library round trip.
	terminal(t, call(t, r, info, "POST", "/api/v1/boards/"+info.BoardID+"/cards", wire(t, info, "", int(b.Result.Revision), 0, map[string]any{"title": "Restored local edit", "description": "Normal bot mutation"}), nil, ""))
	if e = r.Close(); e != nil {
		t.Fatal("restored close failed")
	}
	status := L.RestoreStatus(ctx, root, responseRequest(t, root))
	if status.Exit != 0 || status.Status.Outcome != "confirmed" {
		t.Fatal("restored status lost completion after legitimate edit")
	}
	next, e := L.Start(ctx, L.Options{DataDir: root})
	if e != nil {
		t.Fatal("restored normal restart failed")
	}
	defer next.Close()
	after, e := L.ReadClient(root)
	if e != nil || after.InstallationID != info.InstallationID || after.Incarnation != info.Incarnation || after.Token == info.Token || after.Epoch == info.Epoch {
		t.Fatal("restored restart changed identity or reused live authority")
	}
	if len(snapshot(t, next, after).Result.Cards) != len(before.Result.Cards)+1 {
		t.Fatal("restored edit not durable")
	}
	bootstrap(t, next)
}
func testUnknown(t *testing.T, ctx context.Context, attempt, archive, source string, original L.ClientInfo, path string, originalRaw []byte) {
	t.Helper()
	r, _, info := cliRestored(t, ctx, attempt, archive)
	// Deliberately retain the ORIGINAL incarnation and request. This is lookup only.
	path = strings.Replace(path, "/boards/"+original.BoardID, "/boards/"+info.BoardID, 1)
	out := call(t, r, info, "GET", path, nil, nil, "")
	var v struct {
		Outcome string `json:"outcome"`
	}
	if e := json.Unmarshal(out.raw, &v); e != nil {
		t.Fatal("restored lookup returned malformed response")
	}
	if v.Outcome == "committed" || v.Outcome == "not_committed" || v.Outcome == "not-committed" {
		t.Fatal("restored copy settled an original request")
	}
	if out.code == 200 {
		u, e := T.Decode[T.KnownIdentityUnknown](out.raw)
		if e != nil || u.Outcome != "unknown" {
			t.Fatal("restored lookup falsely authoritative")
		}
	}
	sourceUnknown, e := T.Decode[T.KnownIdentityUnknown](originalRaw)
	if e != nil || sourceUnknown.Outcome != "unknown" || string(sourceUnknown.Incarnation) != original.Incarnation {
		t.Fatal("original uncertainty evidence changed")
	}
	if len(snapshot(t, r, info).Result.Cards) != 2 {
		t.Fatal("lookup replayed a mutation")
	}
}
