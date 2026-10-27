package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/localswim/localswim/internal/assets"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/transport/cli/admin"
	"github.com/localswim/localswim/internal/transport/cli/archive"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"github.com/localswim/localswim/internal/transport/setup"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	exit := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	cancel()
	os.Exit(exit)
}

// runLegacy preserves the earlier TLS composition for isolated legacy tests.
// main dispatches only run, the ordinary loopback HTTP application entry.
func runLegacy(ctx context.Context, args []string, in io.Reader, out, diagnostic io.Writer) int {
	if ctx == nil || ctx.Err() != nil {
		return entryFailure(args, out, diagnostic, "UNAVAILABLE", 3)
	}
	q, e := parseRoot(args)
	if e != nil {
		return entryFailure(args, out, diagnostic, "INVOCATION_OR_IO", 1)
	}
	if q.config == "" && !setupInvocation(q.args) {
		return entryFailure(q.args, out, diagnostic, "INVOCATION_OR_IO", 1)
	}
	var cfg runtimeConfig
	if q.config != "" {
		cfg, e = readConfig(q.config)
		if e != nil {
			return entryFailure(q.args, out, diagnostic, "INVOCATION_OR_IO", 1)
		}
	}
	m, e := setup.NewWithWindowsCurrentUserRoot()
	if e != nil {
		return entryFailure(q.args, out, diagnostic, "UNAVAILABLE", 3)
	}
	provider := admin.Provider{Manager: m}
	a := admin.Runner{Setup: m, Provider: provider, Owner: provider, Manager: m}
	if q.config == "" {
		return cliCommands(core.Runner{Provider: provider}, a, archive.Runner{Provider: provider}).run(ctx, q.args, in, out, diagnostic)
	}
	current, e := m.ReadContext()
	if e != nil {
		return entryFailure(q.args, out, diagnostic, "UNAVAILABLE", 3)
	}
	if current.InstallationID != cfg.InstallationID {
		return entryFailure(q.args, out, diagnostic, "CREDENTIAL_CONTEXT_MISMATCH", 2)
	}
	root, e := m.ProtectedRoot()
	if e != nil || !safeAbsolute(root) {
		return entryFailure(q.args, out, diagnostic, "UNAVAILABLE", 3)
	}
	closePins, e := pinRuntimeConfig(cfg, root)
	if e != nil {
		return entryFailure(q.args, out, diagnostic, "INVOCATION_OR_IO", 1)
	}
	defer closePins()
	if !separateEvidence(cfg, root) {
		return entryFailure(q.args, out, diagnostic, "INVOCATION_OR_IO", 1)
	}
	if q.serve {
		if cfg.Board == nil {
			return entryFailure(nil, out, diagnostic, "INVOCATION_OR_IO", 1)
		}
		handler, e := assets.Handler()
		if e != nil {
			return entryFailure(nil, out, diagnostic, "UNAVAILABLE", 3)
		}
		selected := sourceSelection{Directory: cfg.Board.Directory, Identity: schema.Identity{BoardID: cfg.Board.BoardID, Incarnation: cfg.Board.Incarnation}, InstallationID: cfg.InstallationID}
		s, e := startService(ctx, selected, handler)
		if e != nil {
			return entryFailure(nil, out, diagnostic, "UNAVAILABLE", 3)
		}
		<-s.done
		if s.close() != nil {
			return entryFailure(nil, out, diagnostic, "UNAVAILABLE", 3)
		}
		return 0
	}
	store, e := evidence.NewWindowsStore(cfg.EvidenceDirectory)
	if e != nil {
		return entryFailure(q.args, out, diagnostic, "UNAVAILABLE", 3)
	}
	a.Evidence = store
	return cliCommands(core.Runner{Provider: provider, Evidence: store}, a, archive.Runner{Provider: provider, Evidence: store, ProtectedRoots: protectedInventory(cfg, root)}).run(ctx, q.args, in, out, diagnostic)
}

func entryFailure(args []string, out, diagnostic io.Writer, code string, exit int) int {
	jsonMode := false
	for i, arg := range args {
		if arg == "--json" {
			jsonMode = true
		}
		if arg == "operation" && i+1 < len(args) && args[i+1] == "read" {
			exit = 4
		}
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
	if !jsonMode {
		if _, e := fmt.Fprintln(out, "Request unavailable or refused"); e != nil {
			if exit == 4 {
				return 4
			}
			return 1
		}
	}
	body, e := T.Readable(core.Refused(code, exit).Value())
	if e != nil {
		return exit
	}
	if n, e := out.Write(body); e != nil || n != len(body) {
		if exit == 4 {
			return 4
		}
		return 1
	}
	return exit
}
