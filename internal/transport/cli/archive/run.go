package archive

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
)

// Runner requires explicit current credential selection and an explicit list of
// protected live/source trees. It never loads defaults or activates a restore.
type Runner struct {
	Provider       core.Provider
	Evidence       evidence.Store
	ProtectedRoots []string
	files          nativeFiles
}

// Run emits only shared public envelopes in JSON mode. Local publication is
// complete before success is rendered; failed output cannot revoke a saved file.
func (r Runner) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	q, code, exit := parse(args)
	var out result
	if code != "" {
		out = refused(code, exit)
	} else if strings.HasPrefix(q.kind, "restore ") {
		out = offline(ctx, q, r.ProtectedRoots, stderr)
	} else {
		out = r.execute(ctx, q, stdin, stderr)
	}
	if !q.json && stdout != nil {
		label := map[int]string{0: "confirmed", 1: "failed", 2: "refused", 3: "unavailable", 4: "unknown"}[out.exit]
		board := "unavailable"
		if validID(q.selected.BoardID) {
			board = q.selected.BoardID
		}
		if restored, ok := out.value.(A.Status); ok && restored.Result.SourceIdentity != nil && validID(string(restored.Result.SourceIdentity.BoardID)) {
			board = string(restored.Result.SourceIdentity.BoardID)
		}
		if q.kind == "attachment download" {
			label = "attachment delivery " + label
		} else if q.kind == "export create" {
			label = "export delivery " + label
		} else if strings.HasPrefix(q.kind, "restore ") {
			label = "restore " + label
		}
		if _, e := fmt.Fprintf(stdout, "%s board %s\n", label, board); e != nil {
			return outputFailure(out, q)
		}
	}
	if stdout == nil {
		return outputFailure(out, q)
	}
	b, e := T.Readable(out.value)
	if e != nil {
		if out.localComplete {
			return 4
		}
		if out.exit == 0 {
			out = refused("UNAVAILABLE", 3)
		}
		b, _ = T.Readable(refused("UNAVAILABLE", out.exit).value)
	}
	if n, e := stdout.Write(b); e != nil || n != len(b) {
		return outputFailure(out, q)
	}
	if stderr != nil && out.exit != 0 {
		_, _ = io.WriteString(stderr, "REQUEST_UNAVAILABLE_OR_REFUSED\n")
	}
	return out.exit
}
func outputFailure(out result, q invocation) int {
	if out.localComplete {
		return 4
	}
	if out.exit == 4 || q.kind == "attachment add" && out.exit == 0 {
		return 4
	}
	return 1
}
func (r Runner) execute(ctx context.Context, q invocation, stdin io.Reader, stderr io.Writer) result {
	if ctx == nil || ctx.Err() != nil {
		return refused("UNAVAILABLE", 3)
	}
	if q.kind != "attachment add" && r.ProtectedRoots == nil {
		return refused("UNSAFE_LOCAL_MATERIAL", 1)
	}
	var metadata, raw []byte
	var e error
	if q.kind == "attachment add" {
		metadata, e = readMetadata(ctx, q.input, stdin)
		if e != nil {
			return refused("INVOCATION_OR_IO", 1)
		}
		raw, e = r.files.read(ctx, q.file, maxFile)
		if e != nil {
			return refused("INVOCATION_OR_IO", 1)
		}
	}
	c, e := core.Open(ctx, r.Provider, q.selected)
	if e != nil {
		code, exit := fixedFailure(e)
		return refused(code, exit)
	}
	defer c.Close()
	if q.kind == "attachment add" {
		out := c.DispatchAttachment(ctx, r.Evidence, contract.ResolvedRoute{Command: "attachment.add", BoardID: q.selected.BoardID, TargetID: q.id}, metadata, raw, stderr)
		return result{value: out.Value(), exit: out.Exit()}
	}
	board, failed := currentBoard(ctx, c, q)
	if failed != nil {
		return *failed
	}
	if q.kind == "export create" {
		return r.export(ctx, c, q, board)
	}
	return r.download(ctx, c, q, board)
}
