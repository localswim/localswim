// Package open owns bounded physical SQLite connections on the qualified native
// platform. It performs no domain admission and never uses database/sql.Tx.
package open

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/localswim/localswim/internal/store/schema"
	_ "modernc.org/sqlite"
)

var ErrFenced = errors.New("storage writes fenced pending recovery")
var ErrClosed = errors.New("storage closed")

// Store owns an OS path/exclusion lease plus one writer and at most two readers.
// Close refuses while leases remain: callers must release all readers (including
// export snapshots) before shutdown. A reader can block COMMIT in DELETE mode.
type Store struct {
	mu                     sync.Mutex
	writer, readers        *sql.DB
	guard                  *pathGuard
	writeSlot              chan struct{}
	readSlots              chan struct{}
	active                 int
	closed, fenced, broken bool
}

// Create exclusively creates board.sqlite in an existing private directory. A
// failed create is retained for investigation; it is never recursively removed,
// adopted, overwritten or automatically retried as a fresh database.
func Create(ctx context.Context, dir string, id schema.Identity) (*Store, error) {
	return start(ctx, dir, id, true)
}

// Existing validates identity, exact schema and integrity without migrating or
// repairing data. Unknown/corrupt/version-mismatched files fail closed.
func Existing(ctx context.Context, dir string, id schema.Identity) (*Store, error) {
	return start(ctx, dir, id, false)
}

func start(ctx context.Context, dir string, id schema.Identity, create bool) (*Store, error) {
	if !schema.ValidUUID(id.BoardID) || !schema.ValidUUID(id.Incarnation) {
		return nil, errors.New("invalid storage identity")
	}
	if ctx == nil {
		return nil, errors.New("storage context required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	g, err := acquirePath(dir, create)
	if err != nil {
		return nil, err
	}
	s := &Store{guard: g, writeSlot: make(chan struct{}, 1), readSlots: make(chan struct{}, 2)}
	ok := false
	defer func() {
		if !ok {
			if s.writer != nil {
				s.writer.Close()
			}
			if s.readers != nil {
				s.readers.Close()
			}
			g.close()
		}
	}()
	s.writer, err = pool(g.path, false)
	if err != nil {
		return nil, err
	}
	c, err := s.writer.Conn(ctx)
	if err != nil {
		return nil, errors.New("storage connection unavailable")
	}
	defer c.Close()
	if err = configure(ctx, c, false, create); err != nil {
		return nil, err
	}
	if create {
		if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return nil, errors.New("storage initialization unavailable")
		}
		if err = schema.Initialize(ctx, c, id); err != nil {
			rollback(c)
			return nil, err
		}
		if _, err = c.ExecContext(ctx, "COMMIT"); err != nil {
			rollback(c)
			return nil, errors.New("storage initialization outcome unknown")
		}
	}
	if err = schema.Validate(ctx, c, id); err != nil {
		return nil, err
	}
	s.readers, err = pool(g.path, true)
	if err != nil {
		return nil, err
	}
	r, err := s.readers.Conn(ctx)
	if err != nil {
		return nil, errors.New("storage read connection unavailable")
	}
	err = configure(ctx, r, true, false)
	r.Close()
	if err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}

func pool(path string, read bool) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}
	q := url.Values{}
	if read {
		q.Set("mode", "ro")
	} else {
		q.Set("mode", "rw")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, errors.New("storage binding unavailable")
	}
	n := 1
	if read {
		n = 2
	}
	db.SetMaxOpenConns(n)
	db.SetMaxIdleConns(n)
	return db, nil
}

