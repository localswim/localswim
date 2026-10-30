package open

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/localswim/localswim/internal/store/schema"
)

// InspectionRead is a SELECT-only facet compatible with domain reader adapters.
// Callers must use it synchronously and finish rows within the callback. No
// connection is exposed; retained facets and rows become unusable on return.
type InspectionRead interface {
	Query(string, ...any) (*sql.Rows, error)
	Scan(string, []any, ...any) error
}

// InspectExisting exclusively inspects an offline store without creating files,
// changing database state, collecting bytes or adopting unsupported layouts.
// Every native file is opened existing-only. One read-only SQLite connection,
// bounded by ten seconds and the caller deadline, holds the audit and callback
// in the same actual read transaction. The caller owns any wider root lease and
// complete native inventory/content proof; this is not online authorization.
func InspectExisting(ctx context.Context, dir string, id schema.Identity, fn func(InspectionRead) error) error {
	return inspectExisting(ctx, dir, id, fn, schema.Validate)
}
func inspectExisting(ctx context.Context, dir string, id schema.Identity, fn func(InspectionRead) error, validate func(context.Context, *sql.Conn, schema.Identity) error) (err error) {
	if ctx == nil || fn == nil {
		return errors.New("inspection context and callback required")
	}
	if !schema.ValidUUID(id.BoardID) || !schema.ValidUUID(id.Incarnation) {
		return errors.New("invalid storage identity")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return err
	}
	g, err := acquireInspectionPath(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, g.close()) }()
	db, err := pool(g.path, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	c, err := db.Conn(ctx)
	if err != nil {
		return errors.New("inspection connection unavailable")
	}
	defer func() { err = errors.Join(err, c.Close()) }()
	if err = configure(ctx, c, true, false); err != nil {
		return err
	}
	if _, err = c.ExecContext(ctx, "BEGIN"); err != nil {
		return errors.New("inspection transaction unavailable")
	}
	defer func() { err = errors.Join(err, rollback(c)) }()
	if err = validate(ctx, c, id); err != nil {
		return err
	}
	r := &inspectionReader{ctx: ctx, conn: c}
	defer func() { err = errors.Join(err, r.close()) }()
	// End the actual read transaction on deadline even if trusted callback code
	// is temporarily doing non-SQL work. Inactivate the facet before rollback;
	// join this cleanup before returning the connection or releasing native pins.
	expired := make(chan error, 1)
	stop := context.AfterFunc(ctx, func() { expired <- errors.Join(r.close(), rollback(c)) })
	defer func() {
		if !stop() {
			err = errors.Join(err, <-expired)
		}
	}()
	if err = fn(r); err != nil {
		return err
	}
	return ctx.Err()
}

type inspectionReader struct {
	mu     sync.Mutex
	ctx    context.Context
	conn   *sql.Conn
	rows   []*sql.Rows
	closed bool
}

func (r *inspectionReader) Query(q string, args ...any) (*sql.Rows, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	if !inspectionSelect(q) {
		return nil, errors.New("inspection requires one SELECT")
	}
	rows, err := r.conn.QueryContext(r.ctx, q, args...)
	if err == nil {
		r.rows = append(r.rows, rows)
	}
	return rows, err
}

func (r *inspectionReader) Scan(q string, args []any, dest ...any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	if !inspectionSelect(q) {
		return errors.New("inspection requires one SELECT")
	}
	return r.conn.QueryRowContext(r.ctx, q, args...).Scan(dest...)
}

func (r *inspectionReader) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	var err error
	for _, rows := range r.rows {
		err = errors.Join(err, rows.Close())
	}
	r.rows = nil
	return err
}

// The facade deliberately accepts the SQL vocabulary used by logical projection:
// one SELECT, with quoted literals/identifiers and nested SELECTs. Outside quotes,
// reject statement separators and comments so no trailing statement can disable
// query_only, attach/create a file, change a PRAGMA or end the pinned transaction.
// SQLite mode=ro and query_only independently protect the actual connection.
func inspectionSelect(q string) bool {
	q = strings.TrimSpace(q)
	if len(q) < 7 || !strings.EqualFold(q[:6], "SELECT") || !strings.ContainsRune(" \t\r\n(", rune(q[6])) {
		return false
	}
	var quote byte
	for i := 6; i < len(q); i++ {
		ch := q[i]
		if quote != 0 {
			if ch == quote {
				if i+1 < len(q) && q[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			quote = ch
			continue
		}
		if ch == '[' {
			quote = ']'
			continue
		}
		if ch == ';' || ch == 0 || i+1 < len(q) && (q[i:i+2] == "--" || q[i:i+2] == "/*") {
			return false
		}
	}
	return quote == 0
}
