//go:build windows && amd64

package restore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	E "github.com/localswim/localswim/internal/archive/export"
	V "github.com/localswim/localswim/internal/archive/validate"
	"github.com/localswim/localswim/internal/board/attachments"
	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

func coverNativeRestore(t *testing.T, ctx context.Context, p *Prepared) (*S.Store, *grants.Service) {
	t.Helper()
	dir := hierarchyNativeDir(t)
	if err := p.Construct(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := p.NativeInventory(ctx, dir); err != nil {
		t.Fatal(err)
	}
	id := p.board.Identity
	store, err := S.Existing(ctx, dir, schema.Identity{BoardID: string(id.BoardID), Incarnation: string(id.Incarnation)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	actor := p.board.Actors[0].ActorID
	const grant = "80000000-0000-4000-8000-000000000001"
	const installation = "90000000-0000-4000-8000-000000000001"
	const origin = "https://synthetic.localhost:47443"
	lease, err := store.Writer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = lease.Do(ctx, func(c *sql.Conn) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{"INSERT INTO grant_fact VALUES(?,?)", []any{id.Incarnation, grant}},
			{"INSERT INTO grant_record VALUES(?,?,?,1)", []any{id.Incarnation, grant, actor}},
			{"INSERT INTO grant_right VALUES(?,?,'read'),(?,?,'export'),(?,?,'edit'),(?,?,'read-own-outcome')", []any{id.Incarnation, grant, id.Incarnation, grant, id.Incarnation, grant, id.Incarnation, grant}},
		} {
			if _, e := c.ExecContext(ctx, q.sql, q.args...); e != nil {
				return e
			}
		}
		return nil
	})
	if err = errors.Join(err, lease.Close()); err != nil {
		t.Fatal(err)
	}
	g, err := grants.New(installation, origin, func(_ context.Context, _ writer.Read, now time.Time) (grants.Credential, error) {
		return grants.Credential{InstallationID: installation, Origin: origin, BoardID: string(id.BoardID), Incarnation: string(id.Incarnation), ActorID: string(actor), GrantID: grant, Mode: "cli", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, g
}

func TestCoverNativeExportAndChainedRestore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	current, historical := coverRaster(t, 32), coverRaster(t, 224)
	source := sourcePackage(t, coverMembers(t, current, historical, nil))
	first, err := prepare(ctx, source, testTime, testInc)
	if err != nil {
		t.Fatal(err)
	}
	assertDownloads := func(p *Prepared, store *S.Store, g *grants.Service) {
		t.Helper()
		id := p.board.Identity
		actor := string(p.board.Actors[0].ActorID)
		files := attachments.New(store)
		got, err := files.Read(ctx, g, string(id.BoardID), string(id.Incarnation), actor, string(currentCover))
		if err != nil || !bytes.Equal(got.Bytes, current) {
			t.Fatal("current cover bytes", err)
		}
		if _, err = files.Read(ctx, g, string(id.BoardID), string(id.Incarnation), actor, string(historicalCover)); !errors.Is(err, attachments.ErrNotFound) {
			t.Fatal("historical cover download should refuse", err)
		}
	}
	store, g := coverNativeRestore(t, ctx, first)
	assertDownloads(first, store, g)
	id, card, actor := first.board.Identity, first.board.Cards[0], first.board.Actors[0].ActorID
	const opID A.UUID = "80000000-0000-4000-8000-000000000002"
	raw, err := json.Marshal(map[string]any{
		"opId": opID, "incarnation": id.Incarnation,
		"target": map[string]any{"cardId": card.CardID}, "expectedRevision": first.board.Revision,
		"expectedCardRevision": card.Revision, "input": map[string]any{"title": "Capture the restored cover"},
	})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := contract.Freeze(raw, contract.ResolvedRoute{Command: "card.edit", BoardID: string(id.BoardID), TargetID: string(card.CardID)}, contract.AuthenticatedActor{ActorID: string(actor)})
	if err != nil {
		t.Fatal(err)
	}
	result := writer.New(store).Submit(ctx, intent, cards.New(g).Callbacks(cards.Route{BoardID: string(id.BoardID), Incarnation: string(id.Incarnation), CardID: string(card.CardID)}))
	if result.Terminal == nil || result.Terminal.Outcome != "committed" {
		t.Fatal("postrestore edit did not commit", result)
	}
	exported, err := E.Prepare(ctx, store, E.Request{Identity: id, Revision: first.board.Revision + 1, ExportedAt: testTime, Verify: func(ctx context.Context, r writer.Read) error {
		return g.Require(ctx, r, string(id.BoardID), string(id.Incarnation), string(actor), "read", "export")
	}})
	if err != nil {
		t.Fatal(err)
	}
	var zip bytes.Buffer
	if err = exported.Write(ctx, &zip); err != nil {
		t.Fatal(err)
	}
	next, err := V.Read(ctx, &zip)
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepare(ctx, next, testTime, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if choice := second.board.Cards[0].CoverAttachmentID; choice == nil || *choice != currentCover {
		t.Fatal("current cover changed during re-export or restore")
	}
	oldHistory := 0
	newHistory := 0
	priorOrigin := sourceManifest(t, source).Source
	for _, record := range second.history.Records {
		if record.Origin == priorOrigin && record.CardID == card.CardID && record.CardRevision == card.Revision {
			if record.State == nil || record.State.CoverAttachmentID == nil || *record.State.CoverAttachmentID != currentCover || !reflect.DeepEqual(record.References.AttachmentIDs, card.AttachmentIDs) {
				t.Fatal("postrestore snapshot lost prior choice or references")
			}
			newHistory++
		}
		for _, old := range first.history.Records {
			if reflect.DeepEqual(record, old) {
				oldHistory++
			}
		}
	}
	if oldHistory != len(first.history.Records) || newHistory != 1 || len(second.history.Records) != oldHistory+newHistory {
		t.Fatal("history origin or payload changed across restores")
	}
	secondStore, secondGrants := coverNativeRestore(t, ctx, second)
	assertDownloads(second, secondStore, secondGrants)
	if !reflect.DeepEqual(first.board.Attachments, second.board.Attachments) {
		t.Fatal("attachment metadata changed")
	}
	groups := map[A.Identity][]A.Operation{}
	for _, group := range second.outcomes.SourceArchives {
		groups[group.Identity] = group.Operations
	}
	for _, group := range first.outcomes.SourceArchives {
		before, e1 := A.Canonical(group.Operations)
		after, e2 := A.Canonical(groups[group.Identity])
		if e1 != nil || e2 != nil || !bytes.Equal(before, after) {
			t.Fatal("source originals changed")
		}
	}
	newOperations := groups[id]
	if len(newOperations) != 1 || newOperations[0].OpID != opID || newOperations[0].Full == nil {
		t.Fatal("postrestore original missing")
	}
	canonical, err := A.Canonical(newOperations[0].Full.Intent)
	if err != nil || !bytes.Equal(canonical, intent.CanonicalBytes()) || newOperations[0].Fingerprint != A.Digest(intent.Fingerprint()) {
		t.Fatal("postrestore original changed")
	}
	for _, fact := range first.inventory.Entries {
		if len(fact.Path) < 6 || fact.Path[:6] != "blobs/" {
			continue
		}
		found := false
		for _, after := range second.inventory.Entries {
			found = found || after == fact
		}
		if !found {
			t.Fatal("protected blob missing after re-export and restore", fact.Path)
		}
	}
}
