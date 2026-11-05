package open

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/schema"
	"strings"
	"time"
)

var ErrUpgradeUnknown = errors.New("storage upgrade outcome unknown; inspect deliberately")

type StorageInspection struct {
	PhysicalVersion   int         `json:"physicalVersion"`
	BaseSchemaVersion int         `json:"baseSchemaVersion"`
	BoardID           string      `json:"boardId"`
	Incarnation       string      `json:"incarnation"`
	Valid             bool        `json:"valid"`
	Workflow          *A.Workflow `json:"workflow,omitempty"`
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
		return schema.ValidateV7(ctx, c, id)
	case 8:
		return schema.ValidateV8(ctx, c, id)
	case 9:
		return schema.Validate(ctx, c, id)
	}
	return errors.New("unsupported physical layout")
}

// InspectStorage is existing-only and accepts exactly the independently frozen5/6/7/8 or current9 layout.
func InspectStorage(ctx context.Context, dir string, id schema.Identity) (out StorageInspection, err error) {
	out.BoardID = id.BoardID
	out.Incarnation = id.Incarnation
	err = inspectExisting(ctx, dir, id, func(r InspectionRead) error {
		return r.Scan("SELECT schema_version FROM board WHERE singleton=1", nil, &out.BaseSchemaVersion)
	}, func(ctx context.Context, c *sql.Conn, id schema.Identity) error {
		if err := validateSupported(ctx, c, id); err != nil {
			return err
		}
		if err := c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&out.PhysicalVersion); err != nil {
			return err
		}
		if out.PhysicalVersion >= 8 {
			var err error
			if out.PhysicalVersion == 8 {
				out.Workflow, err = schema.ReadWorkflowV8(ctx, c, id)
			} else {
				out.Workflow, err = schema.ReadWorkflow(ctx, c, id)
			}
			return err
		}
		return nil
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
func supportedStep(from, to int) bool {
	return from == 5 && to == 6 || from == 6 && to == 7 || from == 7 && to == 8 || from == 8 && to == 9
}

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
	} else if from == 6 {
		if err = rebuildLinks(ctx, c, inject); err != nil {
			return false, err
		}
	} else if from == 7 {
		if err = rebuildWorkflow(ctx, c, inject); err != nil {
			return false, err
		}
	} else if err = rebuildPolicies(ctx, c, inject); err != nil {
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
	conserved, e := recordsDigestUpgrade(ctx, c, from == 5, from == 7, from == 8)
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
	return recordsDigestExcluding(ctx, c, emptyHierarchy, false)
}
func recordsDigestExcluding(ctx context.Context, c *sql.Conn, emptyHierarchy, emptyWorkflow bool) ([32]byte, error) {
	return recordsDigestUpgrade(ctx, c, emptyHierarchy, emptyWorkflow, false)
}
func recordsDigestUpgrade(ctx context.Context, c *sql.Conn, emptyHierarchy, emptyWorkflow, policyUpgrade bool) ([32]byte, error) {
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
		if emptyHierarchy && table == "card_parent" || emptyWorkflow && table == "workflow_definition" {
			var count int
			if err := c.QueryRowContext(ctx, `SELECT count(*) FROM "`+table+`"`).Scan(&count); err != nil || count != 0 {
				return zero, errors.New("new extension table is not empty")
			}
			continue
		}
		if policyUpgrade && table == "workflow_policy" {
			continue
		}
		fmt.Fprintln(h, table)
		query := `SELECT rowid,* FROM "` + table + `" ORDER BY rowid`
		if policyUpgrade && table == "card" {
			query = `SELECT rowid,incarnation,card_id,kind,revision,title,description,priority,lane,archived,current_approval_id,owner_id,approval_kind FROM card ORDER BY rowid`
		}
		if policyUpgrade && table == "card_revision" {
			query = `SELECT rowid,incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state,removed_at,removal_reason,removal_revision FROM card_revision ORDER BY rowid`
		}
		rows, err = c.QueryContext(ctx, query)
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

// Rebuild only card for the widened scalar lane shape. Foreign keys remain ON;
// deferral applies within this one transaction, with a full FK audit before
// COMMIT. The stable name is restored before any committed state can be seen.
func rebuildWorkflow(ctx context.Context, c *sql.Conn, inject func(string) error) error {
	for _, q := range []string{
		"PRAGMA defer_foreign_keys=ON",
		"CREATE TEMP TABLE upgrade_card AS SELECT rowid AS original_rowid,* FROM card",
		"DROP TABLE card",
	} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if err := inject("card-dropped"); err != nil {
		return err
	}
	for _, q := range schema.StatementsV8() {
		if !strings.HasPrefix(q, "CREATE TABLE card (") && !(strings.HasPrefix(q, "CREATE TRIGGER ") && strings.Contains(q, " ON card BEGIN ")) {
			continue
		}
		if _, err := c.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if _, err := c.ExecContext(ctx, `INSERT INTO card(rowid,incarnation,card_id,kind,revision,title,description,priority,lane,archived,current_approval_id,owner_id,approval_kind) SELECT original_rowid,incarnation,card_id,kind,revision,title,description,priority,lane,archived,current_approval_id,owner_id,approval_kind FROM upgrade_card ORDER BY original_rowid`); err != nil {
		return err
	}
	if err := inject("card-copied"); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, "DROP TABLE temp.upgrade_card"); err != nil {
		return err
	}
	for _, q := range schema.WorkflowStatements() {
		if _, err := c.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// Rebuild the seven changed tables under the same exclusive transaction. All
// old columns, SQLite types, rowids and payload bytes are copied unchanged.
func rebuildPolicies(ctx context.Context, c *sql.Conn, inject func(string) error) error {
	tables := []string{"policy", "card", "approval", "operation", "operation_reference", "activity", "card_revision"}
	if _, err := c.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); err != nil {
		return err
	}
	columns := map[string]string{}
	for _, table := range tables {
		rows, err := c.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
		if err != nil {
			return err
		}
		var names []string
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var def any
			if err = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
				rows.Close()
				return err
			}
			names = append(names, `"`+name+`"`)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		columns[table] = strings.Join(names, ",")
		if _, err = c.ExecContext(ctx, `CREATE TEMP TABLE "upgrade_`+table+`" AS SELECT rowid AS original_rowid,`+columns[table]+` FROM "`+table+`"`); err != nil {
			return err
		}
	}
	for n := len(tables) - 1; n >= 0; n-- {
		table := tables[n]
		if _, err := c.ExecContext(ctx, `DROP TABLE "`+table+`"`); err != nil {
			return err
		}
		if err := inject(table + "-dropped"); err != nil {
			return err
		}
	}
	statements := schema.Statements()
	for _, q := range statements {
		for _, table := range tables {
			if strings.HasPrefix(q, "CREATE TABLE "+table+" (") {
				if _, err := c.ExecContext(ctx, q); err != nil {
					return err
				}
			}
		}
	}
	for _, table := range tables {
		if _, err := c.ExecContext(ctx, `INSERT INTO "`+table+`"(rowid,`+columns[table]+`) SELECT original_rowid,`+columns[table]+` FROM "upgrade_`+table+`" ORDER BY original_rowid`); err != nil {
			return err
		}
		if err := inject(table + "-copied"); err != nil {
			return err
		}
	}
	for _, q := range statements {
		if strings.HasPrefix(q, "CREATE TABLE ") {
			continue
		}
		for _, table := range tables {
			if strings.Contains(q, " ON "+table+" ") || strings.Contains(q, " ON "+table+"(") {
				if _, err := c.ExecContext(ctx, q); err != nil {
					return err
				}
				break
			}
		}
	}
	for _, q := range schema.WorkflowPolicyStatements() {
		if _, err := c.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if _, err := c.ExecContext(ctx, `INSERT INTO workflow_policy(incarnation,policy_version,workflow_id,definition) SELECT p.incarnation,1,w.workflow_id,w.definition FROM policy p LEFT JOIN workflow_definition w USING(incarnation)`); err != nil {
		return err
	}
	for _, table := range tables {
		if _, err := c.ExecContext(ctx, `DROP TABLE temp."upgrade_`+table+`"`); err != nil {
			return err
		}
	}
	return nil
}
