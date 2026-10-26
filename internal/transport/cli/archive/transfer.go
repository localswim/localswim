package archive

import (
	"bytes"
	"context"
	"mime"
	"net/http"
	"strconv"

	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
)

func base(q invocation) string { return "/api/v1/boards/" + q.selected.BoardID }
func currentBoard(ctx context.Context, c *core.Client, q invocation) (T.BoardRead, *result) {
	status, headers, raw, e := c.Request(ctx, "GET", base(q), nil, "", "application/json", 32*1024*1024)
	if e != nil {
		out := refused("UNAVAILABLE", 3)
		return T.BoardRead{}, &out
	}
	if status != 200 {
		if !jsonHeaders(headers) {
			out := refused("UNAVAILABLE", 3)
			return T.BoardRead{}, &out
		}
		out := classifyRead(status, raw)
		return T.BoardRead{}, &out
	}
	if !jsonHeaders(headers) {
		out := refused("UNAVAILABLE", 3)
		return T.BoardRead{}, &out
	}
	board, e := T.Decode[T.BoardRead](raw)
	if e != nil {
		out := refused("UNAVAILABLE", 3)
		return T.BoardRead{}, &out
	}
	if string(board.Result.Identity.BoardID) != q.selected.BoardID || string(board.Result.Identity.Incarnation) != q.selected.Incarnation {
		out := refused("INCARNATION_CONFLICT", 2)
		return T.BoardRead{}, &out
	}
	return board, nil
}
func jsonHeaders(headers http.Header) bool {
	if len(headers.Values("Content-Type")) != 1 || len(headers.Values("Content-Encoding")) != 0 {
		return false
	}
	media, _, e := mime.ParseMediaType(headers.Get("Content-Type"))
	return e == nil && media == "application/json"
}
func binaryHeaders(headers http.Header, media string, raw []byte, disposition bool) bool {
	if len(headers.Values("Content-Type")) != 1 || len(headers.Values("Content-Encoding")) != 0 {
		return false
	}
	m, params, e := mime.ParseMediaType(headers.Get("Content-Type"))
	if e != nil || m != media || len(params) != 0 {
		return false
	}
	if values := headers.Values("Content-Length"); len(values) > 1 {
		return false
	} else if len(values) == 1 {
		n, e := strconv.ParseInt(values[0], 10, 64)
		if e != nil || n != int64(len(raw)) {
			return false
		}
	}
	if disposition {
		if len(headers.Values("Content-Disposition")) != 1 {
			return false
		}
		d, _, e := mime.ParseMediaType(headers.Get("Content-Disposition"))
		if e != nil || d != "attachment" {
			return false
		}
	}
	return true
}
func exportResult(outcome T.ExportOutcome, source *A.Identity, revision *A.Number, exit int, local bool) result {
	return result{value: T.ExportStatus{APIVersion: 1, Scope: "export", Outcome: outcome, Result: T.ExportResult{SourceIdentity: source, SourceRevision: revision, Destination: nil}}, exit: exit, localComplete: local}
}
func (r Runner) export(ctx context.Context, c *core.Client, q invocation, board T.BoardRead) result {
	request := T.ExportRequest{Incarnation: board.Result.Identity.Incarnation, ExpectedRevision: board.Result.Revision, Input: T.EmptyInput{}}
	wire, e := T.Readable(request)
	if e != nil {
		return refused("INVALID_INPUT", 2)
	}
	status, headers, raw, e := c.Request(ctx, "POST", base(q)+"/exports", bytes.NewReader(wire), "application/json", "application/zip", V.MaxArchiveBytes)
	if e != nil {
		return exportResult("failed", nil, nil, 1, false)
	}
	if status != 200 {
		if !jsonHeaders(headers) {
			return refused("UNAVAILABLE", 3)
		}
		return classifyRead(status, raw)
	}
	if !binaryHeaders(headers, "application/zip", raw, false) {
		return exportResult("failed", nil, nil, 1, false)
	}
	verified, e := V.Read(ctx, bytes.NewReader(raw))
	if e != nil {
		return exportResult("failed", nil, nil, 1, false)
	}
	manifest, e := verified.Manifest()
	if e != nil || manifest.Source != board.Result.Identity || manifest.Revision != board.Result.Revision {
		return exportResult("failed", nil, nil, 1, false)
	}
	state := r.files.save(ctx, q.destination, raw, r.ProtectedRoots)
	if state == saveConfirmed {
		return exportResult("confirmed", &manifest.Source, &manifest.Revision, 0, true)
	}
	if state == saveUnknown {
		return exportResult("unknown", &manifest.Source, &manifest.Revision, 4, false)
	}
	return exportResult("failed", &manifest.Source, &manifest.Revision, 1, false)
}
func (r Runner) download(ctx context.Context, c *core.Client, q invocation, board T.BoardRead) result {
	var expected *A.Attachment
	for i := range board.Result.Attachments {
		if string(board.Result.Attachments[i].AttachmentID) == q.id {
			expected = &board.Result.Attachments[i]
			break
		}
	}
	if expected == nil {
		return refused("NOT_FOUND", 2)
	}
	status, headers, raw, e := c.Request(ctx, "GET", base(q)+"/attachments/"+q.id, nil, "", "application/octet-stream", maxFile)
	if e != nil {
		return refused("UNAVAILABLE", 3)
	}
	if status != 200 {
		if !jsonHeaders(headers) {
			return refused("UNAVAILABLE", 3)
		}
		return classifyRead(status, raw)
	}
	if !binaryHeaders(headers, "application/octet-stream", raw, true) || uint64(len(raw)) != uint64(expected.ByteLength) || sha(raw) != string(expected.SHA256) {
		return refused("UNAVAILABLE", 3)
	}
	state := r.files.save(ctx, q.destination, raw, r.ProtectedRoots)
	if state == saveUnknown {
		return refused("UNAVAILABLE", 4)
	}
	if state != saveConfirmed {
		return refused("INVOCATION_OR_IO", 1)
	}
	// The authenticated current metadata remains the existing public read type;
	// no new byte-delivery envelope or path-bearing success object is invented.
	return result{value: board, exit: 0, localComplete: true}
}
