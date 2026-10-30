package boardapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

var errCursorStale = errors.New("cursor stale")
var errPageComplete = errors.New("page complete")

type searchRequest struct{ q, lane, owner, priority, archive, cursor string }

func parseSearch(raw string) (searchRequest, error) {
	out := searchRequest{archive: "active"}
	v, e := url.ParseQuery(raw)
	if e != nil {
		return out, ErrUnavailable
	}
	for k, vs := range v {
		if len(vs) != 1 || !utf8.ValidString(vs[0]) {
			return out, ErrUnavailable
		}
		switch k {
		case "q":
			if len(vs[0]) > 1024 {
				return out, ErrUnavailable
			}
			out.q = vs[0]
		case "lane":
			switch vs[0] {
			case "Ready for Work", "In Progress", "Ready for Review", "Completed":
			default:
				return out, ErrUnavailable
			}
			out.lane = vs[0]
		case "owner":
			if vs[0] != "unassigned" && !schema.ValidUUID(vs[0]) {
				return out, ErrUnavailable
			}
			out.owner = vs[0]
		case "priority":
			if len(vs[0]) != 2 || vs[0][0] != 'P' || vs[0][1] < '0' || vs[0][1] > '5' {
				return out, ErrUnavailable
			}
			out.priority = vs[0]
		case "archive":
			if vs[0] != "active" && vs[0] != "archived" && vs[0] != "all" {
				return out, ErrUnavailable
			}
			out.archive = vs[0]
		case "cursor":
			if len(vs[0]) == 0 || len(vs[0]) > 2048 {
				return out, ErrUnavailable
			}
			for _, c := range vs[0] {
				if c < 0x21 || c > 0x7e {
					return out, ErrUnavailable
				}
			}
			parts := strings.Split(vs[0], ".")
			if len(parts) != 2 {
				return out, ErrUnavailable
			}
			payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
			if err != nil || len(payload) == 0 {
				return out, ErrUnavailable
			}
			signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
			if err != nil || len(signature) != sha256.Size {
				return out, ErrUnavailable
			}
			out.cursor = vs[0]
		default:
			return out, ErrUnavailable
		}
	}
	return out, nil
}

type cursorPayload struct {
	Epoch    string     `json:"epoch"`
	Actor    string     `json:"actor"`
	Identity A.Identity `json:"identity"`
	Revision A.Number   `json:"revision"`
	Filter   string     `json:"filter"`
	Priority A.Priority `json:"priority"`
	Ticket   A.Positive `json:"ticket"`
	CardID   A.UUID     `json:"cardId"`
}

func (r searchRequest) canonical() T.CardSearchQuery {
	q := T.CardSearchQuery{Q: r.q, Archive: T.CardArchiveMode(r.archive)}
	if r.lane != "" {
		v := A.Lane(r.lane)
		q.Lane = &v
	}
	if r.owner != "" {
		v := r.owner
		q.Owner = &v
	}
	if r.priority != "" {
		v := A.Priority(r.priority)
		q.Priority = &v
	}
	return q
}
func filterDigest(request searchRequest) string {
	b, _ := json.Marshal(request.canonical())
	d := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(d[:])
}

