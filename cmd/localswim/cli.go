package main

import (
	"context"
	"io"

	"github.com/localswim/localswim/internal/transport/cli/admin"
	"github.com/localswim/localswim/internal/transport/cli/archive"
	"github.com/localswim/localswim/internal/transport/cli/core"
)

type commandRunner interface {
	Run(context.Context, []string, io.Reader, io.Writer, io.Writer) int
}

type commands struct{ core, admin, archive commandRunner }

func cliCommands(c core.Runner, a admin.Runner, x archive.Runner) commands { return commands{c, a, x} }

// Dispatch examines command family only. The accepted owning parser always
// receives the complete unchanged argument vector and owns all grammar failures.
// Setup's any-order flags are intentionally left to the admin parser.
func (c commands) run(ctx context.Context, args []string, in io.Reader, out, diagnostic io.Writer) int {
	if setupInvocation(args) {
		return c.admin.Run(ctx, args, in, out, diagnostic)
	}
	p, e := core.ParsePrefix(args)
	if e == nil && len(p.Args) > 0 {
		switch p.Args[0] {
		case "grant", "credential", "recovery", "session":
			return c.admin.Run(ctx, args, in, out, diagnostic)
		case "attachment", "export", "restore":
			if len(p.Args) < 2 || archiveCommand(p.Args[0], p.Args[1]) {
				return c.archive.Run(ctx, args, in, out, diagnostic)
			}
		}
	}
	return c.core.Run(ctx, args, in, out, diagnostic)
}

func setupInvocation(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "--json" {
			continue
		}
		if len(args[i]) >= 2 && args[i][:2] == "--" {
			i++
			continue
		}
		if args[i] == "setup" {
			return true
		}
		break
	}
	return false
}

// Keep the owning parser and runner aligned across explicit, local and workspace
// entry points. Detach is an ordinary JSON mutation with no byte-transfer input.
func archiveCommand(family, action string) bool {
	return family == "export" || family == "restore" || family == "attachment" && action != "detach"
}
