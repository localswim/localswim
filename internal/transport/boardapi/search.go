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
	"strings"
	"unicode/utf8"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
)

var errCursorStale = errors.New("cursor stale")

type searchRequest struct{ archive, cursor string }

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
			if vs[0] != "" {
				return out, ErrUnavailable
			}
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

func filterDigest(archive string) string {
	// Includes the exact canonical empty query and null typed filters.
	b, _ := json.Marshal(struct {
		Q        string  `json:"q"`
		Lane     *string `json:"lane"`
		Owner    *string `json:"owner"`
		Priority *string `json:"priority"`
		Archive  string  `json:"archive"`
	}{Archive: archive})
	d := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(d[:])
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
	out.Result.Query.Archive = T.CardArchiveMode(request.archive)
	if e := r.Scan(`SELECT board_id,incarnation,revision FROM board WHERE singleton=1 AND board_id=?`, []any{q.board}, &out.Result.Identity.BoardID, &out.Result.Identity.Incarnation, &out.Result.Revision); e != nil {
		return nil, e
	}
	key := cursorPayload{Epoch: base64.RawURLEncoding.EncodeToString(h.cursorEpoch[:]), Actor: actor, Identity: out.Result.Identity, Revision: out.Result.Revision, Filter: filterDigest(request.archive)}
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
	if request.cursor != "" {
		where += " AND (c.priority>? OR (c.priority=? AND (t.ticket>? OR (t.ticket=? AND c.card_id>?))))"
		args = append(args, string(last.Priority), string(last.Priority), uint64(last.Ticket), uint64(last.Ticket), string(last.CardID))
	}
	e := rows(r, `SELECT c.card_id,t.ticket,c.revision,c.title,c.lane,c.priority,c.owner_id,c.archived FROM card c JOIN ticket t USING(incarnation,card_id) WHERE `+where+` ORDER BY c.priority,t.ticket,c.card_id LIMIT 21`, args, func(rs *sql.Rows) error {
		var c T.CardSummary
		var owner sql.NullString
		if e := rs.Scan(&c.CardID, &c.Ticket, &c.Revision, &c.Title, &c.Lane, &c.Priority, &owner, &c.Archived); e != nil {
			return e
		}
		c.OwnerID = uuidPtr(owner)
		out.Result.Items = append(out.Result.Items, c)
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