// Settings are read back on EVERY checkout, including a replacement physical
// connection. journal_mode is assigned only for a brand-new file, never for an
// existing database (especially a WAL database).
func configure(ctx context.Context, c *sql.Conn, read, create bool) error {
	if create {
		var mode string
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil || mode != "delete" {
			return errors.New("storage journal initialization failed")
		}
	}
	if _, err := c.ExecContext(ctx, "PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL; PRAGMA read_uncommitted=OFF; PRAGMA busy_timeout=150"); err != nil {
		return errors.New("storage settings unavailable")
	}
	if read {
		if _, err := c.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
			return errors.New("storage reader setup failed")
		}
	}
	for p, w := range map[string]int{"foreign_keys": 1, "synchronous": 2, "read_uncommitted": 0, "busy_timeout": 150, "query_only": boolInt(read)} {
		var got int
		if err := c.QueryRowContext(ctx, "PRAGMA "+p).Scan(&got); err != nil || got != w {
			return fmt.Errorf("storage setting mismatch: %s", p)
		}
	}
	for p, w := range map[string]string{"journal_mode": "delete", "locking_mode": "normal"} {
		var got string
		if err := c.QueryRowContext(ctx, "PRAGMA "+p).Scan(&got); err != nil || got != w {
			return fmt.Errorf("storage setting mismatch: %s", p)
		}
	}
	return nil
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// Lease grants sole sequential ownership of one sql.Conn. Within Do, callers
// must finish/close all Rows and never retain the connection or start goroutines
// using it. No Raw access or concurrent calls on one connection are supported.
type Lease struct {
	mu     sync.Mutex
	store  *Store
	conn   *sql.Conn
	writer bool
	closed bool
}

func (s *Store) Writer(ctx context.Context) (*Lease, error) { return s.lease(ctx, true) }
func (s *Store) Reader(ctx context.Context) (*Lease, error) { return s.lease(ctx, false) }
func (s *Store) lease(ctx context.Context, write bool) (*Lease, error) {
	if ctx == nil {
		return nil, errors.New("storage context required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	slots := s.readSlots
	if write {
		slots = s.writeSlot
	}
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	if s.closed || s.broken || (write && s.fenced) {
		closed := s.closed
		s.mu.Unlock()
		<-slots
		if closed {
			return nil, ErrClosed
		}
		return nil, ErrFenced
	}
	s.active++
	s.mu.Unlock()
	db := s.readers
	if write {
		db = s.writer
	}
	c, err := db.Conn(ctx)
	if err == nil {
		err = configure(ctx, c, !write, false)
	}
	if err != nil {
		if c != nil {
			c.Close()
		}
		s.release(write)
		return nil, errors.New("storage lease unavailable")
	}
	return &Lease{store: s, conn: c, writer: write}, nil
}
func (s *Store) release(write bool) {
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	if write {
		<-s.writeSlot
	} else {
		<-s.readSlots
	}
}

// Do runs one sequential owner callback. Callers must use bounded contexts for
// every SQL call and finish all result sets within that callback. Domain code uses explicit SQL
// BEGIN IMMEDIATE/COMMIT/ROLLBACK. A COMMIT error is UNKNOWN, even SQLITE_BUSY;
// S02 must reconcile terminal evidence and apply its recovery/fencing policy.
func (l *Lease) Do(ctx context.Context, fn func(*sql.Conn) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	if ctx == nil || fn == nil {
		return errors.New("storage callback/context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	l.store.mu.Lock()
	fenced := l.store.broken || (l.writer && l.store.fenced)
	l.store.mu.Unlock()
	if fenced {
		return ErrFenced
	}
	return fn(l.conn)
}

// Fence is one-way for this Store. Recovery must close and explicitly reopen a
// separately validated store; no convenience reset can silently resume writes.
func (s *Store) Fence() { s.mu.Lock(); s.fenced = true; s.mu.Unlock() }

// Close always attempts explicit ROLLBACK before returning a physical connection.
// A cleanup failure fences the Store; no failed transaction is returned for reuse.
func (l *Lease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	err := rollback(l.conn)
	if err != nil {
		l.store.Fence()
		l.store.mu.Lock()
		l.store.broken = true
		l.store.mu.Unlock()
		// Close the pool so Conn.Close discards the unclean connection.
		// No Raw/internal driver API or unclean pool reuse is required.
		if l.writer {
			l.store.writer.Close()
		} else {
			l.store.readers.Close()
		}
	}
	closeErr := l.conn.Close()
	if closeErr != nil {
		l.store.Fence()
	}
	l.store.release(l.writer)
	if err != nil {
		return errors.New("storage cleanup failed; recovery required")
	}
	return closeErr
}

func rollback(c *sql.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.ExecContext(ctx, "ROLLBACK")
	if err != nil && err.Error() == "SQL logic error: cannot rollback - no transaction is active (1)" {
		return nil
	}
	return err
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.active != 0 {
		return errors.New("storage leases remain active")
	}
	s.closed = true
	a := s.readers.Close()
	b := s.writer.Close()
	c := s.guard.close()
	return errors.Join(a, b, c)
}
