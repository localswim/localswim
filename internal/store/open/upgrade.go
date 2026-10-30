package open

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/localswim/localswim/internal/store/schema"
	"time"
)

var ErrUpgradeUnknown = errors.New("storage upgrade outcome unknown; inspect deliberately")

type StorageInspection struct {
	PhysicalVersion   int    `json:"physicalVersion"`
	BaseSchemaVersion int    `json:"baseSchemaVersion"`
	BoardID           string `json:"boardId"`
	Incarnation       string `json:"incarnation"`
	Valid             bool   `json:"valid"`
}

func validateSupported(ctx context.Context, c *sql.Conn, id schema.Identity) error {
	var version int
	if err := c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	switch version {
	case 5:
		return schema.ValidateV5(ctx, c, id)
	case 6:
		return schema.Validate(ctx, c, id)
	}
	return errors.New("unsupported physical layout")
}

// InspectStorage is existing-only and accepts exactly the frozen5 or current6 layout.
func InspectStorage(ctx context.Context, dir string, id schema.Identity) (out StorageInspection, err error) {
	out.BoardID = id.BoardID
	out.Incarnation = id.Incarnation
	err = inspectExisting(ctx, dir, id, func(r InspectionRead) error {
		return r.Scan("SELECT schema_version FROM board WHERE singleton=1", nil, &out.BaseSchemaVersion)
	}, func(ctx context.Context, c *sql.Conn, id schema.Identity) error {
		if err := validateSupported(ctx, c, id); err != nil {
			return err
		}
		return c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&out.PhysicalVersion)
	})
	out.Valid = err == nil
	return
}

// UpgradeStorage is a native offline operation, never an immutable domain intent.
// The caller also holds the initialized application's existing root exclusion.
func UpgradeStorage(ctx context.Context, dir string, id schema.Identity) (changed bool, err error) {
	return upgradeStorage(ctx, dir, id, nil)
}

// fault is a private deterministic test seam; a failure at committed means reply
// loss and is UNKNOWN even though COMMIT succeeded.
func upgradeStorage(ctx context.Context, dir string, id schema.Identity, fault func(string) error) (changed bool, err error) {
	inject := func(stage string) error {
		if fault != nil {
			return fault(stage)
		}
		return nil
	}
	commitAttempted := false
	defer func() {
		if commitAttempted && err != nil {
			err = errors.Join(ErrUpgradeUnknown, err)
		}
	}()
	if ctx == nil {
		return false, errors.New("storage context required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	g, err := acquireUpgradePath(dir)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, g.close()) }()
	db, err := pool(g.path, true)
	if err != nil {
		return false, err
	}
	c, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return false, err
	}
	if err = configure(ctx, c, true, false); err != nil {
		c.Close()
		db.Close()
		return false, err
	}
	if _, err = c.ExecContext(ctx, "BEGIN"); err == nil {
		err = validateSupported(ctx, c, id)
	}
	var version int
	if err == nil {
		err = c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	}
	var before [32]byte
	if err == nil {
		before, err = recordsDigest(ctx, c)
	}
	clean := errors.Join(rollback(c), c.Close(), db.Close())
	if err = errors.Join(err, clean); err != nil {
		return false, err
	}
	if version == 6 {
		return false, nil
	}
	db, err = pool(g.path, false)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	c, err = db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer func() {
		err = errors.Join(err, c.Close())
		if commitAttempted {
			err = errors.Join(err, inject("cleanup"))
		}
	}()
	if err = configure(ctx, c, false, false); err != nil {
		return false, err
	}
	if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return false, err
	}
	committed := false
	defer func() {
		if !committed {
			err = errors.Join(err, rollback(c))
		}
	}()
	if err = schema.ValidateV5(ctx, c, id); err != nil {
		return false, err
	}
	after, e := recordsDigest(ctx, c)
	if e != nil || before != after {
		return false, errors.New("storage changed during upgrade preflight")
	}

	for _, q := range schema.HierarchyStatements() {
		if _, err = c.ExecContext(ctx, q); err != nil {
			return false, err
		}
	}
	if err = inject("ddl"); err != nil {
		return false, err
	}
	if _, err = c.ExecContext(ctx, "PRAGMA user_version=6"); err != nil {
		return false, err
	}
	if err = inject("version"); err != nil {
		return false, err
	}
	if err = schema.Validate(ctx, c, id); err != nil {
		return false, err
	}
	if err = inject("validated"); err != nil {
		return false, err
	}
	commitAttempted = true
	if _, err = c.ExecContext(ctx, "COMMIT"); err != nil {
		return false, ErrUpgradeUnknown
	}
	committed = true
	if inject("committed") != nil {
		return false, ErrUpgradeUnknown
	}
	return true, nil
}

// Freeze every baseline table row and SQLite scalar/blob type. Layout checks
// freeze DDL separately; this proves the preflight snapshot was not replaced.
func recordsDigest(ctx context.Context, c *sql.Conn) ([32]byte, error) {
	var zero [32]byte
	rows, err := c.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type='table' AND name NOT GLOB 'sqlite_*' ORDER BY name")
	if err != nil {
		return zero, err
	}
	tables := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return zero, err
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return zero, err
	}
	h := sha256.New()
	for _, table := range tables {
		fmt.Fprintln(h, table)
		rows, err = c.QueryContext(ctx, `SELECT * FROM "`+table+`" ORDER BY rowid`)
		if err != nil {
			return zero, err
		}
		cols, e := rows.Columns()
		if e != nil {
			rows.Close()
			return zero, e
		}
		for rows.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				rows.Close()
				return zero, err
			}
			b, e := json.Marshal(values)
			if e != nil {
				rows.Close()
				return zero, e
			}
			h.Write(b)
			h.Write([]byte{'\n'})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return zero, err
		}
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}
