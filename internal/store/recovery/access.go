// Package recovery owns board-wide recovery selection and atomic pruning.
package recovery

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/localswim/localswim/internal/store/writer"
)

// readAccess intentionally exposes only S02's restricted read facet.
type readAccess struct {
	ctx  context.Context
	conn *sql.Conn
}

func selectOnly(q string) bool {
	f := strings.Fields(q)
	return len(f) > 0 && strings.EqualFold(f[0], "SELECT") && !strings.ContainsAny(q, ";\x00")
}
func (r readAccess) Query(q string, args ...any) (*sql.Rows, error) {
	if !selectOnly(q) {
		return nil, errors.New("single SELECT required")
	}
	return r.conn.QueryContext(r.ctx, q, args...)
}
func (r readAccess) Scan(q string, args []any, dest ...any) error {
	if !selectOnly(q) {
		return errors.New("single SELECT required")
	}
	return r.conn.QueryRowContext(r.ctx, q, args...).Scan(dest...)
}

type writeAccess struct{ readAccess }

func (r writeAccess) Exec(q string, args ...any) (sql.Result, error) {
	f := strings.Fields(q)
	if len(f) == 0 || strings.ContainsAny(q, ";\x00") || !(strings.EqualFold(f[0], "INSERT") || strings.EqualFold(f[0], "UPDATE") || strings.EqualFold(f[0], "DELETE")) {
		return nil, errors.New("single mutation required")
	}
	return r.conn.ExecContext(r.ctx, q, args...)
}

var _ writer.Write = writeAccess{}
