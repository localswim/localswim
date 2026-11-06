package attachments

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
)

var ErrInvalidInput = errors.New("invalid file query")
var ErrRevisionConflict = errors.New("file revision conflict")

type ListQuery struct {
	View   string
	Limit  int
	Cursor string
}
type fileCursor struct {
	Version           A.Version            `json:"version"`
	BoardID           A.UUID               `json:"boardId"`
	Incarnation       A.UUID               `json:"incarnation"`
	CardID            A.UUID               `json:"cardId"`
	BoardRevision     A.Number             `json:"boardRevision"`
	CardRevision      A.Positive           `json:"cardRevision"`
	View              T.FileReferenceState `json:"view"`
	AfterCreatedAt    A.Timestamp          `json:"afterCreatedAt"`
	AfterAttachmentID A.UUID               `json:"afterAttachmentId"`
}

func ParseListQuery(raw string) (ListQuery, error) {
	q := ListQuery{View: "current", Limit: 50}
	values, e := url.ParseQuery(raw)
	if e != nil {
		return q, ErrInvalidInput
	}
	for k, v := range values {
		if len(v) != 1 || v[0] == "" {
			return q, ErrInvalidInput
		}
		switch k {
		case "view":
			q.View = v[0]
		case "limit":
			n, e := strconv.Atoi(v[0])
			if e != nil || strconv.Itoa(n) != v[0] {
				return q, ErrInvalidInput
			}
			q.Limit = n
		case "cursor":
			q.Cursor = v[0]
		default:
			return q, ErrInvalidInput
		}
	}
	if q.View != "current" && q.View != "retained" || q.Limit < 1 || q.Limit > 100 {
		return q, ErrInvalidInput
	}
	if q.Cursor != "" {
		if _, e := decodeFileCursor(q.Cursor); e != nil {
			return q, e
		}
	}
	return q, nil
}
func decodeFileCursor(s string) (fileCursor, error) {
	var out fileCursor
	if len(s) > 1024 {
		return out, ErrInvalidInput
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	if e != nil || base64.RawURLEncoding.EncodeToString(b) != s {
		return out, ErrInvalidInput
	}
	out, e = A.Decode[fileCursor](b)
	if e != nil {
		return out, ErrInvalidInput
	}
	v, e := contract.DecodeJSON(b)
	if e != nil {
		return out, ErrInvalidInput
	}
	canonical, e := contract.Canonical(v)
	if e != nil || !bytes.Equal(b, canonical) || out.Version != 1 || out.View != "current" && out.View != "retained" {
		return out, ErrInvalidInput
	}
	return out, nil
}
func encodeFileCursor(c fileCursor) string {
	b, _ := json.Marshal(c)
	v, _ := contract.DecodeJSON(b)
	b, _ = contract.Canonical(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

// List and Inspect capture authority, revisions, membership and attribution in one reader snapshot.
func (s *Service) List(ctx context.Context, g *grants.Service, board, inc, actor, card string, q ListQuery) (T.CardFilesRead, error) {
	out := T.CardFilesRead{APIVersion: 1, Scope: "card-files", Result: T.CardFilesView{Identity: A.Identity{BoardID: A.UUID(board), Incarnation: A.UUID(inc)}, CardID: A.UUID(card), View: T.FileReferenceState(q.View), Items: []T.CardFile{}}}
	if q.View != "current" && q.View != "retained" || q.Limit < 1 || q.Limit > 100 {
		return out, ErrInvalidInput
	}
	e := s.fileSnapshot(ctx, g, board, inc, actor, card, func(r readAccess, revision A.Number, cardRevision A.Positive) error {
		out.Result.Revision = revision
		out.Result.CardRevision = cardRevision
		var afterTime, afterID string
		if q.Cursor != "" {
			c, e := decodeFileCursor(q.Cursor)
			if e != nil {
				return e
			}
			if string(c.BoardID) != board || string(c.Incarnation) != inc || string(c.CardID) != card || string(c.View) != q.View {
				return ErrInvalidInput
			}
			if c.BoardRevision != revision || c.CardRevision != cardRevision {
				return ErrRevisionConflict
			}
			afterTime = string(c.AfterCreatedAt)
			afterID = string(c.AfterAttachmentID)
		}
		predicate := "EXISTS"
		if q.View == "retained" {
			predicate = "NOT EXISTS"
		}
		rows, e := r.Query(fileSelect+" WHERE a.incarnation=? AND a.card_id=? AND "+predicate+" (SELECT 1 FROM current_reference cr WHERE cr.incarnation=a.incarnation AND cr.card_id=a.card_id AND cr.kind='attachment' AND cr.entity_id=a.attachment_id) AND (a.created_at>? OR (a.created_at=? AND a.attachment_id>?)) ORDER BY a.created_at,a.attachment_id LIMIT ?", inc, card, afterTime, afterTime, afterID, q.Limit+1)
		if e != nil {
			return e
		}
		items, e := readFiles(rows)
		if e != nil {
			return e
		}

		for index := 0; index < len(items) && index < q.Limit; index++ {
			candidate := []T.CardFile{items[index]}
			if e = attributeFiles(r, inc, candidate); e != nil {
				if errors.Is(e, ErrUnavailable) && len(out.Result.Items) > 0 {
					break
				}
				return e
			}
			previousCursor := out.Result.NextCursor
			out.Result.Items = append(out.Result.Items, candidate[0])
			out.Result.NextCursor = nil
			if index+1 < len(items) {
				last := candidate[0].Attachment
				cursor := encodeFileCursor(fileCursor{1, A.UUID(board), A.UUID(inc), A.UUID(card), revision, cardRevision, T.FileReferenceState(q.View), last.CreatedAt, last.AttachmentID})
				out.Result.NextCursor = &cursor
			}
			size, err := T.FileReadSize(out)
			if err != nil || size > T.FileReadResponseLimit || size > T.FileReadPageTarget && len(out.Result.Items) > 1 {
				out.Result.Items = out.Result.Items[:len(out.Result.Items)-1]
				out.Result.NextCursor = previousCursor
				if len(out.Result.Items) == 0 {
					return ErrUnavailable
				}
				break
			}
			if size > T.FileReadPageTarget {
				break
			}
		}
		return nil
	})
	return out, e
}
func (s *Service) Inspect(ctx context.Context, g *grants.Service, board, inc, actor, card, id string) (T.CardFileRead, error) {
	out := T.CardFileRead{APIVersion: 1, Scope: "card-file", Result: T.CardFileView{Identity: A.Identity{BoardID: A.UUID(board), Incarnation: A.UUID(inc)}, CardID: A.UUID(card)}}
	if !schema.ValidUUID(id) {
		return out, ErrInvalidInput
	}
	e := s.fileSnapshot(ctx, g, board, inc, actor, card, func(r readAccess, revision A.Number, cardRevision A.Positive) error {
		out.Result.Revision = revision
		out.Result.CardRevision = cardRevision
		rows, e := r.Query(fileSelect+" WHERE a.incarnation=? AND a.card_id=? AND a.attachment_id=?", inc, card, id)
		if e != nil {
			return e
		}
		items, e := readFiles(rows)
		if e != nil {
			return e
		}
		if len(items) != 1 {
			return ErrNotFound
		}
		if e = attributeFiles(r, inc, items); e != nil {
			return e
		}
		out.Result.File = items[0]
		size, e := T.FileReadSize(out)
		if e != nil || size > T.FileReadResponseLimit {
			return ErrUnavailable
		}
		return nil
	})
	return out, e
}

const fileSelect = "SELECT a.attachment_id,a.card_id,a.name,a.media_label,a.sha256,a.byte_length,a.created_at,e.origin_board,e.origin_incarnation,EXISTS(SELECT 1 FROM current_reference cr WHERE cr.incarnation=a.incarnation AND cr.card_id=a.card_id AND cr.kind='attachment' AND cr.entity_id=a.attachment_id) FROM attachment a JOIN entity e ON e.incarnation=a.incarnation AND e.kind='attachment' AND e.entity_id=a.attachment_id AND e.removed_at IS NULL"

func readFiles(rows *sql.Rows) ([]T.CardFile, error) {
	defer rows.Close()
	out := []T.CardFile{}
	for rows.Next() {
		f := T.CardFile{ReferenceState: "retained", DownloadState: "not_current", Attribution: T.FileAttribution{State: "unavailable"}}
		a := &f.Attachment
		var current bool
		if e := rows.Scan(&a.AttachmentID, &a.CardID, &a.Name, &a.MediaLabel, &a.SHA256, &a.ByteLength, &a.CreatedAt, &a.CreatedIn.BoardID, &a.CreatedIn.Incarnation, &current); e != nil {
			return nil, e
		}
		if current {
			f.ReferenceState = "current"
			f.DownloadState = "current_reference"
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *Service) fileSnapshot(ctx context.Context, g *grants.Service, board, inc, actor, card string, fn func(readAccess, A.Number, A.Positive) error) error {
	if s == nil || s.store == nil || g == nil || ctx == nil || !schema.ValidUUID(board) || !schema.ValidUUID(inc) || !schema.ValidUUID(actor) || !schema.ValidUUID(card) {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	l, e := s.store.Reader(ctx)
	if e != nil {
		return ErrUnavailable
	}
	var cause error
	e = l.Do(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "BEGIN"); e != nil {
			return e
		}
		r := readAccess{ctx, c}
		if e := g.Require(ctx, r, board, inc, actor, "read"); e != nil {
			cause = grants.ErrDenied
			return nil
		}
		var rev A.Number
		var cr A.Positive
		e := r.Scan("SELECT b.revision,c.revision FROM board b JOIN card c ON c.incarnation=b.incarnation WHERE b.board_id=? AND b.incarnation=? AND c.card_id=?", []any{board, inc, card}, &rev, &cr)
		if errors.Is(e, sql.ErrNoRows) {
			cause = ErrNotFound
			return nil
		}
		if e != nil {
			return e
		}
		cause = fn(r, rev, cr)
		return nil
	})
	closeErr := l.Close()
	if e != nil || closeErr != nil {
		return ErrUnavailable
	}
	if cause != nil && !errors.Is(cause, ErrNotFound) && !errors.Is(cause, ErrInvalidInput) && !errors.Is(cause, ErrRevisionConflict) && !errors.Is(cause, grants.ErrDenied) {
		return ErrUnavailable
	}
	return cause
}

// Candidate rows are capped per returned attachment before transfer to Go.
// Headers and references suffice for both full and compact source originals.
func attributeFiles(r readAccess, inc string, items []T.CardFile) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	args := []any{inc}
	for i, f := range items {
		ids[i] = "?"
		args = append(args, string(f.Attachment.AttachmentID))
	}
	filter := strings.Join(ids, ",")
	op := `SELECT attachment_id,actor_id,kind,label_bytes FROM (SELECT a.attachment_id,o.actor_id,ar.kind,length(CAST(ar.label AS BLOB)) AS label_bytes,row_number() OVER(PARTITION BY a.attachment_id ORDER BY o.op_id) AS n FROM attachment a JOIN entity e ON e.incarnation=a.incarnation AND e.kind='attachment' AND e.entity_id=a.attachment_id JOIN operation o ON o.incarnation=a.incarnation AND o.origin_board=e.origin_board AND o.origin_incarnation=e.origin_incarnation AND o.command='attachment.add' AND o.terminal_class='COMMITTED' AND o.target_kind='card' AND o.target_id=a.card_id AND o.terminal_at=a.created_at JOIN operation_reference ref ON ref.incarnation=o.incarnation AND ref.origin_board=o.origin_board AND ref.origin_incarnation=o.origin_incarnation AND ref.op_id=o.op_id AND ref.kind='attachment' AND ref.entity_id=a.attachment_id LEFT JOIN actor ar ON ar.incarnation=o.incarnation AND ar.actor_id=o.actor_id WHERE a.incarnation=? AND a.attachment_id IN (` + filter + `)) WHERE n<=2`
	found, e := attributionCandidates(r, op, args, "operation", items)
	if e != nil {
		return e
	}
	missing := []string{}
	args = []any{inc}
	for i, f := range items {
		if !found[i] {
			missing = append(missing, "?")
			args = append(args, string(f.Attachment.AttachmentID))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	activity := `SELECT attachment_id,actor_id,kind,label_bytes FROM (SELECT a.attachment_id,ev.actor_id,ar.kind,length(CAST(ar.label AS BLOB)) AS label_bytes,row_number() OVER(PARTITION BY a.attachment_id ORDER BY ev.event_id) AS n FROM attachment a JOIN entity e ON e.incarnation=a.incarnation AND e.kind='attachment' AND e.entity_id=a.attachment_id JOIN activity ev ON ev.incarnation=a.incarnation AND ev.origin_board=e.origin_board AND ev.origin_incarnation=e.origin_incarnation AND ev.type='attachment.add' AND ev.at=a.created_at LEFT JOIN actor ar ON ar.incarnation=ev.incarnation AND ar.actor_id=ev.actor_id WHERE a.incarnation=? AND a.attachment_id IN (` + strings.Join(missing, ",") + `) AND EXISTS(SELECT 1 FROM json_each(ev.affected) j WHERE json_extract(j.value,'$.kind')='attachment' AND json_extract(j.value,'$.id')=a.attachment_id) AND EXISTS(SELECT 1 FROM json_each(ev.affected) j WHERE json_extract(j.value,'$.kind')='card' AND json_extract(j.value,'$.id')=a.card_id)) WHERE n<=2`
	_, e = attributionCandidates(r, activity, args, "activity", items)
	return e
}
func attributionCandidates(r readAccess, q string, args []any, basis T.FileAttributionBasis, items []T.CardFile) (map[int]bool, error) {
	rows, e := r.Query(q, args...)
	if e != nil {
		return nil, e
	}
	type header struct {
		id, actor  string
		kind       sql.NullString
		labelBytes sql.NullInt64
	}
	headers := []header{}
	for rows.Next() {
		var h header
		if e = rows.Scan(&h.id, &h.actor, &h.kind, &h.labelBytes); e != nil {
			rows.Close()
			return nil, e
		}
		headers = append(headers, h)
	}
	e = rows.Err()
	closeErr := rows.Close()
	if e != nil {
		return nil, e
	}
	if closeErr != nil {
		return nil, closeErr
	}
	found := map[int]bool{}
	for _, h := range headers {
		for i := range items {
			if string(items[i].Attachment.AttachmentID) != h.id {
				continue
			}
			if found[i] || !h.kind.Valid || !h.labelBytes.Valid {
				return nil, ErrIntegrity
			}
			found[i] = true
		}
	}
	for _, h := range headers {
		if h.labelBytes.Int64 > T.FileActorLabelLimit {
			return nil, ErrUnavailable
		}
		var label string
		if e = r.Scan("SELECT label FROM actor WHERE incarnation=? AND actor_id=?", []any{args[0], h.actor}, &label); e != nil {
			return nil, e
		}
		a := A.UUID(h.actor)
		k := A.Kind(h.kind.String)
		b := basis
		for i := range items {
			if string(items[i].Attachment.AttachmentID) == h.id {
				items[i].Attribution = T.FileAttribution{State: "known", ActorID: &a, ActorKind: &k, ActorLabel: &label, Basis: &b}
			}
		}
	}
	return found, nil
}
