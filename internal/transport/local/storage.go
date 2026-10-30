package local

import (
	"context"
	"errors"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"os"
	"path/filepath"
)

// Storage performs a deliberate offline inspect or additive upgrade under both
// existing application and database exclusions. It never starts a host or creates
// missing directories, locks, identities, credentials or database files.
func Storage(ctx context.Context, dataDir string, expected schema.Identity, upgrade bool) (out S.StorageInspection, changed bool, err error) {
	return storage(ctx, dataDir, expected, upgrade, 0, 0)
}

// StorageTo performs one deliberate selected step under both native exclusions.
func StorageTo(ctx context.Context, dataDir string, expected schema.Identity, from, to int) (S.StorageInspection, bool, error) {
	if !(from == 5 && to == 6 || from == 6 && to == 7) {
		return S.StorageInspection{}, false, errLocal
	}
	return storage(ctx, dataDir, expected, true, from, to)
}
func storage(ctx context.Context, dataDir string, expected schema.Identity, upgrade bool, from, to int) (out S.StorageInspection, changed bool, err error) {
	if ctx == nil || ctx.Err() != nil || !schema.ValidUUID(expected.BoardID) || !schema.ValidUUID(expected.Incarnation) {
		return out, false, errLocal
	}
	p, e := workspacePath(dataDir, false, true, false)
	if e != nil {
		if p != nil {
			return out, false, workspaceRetain(e, p.Close)
		}
		return out, false, e
	}
	owned := &workspaceInspectionOwner{root: p}
	defer func() {
		if closeErr := owned.Close(); closeErr != nil {
			err = errors.Join(err, workspaceRetain(closeErr, owned.Close))
		}
		if changed && err != nil {
			err = errors.Join(S.ErrUpgradeUnknown, err)
		}
	}()
	lock, e := workspaceExistingLock(p.path)
	if e != nil {
		var cleanup WorkspaceCleanup
		if errors.As(e, &cleanup) {
			owned.cleanup = cleanup
		}
		return out, false, e
	}
	owned.lock = lock
	idFile, raw, e := workspaceIdentity(p.path)
	if e != nil {
		var cleanup WorkspaceCleanup
		if errors.As(e, &cleanup) {
			owned.cleanup = cleanup
		}
		return out, false, e
	}
	owned.identity = idFile
	var id identity
	if decode(raw, &id) != nil || !id.valid() || id.BoardID != expected.BoardID || id.Incarnation != expected.Incarnation {
		return out, false, errLocal
	}
	entries, e := os.ReadDir(dataDir)
	if e != nil {
		return out, false, errLocal
	}
	allowed := map[string]bool{"identity.json": true, "local.lock": true, "cli-evidence": true}
	boardDir := filepath.Join(dataDir, "board")
	if id.Version == 2 {
		if _, e = restoreEvidence(dataDir, id, id.RestoredRequestID); e != nil {
			return out, false, e
		}
		boardDir = filepath.Join(dataDir, "data")
		allowed["data"] = true
		allowed["restore-receipt.json"] = true
		allowed["local-restore.json"] = true
	} else {
		allowed["board"] = true
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return out, false, errLocal
		}
	}
	upgradeStore := S.UpgradeStorage
	if from != 0 {
		upgradeStore = func(ctx context.Context, dir string, id schema.Identity) (bool, error) {
			return S.UpgradeStorageTo(ctx, dir, id, from, to)
		}
	}
	return storageOperation(ctx, boardDir, expected, upgrade, upgradeStore, S.InspectStorage)
}

// Private dependency seam exercises post-COMMIT inspection failure without a
// product fault flag, automatic retry or a second mutation.
func storageOperation(ctx context.Context, boardDir string, expected schema.Identity, upgrade bool,
	upgradeStore func(context.Context, string, schema.Identity) (bool, error),
	inspectStore func(context.Context, string, schema.Identity) (S.StorageInspection, error)) (out S.StorageInspection, changed bool, err error) {
	if upgrade {
		changed, err = upgradeStore(ctx, boardDir, expected)
		if err != nil {
			if changed {
				err = errors.Join(S.ErrUpgradeUnknown, err)
			}
			return out, changed, err
		}
	}
	out, err = inspectStore(ctx, boardDir, expected)
	if err != nil && changed {
		err = errors.Join(S.ErrUpgradeUnknown, err)
	}
	return
}
