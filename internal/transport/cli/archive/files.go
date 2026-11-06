package archive

import (
	"context"
	"fmt"
	B "github.com/localswim/localswim/internal/board/attachments"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"strings"
)

func readFiles(ctx context.Context, c *core.Client, q invocation) result {
	path := base(q) + "/cards/" + q.id + "/attachments"
	if q.kind == "attachment inspect" {
		path += "/" + q.attachment
	} else if q.query != "" {
		path += "?" + q.query
	}
	status, headers, raw, e := c.Request(ctx, "GET", path, nil, "", "application/json", T.FileReadResponseLimit)
	if e != nil || !jsonHeaders(headers) {
		return refused("UNAVAILABLE", 3)
	}
	if status != 200 {
		return classifyRead(status, raw)
	}
	var value any
	var identity A.Identity
	if q.kind == "attachment inspect" {
		x, e := T.Decode[T.CardFileRead](raw)
		if e != nil || string(x.Result.CardID) != q.id || string(x.Result.File.Attachment.AttachmentID) != q.attachment {
			return refused("UNAVAILABLE", 3)
		}
		value = x
		identity = x.Result.Identity
	} else {
		x, e := T.Decode[T.CardFilesRead](raw)
		query, _ := B.ParseListQuery(q.query)
		if e != nil || string(x.Result.CardID) != q.id || string(x.Result.View) != query.View || len(x.Result.Items) > query.Limit {
			return refused("UNAVAILABLE", 3)
		}
		value = x
		identity = x.Result.Identity
	}
	if string(identity.BoardID) != q.selected.BoardID || string(identity.Incarnation) != q.selected.Incarnation {
		return refused("INCARNATION_CONFLICT", 2)
	}
	return result{value: value}
}
func readableFiles(value any) (string, bool) {
	var out strings.Builder
	var files []T.CardFile
	switch x := value.(type) {
	case T.CardFilesRead:
		fmt.Fprintf(&out, "Board %s/%s revision %d\nCard %s revision %d\nView %s\n", x.Result.Identity.BoardID, x.Result.Identity.Incarnation, x.Result.Revision, x.Result.CardID, x.Result.CardRevision, x.Result.View)
		files = x.Result.Items
		if x.Result.NextCursor != nil {
			fmt.Fprintf(&out, "Continuation %s\n", *x.Result.NextCursor)
		}
	case T.CardFileRead:
		fmt.Fprintf(&out, "Board %s/%s revision %d\nCard %s revision %d\n", x.Result.Identity.BoardID, x.Result.Identity.Incarnation, x.Result.Revision, x.Result.CardID, x.Result.CardRevision)
		files = []T.CardFile{x.Result.File}
	default:
		return "", false
	}
	for _, f := range files {
		a := f.Attachment
		fmt.Fprintf(&out, "File %s\nState %s, download %s\nSource %s/%s at %s\nName %q\nMedia %q\nBytes %d SHA256 %s\n", a.AttachmentID, f.ReferenceState, f.DownloadState, a.CreatedIn.BoardID, a.CreatedIn.Incarnation, a.CreatedAt, a.Name, a.MediaLabel, a.ByteLength, a.SHA256)
		if f.Attribution.State == "known" {
			fmt.Fprintf(&out, "Attribution %s %s %q via %s\n", *f.Attribution.ActorID, *f.Attribution.ActorKind, *f.Attribution.ActorLabel, *f.Attribution.Basis)
		} else {
			out.WriteString("Attribution unavailable\n")
		}
	}
	return out.String(), true
}
