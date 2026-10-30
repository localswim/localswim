package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	local "github.com/localswim/localswim/internal/transport/local"
	"io"
	"path/filepath"
)

func runLocalStorage(ctx context.Context, args []string, out, diagnostic io.Writer) (bool, int) {
	return runLocalStorageWith(ctx, args, out, diagnostic, local.Storage)
}
func runLocalStorageWith(ctx context.Context, args []string, out, diagnostic io.Writer, operation func(context.Context, string, schema.Identity, bool) (S.StorageInspection, bool, error)) (bool, int) {
	if len(args) < 3 || args[0] != "--data" {
		return false, 0
	}
	i := 2
	jsonMode := false
	if i < len(args) && args[i] == "--json" {
		jsonMode = true
		i++
	}
	if i >= len(args) || args[i] != "storage" {
		return false, 0
	}
	i++
	fail := func(code string, exit int) (bool, int) {
		if code == "STORAGE_UPGRADE_UNKNOWN" || code == "STORAGE_REFUSED" {
			return true, storageFailure(out, diagnostic, jsonMode, code, exit)
		}
		return true, entryFailure(args, out, diagnostic, code, exit)
	}
	if i >= len(args) {
		return fail("INVOCATION_OR_IO", 1)
	}
	action := args[i]
	i++
	if action != "inspect" && action != "upgrade" {
		return fail("INVOCATION_OR_IO", 1)
	}
	flags := map[string]string{}
	for i < len(args) {
		key := args[i]
		i++
		if key == "--json" && !jsonMode {
			jsonMode = true
			continue
		}
		if i >= len(args) || flags[key] != "" {
			return fail("INVOCATION_OR_IO", 1)
		}
		switch key {
		case "--board-id", "--incarnation", "--from-version", "--to-version":
		default:
			return fail("INVOCATION_OR_IO", 1)
		}
		flags[key] = args[i]
		i++
	}
	if !schema.ValidUUID(flags["--board-id"]) || !schema.ValidUUID(flags["--incarnation"]) {
		return fail("INVOCATION_OR_IO", 1)
	}
	if action == "upgrade" {
		if flags["--from-version"] != "5" || flags["--to-version"] != "6" {
			return fail("INVOCATION_OR_IO", 1)
		}
	} else if flags["--from-version"] != "" || flags["--to-version"] != "" {
		return fail("INVOCATION_OR_IO", 1)
	}
	dir, err := filepath.Abs(filepath.FromSlash(args[1]))
	if err != nil || !safeAbsolute(dir) {
		return fail("INVOCATION_OR_IO", 1)
	}
	inspection, changed, err := operation(ctx, dir, schema.Identity{BoardID: flags["--board-id"], Incarnation: flags["--incarnation"]}, action == "upgrade")
	if errors.Is(err, S.ErrUpgradeUnknown) || err != nil && changed {
		return fail("STORAGE_UPGRADE_UNKNOWN", 4)
	}
	if err != nil {
		return fail("STORAGE_REFUSED", 2)
	}
	status := "VALID"
	if action == "upgrade" {
		status = "NO_CHANGE"
		if changed {
			status = "UPGRADED"
		}
	}
	if out == nil {
		if changed {
			return true, 4
		}
		return true, 1
	}
	if jsonMode {
		b, e := json.Marshal(struct {
			Outcome string              `json:"outcome"`
			Result  S.StorageInspection `json:"result"`
		}{status, inspection})
		if e != nil {
			if changed {
				return true, 4
			}
			return true, 1
		}
		b = append(b, '\n')
		if n, e := out.Write(b); e != nil || n != len(b) {
			if changed {
				return true, 4
			}
			return true, 1
		}
	} else {
		if _, e := fmt.Fprintf(out, "%s physicalVersion=%d baseSchemaVersion=%d board=%s incarnation=%s valid=%t\n", status, inspection.PhysicalVersion, inspection.BaseSchemaVersion, inspection.BoardID, inspection.Incarnation, inspection.Valid); e != nil {
			if changed {
				return true, 4
			}
			return true, 1
		}
	}
	return true, 0
}

// Native storage uncertainty has no card operation identity or lookup receipt.
func storageFailure(out, diagnostic io.Writer, jsonMode bool, code string, exit int) int {
	outcome, message := "REFUSED", "Storage inspection or upgrade refused"
	if exit == 4 {
		outcome = "UNKNOWN"
		message = "Upgrade outcome unknown; deliberately inspect storage before deciding"
	}
	if diagnostic != nil {
		_, _ = fmt.Fprintln(diagnostic, code)
	}
	if out == nil {
		if exit == 4 {
			return 4
		}
		return 1
	}
	var err error
	if jsonMode {
		err = json.NewEncoder(out).Encode(struct {
			Outcome string `json:"outcome"`
			Error   struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}{Outcome: outcome, Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{code, message}})
	} else {
		_, err = fmt.Fprintf(out, "%s: %s\n", outcome, message)
	}
	if err != nil && exit != 4 {
		return 1
	}
	return exit
}
