package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/assets"
	"github.com/localswim/localswim/internal/transport/cli/archive"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	local "github.com/localswim/localswim/internal/transport/local"
)

type localInvocation struct {
	data  string
	port  int
	serve bool
	args  []string
}

func parseLocal(args []string) (localInvocation, error) {
	var q localInvocation
	if len(args) == 0 {
		return q, errComposition
	}
	if args[0] == "serve" {
		q.serve = true
		seen := map[string]bool{}
		for i := 1; i < len(args); i += 2 {
			if i+1 >= len(args) || seen[args[i]] {
				return q, errComposition
			}
			seen[args[i]] = true
			switch args[i] {
			case "--data":
				q.data = args[i+1]
			case "--port":
				n, e := strconv.Atoi(args[i+1])
				if e != nil || n < 1024 || n > 65535 || strconv.Itoa(n) != args[i+1] {
					return q, errComposition
				}
				q.port = n
			default:
				return q, errComposition
			}
		}
	} else {
		if len(args) < 3 || args[0] != "--data" {
			return q, errComposition
		}
		q.data = args[1]
		q.args = append([]string(nil), args[2:]...)
		for _, arg := range q.args {
			switch arg {
			case "--data", "--port", "--config", "--actor", "--native-owner", "--installation", "--board", "--incarnation", "--grant":
				return q, errComposition
			}
		}
	}
	if q.data == "" || strings.ContainsAny(q.data, "\x00\r\n") {
		return q, errComposition
	}
	var e error
	q.data, e = filepath.Abs(filepath.FromSlash(q.data))
	if e != nil || !safeAbsolute(q.data) {
		return q, errComposition
	}
	return q, nil
}

type localCLIProvider struct{ data string }

func (p localCLIProvider) Open(ctx context.Context, selected core.Selectors) (*core.Connection, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, &core.Failure{Code: "UNAVAILABLE", Exit: 3}
	}
	info, e := local.ReadClient(p.data)
	if e != nil {
		return nil, &core.Failure{Code: "CREDENTIAL_UNAVAILABLE", Exit: 3}
	}
	actual := core.Selectors{InstallationID: info.InstallationID, BoardID: info.BoardID, Incarnation: info.Incarnation, GrantID: info.GrantID}
	if actual != selected {
		return nil, &core.Failure{Code: "CREDENTIAL_CONTEXT_MISMATCH", Exit: 2}
	}
	return core.NewLocalBoardConnection(evidence.Context{InstallationID: info.InstallationID, Origin: info.Origin}, core.CredentialMetadata{Selected: actual, ActorID: info.ActorID, ExpiresAt: time.Now().Add(time.Hour)}, info.Token)
}

func (p localCLIProvider) NativeOwner(context.Context, evidence.Record) (*core.Connection, error) {
	return nil, &core.Failure{Code: "FORBIDDEN", Exit: 2}
}

func run(ctx context.Context, args []string, in io.Reader, out, diagnostic io.Writer) int {
	if ctx == nil || ctx.Err() != nil {
		return entryFailure(args, out, diagnostic, "UNAVAILABLE", 3)
	}
	if handled, exit := runLocalRestore(ctx, args, in, out, diagnostic); handled {
		return exit
	}
	if len(args) == 1 && args[0] == "--help" {
		if out == nil {
			return 1
		}
		_, e := fmt.Fprintln(out, "Localswim: localhost swimlanes\n  localswim serve --data DIR [--port PORT]\n  localswim --data DIR [--json] board read\n  localswim --data DIR [--json] card COMMAND ...\n  localswim restore --source FILE --data NEWDIR [--port PORT] [--json]\n  localswim restore-status --data DIR --request-id UUID [--json]\nBrowser and CLI share the local service. No setup, login codes or TLS enrollment.")
		if e != nil {
			return 1
		}
		return 0
	}
	q, e := parseLocal(args)
	if e != nil {
		return entryFailure(args, out, diagnostic, "INVOCATION_OR_IO", 1)
	}
	if q.serve {
		a, e := assets.Handler()
		if e != nil {
			return entryFailure(nil, out, diagnostic, "UNAVAILABLE", 3)
		}
		r, e := local.Start(ctx, local.Options{DataDir: q.data, Port: q.port, Assets: a})
		if e != nil {
			return entryFailure(nil, out, diagnostic, "UNAVAILABLE", 3)
		}
		if out == nil {
			_ = r.Close()
			return 1
		}
		if _, e = fmt.Fprintln(out, "Localswim "+r.Origin()+"/"); e != nil {
			_ = r.Close()
			return 1
		}
		select {
		case <-ctx.Done():
		case <-r.Done():
		}
		if r.Close() != nil {
			return entryFailure(nil, out, diagnostic, "UNAVAILABLE", 3)
		}
		return 0
	}
	info, e := local.ReadClient(q.data)
	if e != nil {
		return entryFailure(q.args, out, diagnostic, "UNAVAILABLE", 3)
	}
	originalCLIArgs := append([]string(nil), q.args...)
	prefix := []string{"--installation", info.InstallationID, "--board", info.BoardID, "--incarnation", info.Incarnation, "--grant", info.GrantID}
	if len(q.args) > 0 && q.args[0] == "--json" {
		prefix = append([]string{"--json"}, prefix...)
		q.args = q.args[1:]
	}
	store, e := evidence.NewWindowsStore(filepath.Join(q.data, "cli-evidence"))
	if e != nil {
		return entryFailure(originalCLIArgs, out, diagnostic, "INVOCATION_OR_IO", 1)
	}
	p := localCLIProvider{q.data}
	all := append(prefix, q.args...)
	if len(q.args) > 0 && (q.args[0] == "attachment" || q.args[0] == "export") {
		return (archive.Runner{Provider: p, Evidence: store, ProtectedRoots: []string{q.data}}).Run(ctx, all, in, out, diagnostic)
	}
	return (core.Runner{Provider: p, Evidence: store}).Run(ctx, all, in, out, diagnostic)
}
