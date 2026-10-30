//go:build windows && amd64

package restore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func hierarchyNativeDir(t *testing.T) string {
	t.Helper()
	root := os.Getenv("S01_TEST_ROOT")
	if root == "" {
		t.Skip("native hierarchy restore requires S01_TEST_ROOT")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if !filepath.IsAbs(root) || strings.HasPrefix(strings.ToLower(filepath.Clean(root))+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("private external fixture root required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	u, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	var nonce [8]byte
	rand.Read(nonce[:])
	dir := filepath.Join(root, fmt.Sprintf("hierarchy-restore-%x", nonce[:]))
	if err = windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestHierarchyChainedNativeRestoreMutationRestore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	members := fixture(t)
	b, err := A.DecodeReadable[A.Board](members["board.json"])
	if err != nil {
		t.Fatal(err)
	}
	child := b.Cards[0].CardID
	parent := b.Cards[0]
	parent.CardID = "70000000-0000-4000-8000-000000000002"
	parent.Ticket = 2
	parent.Revision = 1
	parent.Title = "Historical-origin parent"
	parent.CommentIDs = []A.UUID{}
	parent.LinkIDs = []A.UUID{}
	parent.AttachmentIDs = []A.UUID{}
	parent.CurrentApprovalID = nil
	b.Cards = append(b.Cards, parent)
	b.NextTicket = 3
	b.Cards[0].Parent = &A.CardRef{BoardID: b.Identity.BoardID, Incarnation: b.Identity.Incarnation, CardID: parent.CardID}
	members["board.json"], err = A.Readable(b)
	if err != nil {
		t.Fatal(err)
	}
	source := sourcePackage(t, members)
	first, err := prepare(ctx, source, testTime, testInc)
	if err != nil {
		t.Fatal(err)
	}
	firstHistory := append([]A.Snapshot{}, first.history.Records...)
	if first.board.Cards[0].Parent.Incarnation != testInc || first.board.Cards[1].CreatedIn.Incarnation == testInc {
		t.Fatal("current rebasing or old provenance")
	}
	dir := hierarchyNativeDir(t)
	if err = first.Construct(ctx, dir); err != nil {
		t.Fatal(err)
	}
	store, err := S.Existing(ctx, dir, schema.Identity{BoardID: string(b.Identity.BoardID), Incarnation: string(testInc)})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	actor := string(b.Actors[0].ActorID)
	grant := "80000000-0000-4000-8000-000000000001"
	installation := "90000000-0000-4000-8000-000000000001"
	origin := "https://synthetic.localhost:47443"
	lease, err := store.Writer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = lease.Do(ctx, func(c *sql.Conn) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{{"INSERT INTO grant_fact VALUES(?,?)", []any{testInc, grant}}, {"INSERT INTO grant_record VALUES(?,?,?,1)", []any{testInc, grant, actor}}, {"INSERT INTO grant_right VALUES(?,?,'edit')", []any{testInc, grant}}, {"INSERT INTO grant_right VALUES(?,?,'read-own-outcome')", []any{testInc, grant}}} {
			if _, e := c.ExecContext(ctx, q.sql, q.args...); e != nil {
				return e
			}
		}
		return nil
	})
	lease.Close()
	if err != nil {
		t.Fatal(err)
	}
	g, err := grants.New(installation, origin, func(_ context.Context, r writer.Read, n time.Time) (grants.Credential, error) {
		return grants.Credential{InstallationID: installation, Origin: origin, BoardID: string(b.Identity.BoardID), Incarnation: string(testInc), ActorID: actor, GrantID: grant, Mode: "cli", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	service := cards.New(g)
	engine := writer.New(store)
	revision := uint64(b.Revision)
	cardRevision := uint64(b.Cards[0].Revision)
	submit := func(input map[string]any) {
		t.Helper()
		var nonce [16]byte
		rand.Read(nonce[:])
		op := fmt.Sprintf("%x-%x-%x-%x-%x", nonce[:4], nonce[4:6], nonce[6:8], nonce[8:10], nonce[10:])
		raw, _ := json.Marshal(map[string]any{"opId": op, "incarnation": testInc, "target": map[string]any{"cardId": child}, "expectedRevision": revision, "expectedCardRevision": cardRevision, "input": input})
		intent, e := contract.Freeze(raw, contract.ResolvedRoute{Command: "card.edit", BoardID: string(b.Identity.BoardID), TargetID: string(child)}, contract.AuthenticatedActor{ActorID: actor})
		if e != nil {
			t.Fatal(e)
		}
		result := engine.Submit(ctx, intent, service.Callbacks(cards.Route{BoardID: string(b.Identity.BoardID), Incarnation: string(testInc), CardID: string(child)}))
		if result.Terminal == nil || result.Terminal.Outcome != "committed" {
			t.Fatal("hierarchy mutation refused")
		}
		revision++
		cardRevision++
	}
	submit(map[string]any{"parent": nil})
	submit(map[string]any{"parent": A.CardRef{BoardID: b.Identity.BoardID, Incarnation: testInc, CardID: parent.CardID}})
	submit(map[string]any{"title": "Capture snapshot-time origin"})
	projection, err := projectStore(ctx, store, first.board.Identity, A.Number(revision), testTime)
	if err != nil {
		t.Fatal(err)
	}
	nextMembers := map[string][]byte{}
	for name, raw := range projection.members {
		nextMembers[name] = raw
	}
	nextMembers["manifest.json"], _ = A.Readable(projection.manifest)
	h, err := A.DecodeReadable[A.History](nextMembers["history.json"])
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range h.Records {
		if s.Origin.Incarnation == testInc && s.State != nil && s.State.Parent != nil {
			found = true
			if s.State.Parent.Incarnation != testInc {
				t.Fatal("new snapshot parent provenance lost")
			}
		}
	}
	if !found {
		t.Fatal("new hierarchy snapshot missing")
	}
	for _, old := range firstHistory {
		found := false
		for _, s := range h.Records {
			if s.Origin == old.Origin && s.CardID == old.CardID && s.CardRevision == old.CardRevision {
				found = true
				if !reflect.DeepEqual(s, old) {
					t.Fatal("old history rewritten")
				}
			}
		}
		if !found {
			t.Fatal("old history missing")
		}
	}
	secondInc := A.UUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	second, err := prepare(ctx, sourcePackage(t, nextMembers), testTime, secondInc)
	if err != nil {
		t.Fatal(err)
	}
	if second.board.Cards[0].Parent.Incarnation != secondInc || !reflect.DeepEqual(second.history.Records, h.Records) {
		t.Fatal("second restore rewrites history or fails current rebase")
	}
	secondDir := hierarchyNativeDir(t)
	if err = second.Construct(ctx, secondDir); err != nil {
		t.Fatal(err)
	}
	if _, err = second.NativeInventory(ctx, secondDir); err != nil {
		t.Fatal(err)
	}
	// Retained explicit parent:null must refreeze identically after both restores.
	for _, group := range second.outcomes.SourceArchives {
		if group.Identity == first.board.Identity {
			for _, op := range group.Operations {
				if op.Full == nil {
					continue
				}
				raw, decodeErr := contract.DecodeJSON(op.Full.Intent)
				if decodeErr != nil {
					t.Fatal(decodeErr)
				}
				payload := raw.(map[string]any)["payload"].(map[string]any)
				if value, present := payload["parent"]; present && value == nil {
					if err = A.Validate(op); err != nil {
						t.Fatal("clear parent retained intent", err)
					}
					return
				}
			}
		}
	}
	t.Fatal("clear parent original lost")
}
