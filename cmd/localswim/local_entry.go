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
	"github.com/localswim/localswim/internal/workspace"
)

type localInvocation struct {
	data      string
	workspace string
	project   string
	board     string
	port      int
	serve     bool
	args      []string
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
			case "--workspace":
				q.workspace = args[i+1]
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
		if seen["--data"] == seen["--workspace"] {
			return q, errComposition
		}
		if seen["--workspace"] && q.workspace == "" {
			return q, errComposition
		}
	} else if args[0] == "--workspace" {
		if e := parseWorkspaceLocal(args, &q); e != nil {
			return q, e
		}
	} else {
		if len(args) < 3 || args[0] != "--data" {
			return q, errComposition
		}
		q.data = args[1]
		q.args = append([]string(nil), args[2:]...)
		for _, arg := range q.args {
			switch arg {
			case "--data", "--workspace", "--project", "--port", "--config", "--actor", "--native-owner", "--installation", "--board", "--incarnation", "--grant":
				return q, errComposition
			}
		}
	}
	selected := &q.data
	if q.workspace != "" {
		selected = &q.workspace
	}
	if *selected == "" || strings.ContainsAny(*selected, "\x00\r\n") {
		return q, errComposition
	}
	var e error
	*selected, e = filepath.Abs(filepath.FromSlash(*selected))
	if e != nil || !safeAbsolute(*selected) {
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
	connectionContext := evidence.Context{InstallationID: info.InstallationID, Origin: info.Origin}
	metadata := core.CredentialMetadata{Selected: actual, ActorID: info.ActorID, ExpiresAt: time.Now().Add(time.Hour)}
	if info.WorkspaceID != "" {
		return core.NewLocalWorkspaceBoardConnection(connectionContext, metadata, info.Token, info.WorkspaceID)
	}
	return core.NewLocalBoardConnection(connectionContext, metadata, info.Token)
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
		_, e := fmt.Fprintln(out, "Localswim: localhost swimlanes\n  localswim serve --data DIR [--port PORT]\n  localswim serve --workspace DIR [--port PORT]\n"+localDomainCLIHelp+"\n"+workspaceCLIHelp+"\n  localswim restore --source FILE --data NEWDIR [--port PORT] [--json]\n  localswim restore-status --data DIR --request-id UUID [--json]\nA new workspace requires an explicit port; reopen uses its saved port.\nBrowser and CLI share the local service. No setup, login codes or TLS enrollment.")
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
		if !retryFailedServeLifetimes() {
			return entryFailure(nil, out, diagnostic, "UNAVAILABLE", 3)
		}
		a, e := assets.Handler()
		if e != nil {
			return entryFailure(nil, out, diagnostic, "UNAVAILABLE", 3)
		}
		var r serveRuntime
		if q.workspace != "" {
			started, startErr := workspace.Start(ctx, workspace.Options{DataDir: q.workspace, Port: q.port, Assets: a})
			e = startErr
			if started != nil {
				r = started
			}
		} else {
			started, startErr := local.Start(ctx, local.Options{DataDir: q.data, Port: q.port, Assets: a})
			e = startErr
			if started != nil {
				r = started
			}
		}
		if e != nil {
			_ = finishServe(r)
			return entryFailure(nil, out, diagnostic, "UNAVAILABLE", 3)
		}
		if out == nil {
			_ = finishServe(r)
			return 1
		}
		if _, e = fmt.Fprintln(out, "Localswim "+r.Origin()+"/"); e != nil {
			_ = finishServe(r)
			return 1
		}
		select {
		case <-ctx.Done():
		case <-r.Done():
		}
		if finishServe(r) != nil {
			return entryFailure(nil, out, diagnostic, "UNAVAILABLE", 3)
		}
		return 0
	}
	if q.workspace != "" && !q.serve {
		return runWorkspaceCLI(ctx, q, in, out, diagnostic)
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
