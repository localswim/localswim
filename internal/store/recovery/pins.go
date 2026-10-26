package recovery

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"

	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/writer"
)

// Registry is shared by every recovery/export/attachment consumer of one Store.
// RegistryFor never creates a second independent pin universe for that Store.
// It owns no SQL connection or export IO. Store shutdown still requires all
// export readers to close; callers must not retain closed Stores indefinitely.
type Registry struct {
	gate   chan struct{}
	store  *S.Store
	leases map[*ExportSnapshot][]A.ByteFact
}

var registries sync.Map

func RegistryFor(s *S.Store) *Registry {
	if s == nil {
		return nil
	}
	r := &Registry{store: s, gate: make(chan struct{}, 1), leases: map[*ExportSnapshot][]A.ByteFact{}}
	v, _ := registries.LoadOrStore(s, r)
	return v.(*Registry)
}

// PinGuard serializes exact pin observation with installation/removal and
// reclamation. Acquire ONLY after obtaining the writer slot for mutations.
// Hold it through COMMIT/reconciliation/reader cleanup. Preview/export take
// the guard BEFORE reader checkout, so an idle reader cannot consume the pool
// while waiting for recovery's guard during S02 reconciliation.
// No active reader transaction may wait to acquire this guard during cleanup.
type PinGuard struct {
	registry *Registry
	closed   bool
}

func (r *Registry) Guard(ctx context.Context) (*PinGuard, error) {
	if r == nil || ctx == nil {
		return nil, errors.New("pin context required")
	}
	select {
	case r.gate <- struct{}{}:
		return &PinGuard{registry: r}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (g *PinGuard) Close() {
	if g != nil && !g.closed {
		g.closed = true
		<-g.registry.gate
	}
}
func (g *PinGuard) Facts() ([]A.ByteFact, error) {
	if g == nil || g.closed {
		return nil, errors.New("pin guard closed")
	}
	m := map[A.Digest]A.Number{}
	for _, facts := range g.registry.leases {
		for _, f := range facts {
			if e := addFact(m, f); e != nil {
				return nil, e
			}
		}
	}
	return factList(m), nil
}

// CheckQuota checks the distinct live/full/export protected union. A01 must use
// this same guard at writer admission when adding a new protected digest.
func (g *PinGuard) CheckQuota(r writer.Read) error {
	f, e := g.Facts()
	if e != nil {
		return e
	}
	p, e := domainPins(r, nil)
	if e != nil {
		return e
	}
	for _, v := range f {
		if e = addFact(p, v); e != nil {
			return e
		}
	}
	_, e = factSum(factList(p))
	return e
}

// ExportSnapshot owns an actual S01 reader transaction plus its exact pinned
// byte union. It intentionally supplies no archive creation or output methods.
type ExportSnapshot struct {
	mu       sync.Mutex
	registry *Registry
	reader   *S.Lease
	facts    []A.ByteFact
	identity A.Identity
	revision A.Number
	closed   bool
	closeErr error
}

// BeginExportSnapshot serializes snapshot+pin acquisition with the S01 writer.
// Lock order: writer slot, registry, reader checkout, BEGIN and
// first SELECT, publish pins; release registry/writer before caller reads.
func (r *Registry) BeginExportSnapshot(ctx context.Context) (*ExportSnapshot, error) {
	if r == nil || ctx == nil {
		return nil, errors.New("export snapshot unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, transactionLimit)
	defer cancel()
	w, e := r.store.Writer(ctx)
	if e != nil {
		return nil, e
	}
	defer w.Close()
	g, e := r.Guard(ctx)
	if e != nil {
		return nil, e
	}
	defer g.Close()
	l, e := r.store.Reader(ctx)
	if e != nil {
		return nil, e
	}
	x := &ExportSnapshot{registry: r, reader: l}
	e = l.Do(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "BEGIN"); e != nil {
			return e
		}
		a := readAccess{ctx, c}
		if e := a.Scan("SELECT board_id,incarnation,revision FROM board WHERE singleton=1", nil, &x.identity.BoardID, &x.identity.Incarnation, &x.revision); e != nil {
			return e
		}
		m, e := domainPins(a, nil)
		if e != nil {
			return e
		}
		x.facts = factList(m)
		// Existing exports and this snapshot obey the same protected cap.
		old, e := g.Facts()
		if e != nil {
			return e
		}
		for _, f := range old {
			if e = addFact(m, f); e != nil {
				return e
			}
		}
		_, e = factSum(factList(m))
		return e
	})
	if e != nil {
		l.Close()
		return nil, e
	}
	r.leases[x] = append([]A.ByteFact(nil), x.facts...)
	return x, nil
}
func (x *ExportSnapshot) Identity() (A.Identity, A.Number) { return x.identity, x.revision }
func (x *ExportSnapshot) Facts() []A.ByteFact              { return append([]A.ByteFact{}, x.facts...) }

// Read closes every Rows within fn; neither the facet nor connection may escape.
func (x *ExportSnapshot) Read(ctx context.Context, fn func(writer.Read) error) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed || ctx == nil || fn == nil {
		return errors.New("export snapshot closed")
	}
	ctx, cancel := context.WithTimeout(ctx, transactionLimit)
	defer cancel()
	return x.reader.Do(ctx, func(c *sql.Conn) error { return fn(readAccess{ctx, c}) })
}

// Close MUST release the SQLite reader BEFORE waiting for the pin registry.
// Recovery may hold the registry while COMMIT waits for this reader. Reversing
// these steps would deadlock. On reader cleanup failure retain pins permanently
// for this fenced Store rather than claiming bytes are safe to reclaim.
func (x *ExportSnapshot) Close() error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed {
		return x.closeErr
	}
	x.closed = true
	if e := x.reader.Close(); e != nil {
		x.closeErr = e
		return e
	}
	// After releasing the reader, take writer before registry. This also
	// reclaims the last export-only BLOB without a domain revision/event.
	ctx, cancel := context.WithTimeout(context.Background(), transactionLimit)
	defer cancel()
	w, e := x.registry.store.Writer(ctx)
	if e != nil {
		x.closeErr = e
		return e
	}
	g, e := x.registry.Guard(ctx)
	if e != nil {
		w.Close()
		x.closeErr = e
		return e
	}
	defer g.Close()
	delete(x.registry.leases, x)
	attempted := false
	e = w.Do(ctx, func(c *sql.Conn) error {
		pins, e := g.Facts()
		if e != nil {
			return e
		}
		// The writer slot and registry already stabilize references. Avoid an
		// unnecessary DELETE-mode COMMIT when other export readers remain and
		// there is no orphan byte row to remove.
		remove, e := orphanBytes(readAccess{ctx, c}, pins)
		if e != nil {
			return e
		}
		if len(remove) == 0 {
			return nil
		}
		if _, e := c.ExecContext(ctx, "BEGIN IMMEDIATE"); e != nil {
			return e
		}
		if e = collectOrphanBytes(writeAccess{readAccess{ctx, c}}, pins); e != nil {
			return e
		}
		attempted = true
		_, e = c.ExecContext(ctx, "COMMIT")
		return e
	})
	closeErr := w.Close()
	if e != nil || closeErr != nil {
		if attempted || closeErr != nil {
			x.registry.store.Fence()
		}
		x.registry.leases[x] = append([]A.ByteFact(nil), x.facts...)
		x.closeErr = errors.New("export pin cleanup unavailable")
		return x.closeErr
	}
	return nil
}

