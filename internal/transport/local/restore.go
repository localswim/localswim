package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"

	P "github.com/localswim/localswim/internal/archive/publication"
	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
)

type RestoreOptions struct {
	SourceArchive, DataDir string
	RequestID              A.UUID
	ProtectedRoots         []string
	Port                   int
}

type restoreCompletion struct {
	Version       int      `json:"version"`
	RequestID     A.UUID   `json:"requestId"`
	Publication   A.Status `json:"publication"`
	Identity      identity `json:"identity"`
	ReceiptSHA256 string   `json:"receiptSha256"`
}

func restoreUnknown() P.Response {
	return P.Response{Status: A.Status{APIVersion: 1, Scope: "restore", Outcome: "unknown"}, Exit: 4}
}

// Restore explicitly turns a newly published offline copy into a new local
// installation. It never retries or adopts an existing destination.
func Restore(ctx context.Context, o RestoreOptions) P.Response {
	if ctx == nil || ctx.Err() != nil || !safeDir(o.DataDir) || len(o.ProtectedRoots) == 0 || !schema.ValidUUID(string(o.RequestID)) || o.Port != 0 && (o.Port < 1024 || o.Port > 65535) {
		return restoreUnknown()
	}
	r := P.Restore(ctx, P.Request{SourceArchive: o.SourceArchive, Destination: o.DataDir, RequestID: o.RequestID, ProtectedRoots: o.ProtectedRoots})
	if r.Status.Outcome != "confirmed" {
		return r
	}
	release, e := pinRoot(o.DataDir, false)
	if e != nil {
		return restoreUnknown()
	}
	defer release()
	unlock, e := lockRoot(o.DataDir)
	if e != nil {
		return restoreUnknown()
	}
	defer unlock()
	raw, e := readPrivateBounded(filepath.Join(o.DataDir, "restore-receipt.json"), V.MaxJSONBytes)
	if e != nil {
		return restoreUnknown()
	}
	receipt, e := A.DecodeReadable[A.Receipt](raw)
	if e != nil || receipt.RequestID != o.RequestID || receipt.Phase != "published" || receipt.Result != "complete" || r.Status.Result.Destination == nil || receipt.Destination != *r.Status.Result.Destination {
		return restoreUnknown()
	}
	if o.Port == 0 {
		o.Port = 8792
	}
	id := identity{Version: 2, RestoredRequestID: string(o.RequestID), InstallationID: uuid(), BoardID: string(receipt.Destination.BoardID), Incarnation: string(receipt.Destination.Incarnation), HumanID: uuid(), BotID: uuid(), HumanGrant: uuid(), BotGrant: uuid(), Port: o.Port}
	if !id.valid() {
		return restoreUnknown()
	}
	store, e := S.Existing(ctx, filepath.Join(o.DataDir, "data"), schema.Identity{BoardID: id.BoardID, Incarnation: id.Incarnation})
	if e != nil {
		return restoreUnknown()
	}
	e = initialize(ctx, store, id)
	ce := store.Close()
	if e != nil || ce != nil {
		return restoreUnknown()
	}
	sum := sha256.Sum256(raw)
	marker := restoreCompletion{Version: 1, RequestID: o.RequestID, Publication: r.Status, Identity: id, ReceiptSHA256: hex.EncodeToString(sum[:])}
	b, e := json.Marshal(marker)
	if e != nil || publish(filepath.Join(o.DataDir, "local-restore.json"), b, false) != nil {
		return restoreUnknown()
	}
	// Identity is the last completion publication. Partial bootstrap remains unknown.
	b, e = json.Marshal(id)
	if e != nil || publish(filepath.Join(o.DataDir, "identity.json"), b, false) != nil {
		return restoreUnknown()
	}
	return r
}

