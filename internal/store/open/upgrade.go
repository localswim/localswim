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
		return schema.ValidateV6(ctx, c, id)
	case 7:
		return schema.Validate(ctx, c, id)
	}
	return errors.New("unsupported physical layout")
}

// InspectStorage is existing-only and accepts exactly the independently frozen5/6 or current7 layout.
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

// UpgradeStorageTo performs only the explicitly selected supported adjacent step.
// An already-valid destination is NO_CHANGE; every other source refuses.
func UpgradeStorageTo(ctx context.Context, dir string, id schema.Identity, from, to int) (bool, error) {
	if !supportedStep(from, to) {
		return false, errors.New("unsupported storage upgrade step")
	}
	return upgradeStorageStep(ctx, dir, id, from, to, nil)
}
func supportedStep(from, to int) bool { return from == 5 && to == 6 || from == 6 && to == 7 }

// fault is a private deterministic test seam; a failure at committed means reply
// loss and is UNKNOWN even though COMMIT succeeded.
func upgradeStorage(ctx context.Context, dir string, id schema.Identity, fault func(string) error) (changed bool, err error) {
	return upgradeStorageStep(ctx, dir, id, 0, 0, fault)
}
func upgradeStorageStep(ctx context.Context, dir string, id schema.Identity, from, to int, fault func(string) error) (changed bool, err error) {
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
	if from == 0 {
		if version == schema.Version {
			return false, nil
		}
		from, to = version, version+1
	}
	if version == to {
		return false, nil
	}
	if version != from || !supportedStep(from, to) {
		return false, errors.New("storage upgrade source mismatch")
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
	if err = validateSupported(ctx, c, id); err != nil {
		return false, err
	}
	var lockedVersion int
	if err = c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&lockedVersion); err != nil || lockedVersion != from {
		return false, errors.New("storage changed during upgrade preflight")
	}
	after, e := recordsDigest(ctx, c)
	if e != nil || before != after {
		return false, errors.New("storage changed during upgrade preflight")
	}

	if from == 5 {
		for _, q := range schema.HierarchyStatements() {
			if _, err = c.ExecContext(ctx, q); err != nil {
				return false, err
			}
		}
	} else if err = rebuildLinks(ctx, c, inject); err != nil {
		return false, err
	}
	if err = inject("ddl"); err != nil {
		return false, err
	}
	if _, err = c.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", to)); err != nil {
		return false, err
	}
	if err = inject("version"); err != nil {
		return false, err
	}
	if err = validateSupported(ctx, c, id); err != nil {
		return false, err
	}
	// Recheck all conserved typed rows after candidate validation. The only added
	// table in5->6 is empty;6->7 preserves the entire row stream including rowids.
	conserved, e := recordsDigestExcept(ctx, c, from == 5)
	if e != nil || conserved != before {
		return false, errors.New("storage upgrade changed retained records")
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

// Rebuild only link. No persistent helper, foreign-key weakening, row filtering,
// ID reassignment or link update is used. Preserve rowid as well as every column
// so the complete typed-row digest remains stable across the rebuild.
func rebuildLinks(ctx context.Context, c *sql.Conn, inject func(string) error) error {
	for _, q := range []string{
		"CREATE TEMP TABLE upgrade_link AS SELECT rowid AS original_rowid,* FROM link",
		"DROP TABLE link",
	} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if err := inject("link-dropped"); err != nil {
		return err
	}
	for _, q := range schema.LinkStatements() {
		if _, err := c.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if _, err := c.ExecContext(ctx, "INSERT INTO link(rowid,incarnation,link_id,entity_kind,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at) SELECT original_rowid,incarnation,link_id,entity_kind,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at FROM upgrade_link ORDER BY original_rowid"); err != nil {
		return err
	}
	if err := inject("link-copied"); err != nil {
		return err
	}
	_, err := c.ExecContext(ctx, "DROP TABLE temp.upgrade_link")
	return err
}

// Freeze every baseline table row and SQLite scalar/blob type. Layout checks
// freeze DDL separately; this proves the preflight snapshot was not replaced.
func recordsDigest(ctx context.Context, c *sql.Conn) ([32]byte, error) {
	return recordsDigestExcept(ctx, c, false)
}
func recordsDigestExcept(ctx context.Context, c *sql.Conn, emptyHierarchy bool) ([32]byte, error) {
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
		if emptyHierarchy && table == "card_parent" {
			var count int
			if err := c.QueryRowContext(ctx, "SELECT count(*) FROM card_parent").Scan(&count); err != nil || count != 0 {
				return zero, errors.New("new hierarchy table is not empty")
			}
			continue
		}
		fmt.Fprintln(h, table)
		rows, err = c.QueryContext(ctx, `SELECT rowid,* FROM "`+table+`" ORDER BY rowid`)
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
			typed := make([]struct {
				Type  string
				Value any
			}, len(values))
			for i, value := range values {
				typed[i].Type = fmt.Sprintf("%T", value)
				typed[i].Value = value
			}
			b, e := json.Marshal(typed)
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
