package main

import (
	"bytes"
	"context"
	"errors"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"strconv"
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

func TestStorageCLIExplicitAdjacentSteps(t *testing.T) {
	for _, test := range []struct {
		from, to string
		exit     int
	}{{"5", "6", 0}, {"6", "7", 0}, {"5", "7", 1}, {"7", "8", 1}, {"06", "7", 1}} {
		t.Run(test.from+"-"+test.to, func(t *testing.T) {
			args := []string{"--data", `C:\synthetic-root`, "--json", "storage", "upgrade", "--from-version", test.from, "--to-version", test.to, "--board-id", "10000000-0000-4000-8000-000000000001", "--incarnation", "20000000-0000-4000-8000-000000000001"}
			var out, diagnostic bytes.Buffer
			calls := 0
			handled, exit := runLocalStorageWithStep(context.Background(), args, &out, &diagnostic, func(_ context.Context, _ string, _ schema.Identity, upgrade bool, from, to int) (S.StorageInspection, bool, error) {
				calls++
				if !upgrade || strconv.Itoa(from) != test.from || strconv.Itoa(to) != test.to {
					t.Fatal("selected step lost")
				}
				return S.StorageInspection{PhysicalVersion: to, BaseSchemaVersion: 5, Valid: true}, true, nil
			})
			if !handled || exit != test.exit {
				t.Fatal("exit", exit)
			}
			if test.exit == 0 && calls != 1 || test.exit != 0 && calls != 0 {
				t.Fatal("invalid step dispatched", calls)
			}
		})
	}
}