// restoreEvidence is metadata-only and must precede every mutable restored open.
func restoreEvidence(root string, id identity, request string) (A.Status, error) {
	var m restoreCompletion
	b, e := readPrivate(filepath.Join(root, "local-restore.json"))
	if e != nil || decode(b, &m) != nil || !id.valid() || id.Version != 2 || id.RestoredRequestID != request || m.Version != 1 || m.RequestID != A.UUID(request) || m.Identity != id || m.Publication.APIVersion != 1 || m.Publication.Scope != "restore" || m.Publication.Outcome != "confirmed" {
		return A.Status{}, errLocal
	}
	b, e = readPrivateBounded(filepath.Join(root, "restore-receipt.json"), V.MaxJSONBytes)
	if e != nil {
		return A.Status{}, errLocal
	}
	sum := sha256.Sum256(b)
	if m.ReceiptSHA256 != hex.EncodeToString(sum[:]) {
		return A.Status{}, errLocal
	}
	receipt, e := A.DecodeReadable[A.Receipt](b)
	if e != nil || receipt.RequestID != m.RequestID || receipt.Phase != "published" || receipt.Result != "complete" || receipt.Destination.BoardID != A.UUID(id.BoardID) || receipt.Destination.Incarnation != A.UUID(id.Incarnation) {
		return A.Status{}, errLocal
	}
	expected := A.Status{APIVersion: 1, Scope: "restore", Outcome: "confirmed", Result: A.RestoreResult{SourceIdentity: &receipt.SourceManifest.Source, SourceRevision: &receipt.SourceManifest.Revision, Destination: &receipt.Destination}}
	a, _ := json.Marshal(expected)
	z, _ := json.Marshal(m.Publication)
	if string(a) != string(z) {
		return A.Status{}, errLocal
	}
	return expected, nil
}

// RestoreStatus is existing-only and read-only, including its exclusion lease.
func RestoreStatus(ctx context.Context, dataDir string, requestID A.UUID) P.Response {
	if ctx == nil || ctx.Err() != nil || !schema.ValidUUID(string(requestID)) {
		return restoreUnknown()
	}
	release, e := pinRoot(dataDir, false)
	if e != nil {
		return restoreUnknown()
	}
	defer release()
	unlock, e := lockExistingRoot(dataDir)
	if e != nil {
		return restoreUnknown()
	}
	defer unlock()
	b, e := readPrivate(filepath.Join(dataDir, "identity.json"))
	var id identity
	if e != nil || decode(b, &id) != nil {
		return restoreUnknown()
	}
	status, e := restoreEvidence(dataDir, id, string(requestID))
	if e != nil {
		return restoreUnknown()
	}
	e = S.InspectExisting(ctx, filepath.Join(dataDir, "data"), schema.Identity{BoardID: id.BoardID, Incarnation: id.Incarnation}, func(r S.InspectionRead) error { return verifyBootstrap(r, id) })
	if e != nil {
		return restoreUnknown()
	}
	return P.Response{Status: status, Exit: 0}
}

func verifyBootstrap(r S.InspectionRead, id identity) error {
	for _, v := range []struct {
		actor, grant, kind string
		human              bool
	}{{id.HumanID, id.HumanGrant, "human", true}, {id.BotID, id.BotGrant, "automation", false}} {
		var count int
		if r.Scan("SELECT count(*) FROM actor WHERE incarnation=? AND actor_id=? AND kind=? AND origin_board=? AND origin_incarnation=?", []any{id.Incarnation, v.actor, v.kind, id.BoardID, id.Incarnation}, &count) != nil || count != 1 {
			return errLocal
		}
		if r.Scan("SELECT count(*) FROM grant_record WHERE incarnation=? AND grant_id=? AND actor_id=? AND enabled=1", []any{id.Incarnation, v.grant, v.actor}, &count) != nil || count != 1 {
			return errLocal
		}
		rights := []string{"attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"}
		if v.human {
			rights = append(rights, "approve")
		}
		if r.Scan("SELECT count(*) FROM grant_right WHERE incarnation=? AND grant_id=?", []any{id.Incarnation, v.grant}, &count) != nil || count != len(rights) {
			return errLocal
		}
		for _, right := range rights {
			if r.Scan("SELECT count(*) FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name=?", []any{id.Incarnation, v.grant, right}, &count) != nil || count != 1 {
				return errLocal
			}
		}
	}
	return nil
}
