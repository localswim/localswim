package local

import (
	"context"
	"errors"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"testing"
)

func TestStoragePostCommitFailureRemainsUnknown(t *testing.T) {
	fault := errors.New("synthetic close or inspection failure")
	for _, stage := range []string{"upgrade-cleanup", "inspection"} {
		t.Run(stage, func(t *testing.T) {
			inspections := 0
			_, changed, err := storageOperation(context.Background(), "unused", schema.Identity{}, true, func(context.Context, string, schema.Identity) (bool, error) {
				if stage == "upgrade-cleanup" {
					return true, fault
				}
				return true, nil
			}, func(context.Context, string, schema.Identity) (S.StorageInspection, error) {
				inspections++
				return S.StorageInspection{}, fault
			})
			if !changed || !errors.Is(err, S.ErrUpgradeUnknown) {
				t.Fatal("postcommit failure became definite", changed, err)
			}
			if stage == "upgrade-cleanup" && inspections != 0 {
				t.Fatal("retried after uncertain upgrade")
			}
		})
	}
	_, changed, err := storageOperation(context.Background(), "unused", schema.Identity{}, true, func(context.Context, string, schema.Identity) (bool, error) { return false, fault }, func(context.Context, string, schema.Identity) (S.StorageInspection, error) {
		t.Fatal("inspected after refused upgrade")
		return S.StorageInspection{}, nil
	})
	if changed || errors.Is(err, S.ErrUpgradeUnknown) {
		t.Fatal("precommit refusal became unknown")
	}
}