func addFact(m map[A.Digest]A.Number, f A.ByteFact) error {
	if e := A.Validate(f); e != nil {
		return e
	}
	if n, ok := m[f.SHA256]; ok && n != f.ByteLength {
		return errors.New("digest length disagreement")
	}
	m[f.SHA256] = f.ByteLength
	return nil
}
func factList(m map[A.Digest]A.Number) []A.ByteFact {
	a := make([]A.ByteFact, 0, len(m))
	for h, n := range m {
		a = append(a, A.ByteFact{SHA256: h, ByteLength: n})
	}
	sort.Slice(a, func(i, j int) bool { return a[i].SHA256 < a[j].SHA256 })
	return a
}
func factSum(a []A.ByteFact) (A.Number, error) {
	var n A.Number
	for _, f := range a {
		if f.ByteLength > 10485760 || n > 67108864-f.ByteLength {
			return 0, errors.New("protected byte quota exceeded")
		}
		n += f.ByteLength
	}
	return n, nil
}

// domainPins deliberately never reads byte_fact: compact and refused facts do
// not protect BLOBs. Selection excludes only its chosen history/operation pins.
func domainPins(r writer.Read, s *selection) (map[A.Digest]A.Number, error) {
	m := map[A.Digest]A.Number{}
	queries := []string{"SELECT sha256,byte_length,'','', '',0 FROM current_attachment_pin", "SELECT sha256,byte_length,origin_board,origin_incarnation,card_id,card_revision FROM history_blob_pin", "SELECT sha256,byte_length,origin_board,origin_incarnation,op_id,0 FROM operation_blob_pin"}
	for kind, q := range queries {
		rows, e := r.Query(q)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var f A.ByteFact
			var b, i, id string
			var rev uint64
			if e = rows.Scan(&f.SHA256, &f.ByteLength, &b, &i, &id, &rev); e != nil {
				rows.Close()
				return nil, e
			}
			if s != nil && (kind == 1 && s.hist[historyID(b, i, id, rev)] || kind == 2 && s.ops[outcomeID(b, i, id)]) {
				continue
			}
			if e = addFact(m, f); e != nil {
				rows.Close()
				return nil, e
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	return m, nil
}
