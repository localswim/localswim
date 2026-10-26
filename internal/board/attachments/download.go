package attachments

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/store/schema"
)

// Download is fully validated before adapters can emit headers or bytes.
type Download struct {
	Name  string
	Bytes []byte
}

type readAccess struct {
	ctx context.Context
	c   *sql.Conn
}

func selectOnly(q string) bool {
	f := strings.Fields(q)
	return len(f) > 0 && strings.EqualFold(f[0], "SELECT") && !strings.ContainsAny(q, ";\x00")
}
func (r readAccess) Scan(q string, args []any, dest ...any) error {
	if !selectOnly(q) {
		return ErrUnavailable
	}
	return r.c.QueryRowContext(r.ctx, q, args...).Scan(dest...)
}
func (r readAccess) Query(q string, args ...any) (*sql.Rows, error) {
	if !selectOnly(q) {
		return nil, ErrUnavailable
	}
	return r.c.QueryContext(r.ctx, q, args...)
}

// Read requires ordinary read authority, exact credential board/incarnation/
// actor binding, and the live attachment association in one actual reader
// snapshot. Native owner and attach-only authority never imply ordinary reads.
func (s *Service) Read(ctx context.Context, g *grants.Service, board, inc, actor, attachment string) (Download, error) {
	var out Download
	if s == nil || s.store == nil || g == nil || ctx == nil || !schema.ValidUUID(board) || !schema.ValidUUID(inc) || !schema.ValidUUID(actor) || !schema.ValidUUID(attachment) {
		return out, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	l, e := s.store.Reader(ctx)
	if e != nil {
		return out, ErrUnavailable
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
		var hash string
		var length uint64
		e := r.Scan("SELECT a.name,a.sha256,a.byte_length FROM attachment a JOIN current_reference r ON r.incarnation=a.incarnation AND r.card_id=a.card_id AND r.kind='attachment' AND r.entity_id=a.attachment_id JOIN entity e ON e.incarnation=a.incarnation AND e.kind='attachment' AND e.entity_id=a.attachment_id WHERE a.incarnation=? AND a.attachment_id=? AND e.removed_at IS NULL", []any{inc, attachment}, &out.Name, &hash, &length)
		if errors.Is(e, sql.ErrNoRows) {
			cause = ErrNotFound
			return nil
		}
		if e != nil {
			return e
		}
		var stored, actual uint64
		e = r.Scan("SELECT byte_length,length(bytes) FROM blob WHERE incarnation=? AND sha256=?", []any{inc, hash}, &stored, &actual)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e != nil || length > MaxFileBytes || stored != length || actual != length {
			s.store.Fence()
			cause = ErrIntegrity
			return nil
		}
		if e = r.Scan("SELECT bytes FROM blob WHERE incarnation=? AND sha256=?", []any{inc, hash}, &out.Bytes); e != nil {
			return e
		}
		if uint64(len(out.Bytes)) != length || digest(out.Bytes) != hash {
			s.store.Fence()
			out = Download{}
			cause = ErrIntegrity
		}
		return nil
	})
	closeErr := l.Close()
	if e != nil || closeErr != nil {
		return Download{}, ErrUnavailable
	}
	if cause != nil {
		return Download{}, cause
	}
	return out, nil
}