// Literal matching deliberately uses Go simple lowercase, without normalization
// or full case folding. Identity is exact; invalid spelling remains literal text.
func searchMatches(query string, c T.CardSummary, description string, comments []string) bool {
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(c.Title), query) || strings.Contains(strings.ToLower(description), query) {
		return true
	}
	for _, text := range comments {
		if strings.Contains(strings.ToLower(text), query) {
			return true
		}
	}
	if schema.ValidUUID(query) && query == string(c.CardID) {
		return true
	}
	digits := strings.TrimPrefix(query, "#")
	n, e := strconv.ParseUint(digits, 10, 64)
	return e == nil && n > 0 && n <= 9007199254740991 && strconv.FormatUint(n, 10) == digits && n == uint64(c.Ticket)
}
func (h *Handler) encodeCursor(p cursorPayload) string {
	b, _ := json.Marshal(p)
	m := hmac.New(sha256.New, h.cursorKey[:])
	_, _ = m.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func (h *Handler) decodeCursor(s string, expected cursorPayload) (cursorPayload, error) {
	var out cursorPayload
	if len(s) > 2048 {
		return out, errCursorStale
	}
	parts := strings.Split(s, ".")
	if len(parts) != 2 {
		return out, errCursorStale
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if e != nil {
		return out, errCursorStale
	}
	sig, e := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if e != nil || len(sig) != sha256.Size {
		return out, errCursorStale
	}
	m := hmac.New(sha256.New, h.cursorKey[:])
	_, _ = m.Write(b)
	if !hmac.Equal(sig, m.Sum(nil)) {
		return out, errCursorStale
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil {
		return out, errCursorStale
	}
	canonical, _ := json.Marshal(out)
	if !bytes.Equal(b, canonical) || out.Epoch != expected.Epoch || out.Actor != expected.Actor || out.Identity != expected.Identity || out.Revision != expected.Revision || out.Filter != expected.Filter {
		return out, errCursorStale
	}
	return out, nil
}
func (h *Handler) search(r writer.Read, q route, actor string, request searchRequest) (any, error) {
	out := T.CardSearchRead{APIVersion: 1, Scope: "card-search"}
	out.Result.Items = []T.CardSummary{}
	out.Result.Query = request.canonical()
	if e := r.Scan(`SELECT board_id,incarnation,revision FROM board WHERE singleton=1 AND board_id=?`, []any{q.board}, &out.Result.Identity.BoardID, &out.Result.Identity.Incarnation, &out.Result.Revision); e != nil {
		return nil, e
	}
	key := cursorPayload{Epoch: base64.RawURLEncoding.EncodeToString(h.cursorEpoch[:]), Actor: actor, Identity: out.Result.Identity, Revision: out.Result.Revision, Filter: filterDigest(request)}
	var last cursorPayload
	if request.cursor != "" {
		var e error
		last, e = h.decodeCursor(request.cursor, key)
		if e != nil {
			return nil, e
		}
	}
	where := "c.incarnation=?"
	args := []any{string(out.Result.Identity.Incarnation)}
	if request.archive != "all" {
		where += " AND c.archived=?"
		args = append(args, request.archive == "archived")
	}
	if request.lane != "" {
		where += " AND c.lane=?"
		args = append(args, request.lane)
	}
	if request.priority != "" {
		where += " AND c.priority=?"
		args = append(args, request.priority)
	}
	if request.owner == "unassigned" {
		where += " AND c.owner_id IS NULL"
	} else if request.owner != "" {
		where += " AND c.owner_id=?"
		args = append(args, request.owner)
	}
	if request.cursor != "" {
		where += " AND (c.priority>? OR (c.priority=? AND (t.ticket>? OR (t.ticket=? AND c.card_id>?))))"
		args = append(args, string(last.Priority), string(last.Priority), uint64(last.Ticket), uint64(last.Ticket), string(last.CardID))
	}
	lowered := strings.ToLower(request.q)
	stop := errPageComplete
	e := searchRows(r, `SELECT c.card_id,t.ticket,c.revision,c.title,c.lane,c.priority,c.owner_id,c.archived,c.description,(SELECT json_group_array(m.text) FROM current_reference ref JOIN comment m ON m.incarnation=ref.incarnation AND m.comment_id=ref.entity_id AND m.card_id=ref.card_id WHERE ref.incarnation=c.incarnation AND ref.card_id=c.card_id AND ref.kind='comment') FROM card c JOIN ticket t USING(incarnation,card_id) WHERE `+where+` ORDER BY c.priority,t.ticket,c.card_id`, args, func(rs *sql.Rows) error {
		var c T.CardSummary
		var owner sql.NullString
		var description, commentJSON string
		var comments []string
		if e := rs.Scan(&c.CardID, &c.Ticket, &c.Revision, &c.Title, &c.Lane, &c.Priority, &owner, &c.Archived, &description, &commentJSON); e != nil {
			return e
		}
		c.OwnerID = uuidPtr(owner)
		if e := json.Unmarshal([]byte(commentJSON), &comments); e != nil {
			return e
		}
		if !searchMatches(lowered, c, description, comments) {
			return nil
		}
		out.Result.Items = append(out.Result.Items, c)
		if len(out.Result.Items) == 21 {
			return stop
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if len(out.Result.Items) > 20 {
		out.Result.Items = out.Result.Items[:20]
		last := out.Result.Items[19]
		key.Priority = last.Priority
		key.Ticket = last.Ticket
		key.CardID = last.CardID
		token := h.encodeCursor(key)
		out.Result.NextCursor = &token
	}
	return out, nil
}

// Stop only after the 21st match; row/close failures still invalidate the page.
func searchRows(r writer.Read, query string, args []any, fn func(*sql.Rows) error) error {
	rs, e := r.Query(query, args...)
	if e != nil {
		return e
	}
	for rs.Next() {
		if e = fn(rs); e != nil {
			if e == errPageComplete {
				e = nil
			}
			break
		}
	}
	return errors.Join(e, rs.Err(), rs.Close())
}
