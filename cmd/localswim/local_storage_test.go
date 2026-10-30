package main

import (
	"bytes"
	"context"
	"errors"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"strings"
	"testing"
)

func TestStorageCLICommitKnowledgeExitAndDelivery(t *testing.T) {
	args := []string{"--data", `C:\synthetic-root`, "--json", "storage", "upgrade", "--from-version", "5", "--to-version", "6", "--board-id", "10000000-0000-4000-8000-000000000001", "--incarnation", "20000000-0000-4000-8000-000000000001"}
	for _, test := range []struct {
		name    string
		changed bool
		err     error
		exit    int
	}{{"precommit", false, errors.New("refused"), 2}, {"uncertain-commit", false, S.ErrUpgradeUnknown, 4}, {"postcommit-inspection", true, errors.New("inspection"), 4}, {"postcommit-cleanup", true, errors.New("cleanup"), 4}} {
		t.Run(test.name, func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			handled, exit := runLocalStorageWith(context.Background(), args, &out, &diagnostic, func(context.Context, string, schema.Identity, bool) (S.StorageInspection, bool, error) {
				return S.StorageInspection{}, test.changed, test.err
			})
			if !handled || exit != test.exit {
				t.Fatal("exit lost commit knowledge", exit)
			}
			if test.exit == 4 && !strings.Contains(out.String(), "STORAGE_UPGRADE_UNKNOWN") {
				t.Fatal("unknown not visible")
			}
		})
	}
	var diagnostic bytes.Buffer
	if _, exit := runLocalStorageWith(context.Background(), args, nil, &diagnostic, func(context.Context, string, schema.Identity, bool) (S.StorageInspection, bool, error) {
		return S.StorageInspection{PhysicalVersion: 6, BaseSchemaVersion: 5, Valid: true}, true, nil
	}); exit != 4 {
		t.Fatal("lost successful reply downgraded", exit)
	}
}
