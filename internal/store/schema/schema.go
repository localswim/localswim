// Package schema defines the version-six physical storage layout. Admission, canonical
// wire validation, retention execution and domain transactions belong to callers.
package schema

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const Version = 6
const BaseSchemaVersion = 5
const ApplicationID = 0x4c535731 // LSW1
const MaxInteger int64 = 9007199254740991

// Identity is deliberately independent of the transport contract package.
type Identity struct{ BoardID, Incarnation string }

// ValidUUID accepts the frozen nonzero lowercase UUID shape, without imposing a
// UUID generation-version policy that the contract does not require.
func ValidUUID(v string) bool {
	if len(v) != 36 || v == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range []byte(v) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Statements returns fresh, deterministic DDL, including constraints and indexes.
// The small macros keep the same scalar checks on every identity and counter.
func Statements() []string {
	return append(StatementsV5(), HierarchyStatements()...)
}

func statementsFrom(ddl string) []string {
	parts := strings.Split(ddl, ";\n")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, expand(s))
	}
	out = append(out, guardsV5()...)
	return out
}

func expand(s string) string {
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], "}}")
		if j < 0 {
			panic("bad schema macro")
		}
		j += i
		p := strings.SplitN(s[i+2:j], ":", 2)
		c := p[1]
		var x string
		switch p[0] {
		case "l":
			// JSON quoting exposes NUL without truncation. Collapse escaped
			// backslashes first so literal escape text cannot become an escape.
			// The remaining JSON escapes each represent one Unicode scalar.
			x = "json_quote(" + c + ")"
			for _, escape := range []string{`\\`, `\"`, `\b`, `\f`, `\n`, `\r`, `\t`} {
				x = "replace(" + x + ",'" + escape + "','x')"
			}
			for scalar := 0; scalar < 32; scalar++ {
				x = "replace(" + x + ",'" + fmt.Sprintf(`\u%04x`, scalar) + "','x')"
			}
			x = "(length(" + x + ")-2)"
		case "u":
			x = "(length(" + c + ")=36 AND substr(" + c + ",9,1)='-' AND substr(" + c + ",14,1)='-' AND substr(" + c + ",19,1)='-' AND substr(" + c + ",24,1)='-' AND length(replace(" + c + ",'-',''))=32 AND replace(" + c + ",'-','') NOT GLOB '*[^0-9a-f]*' AND " + c + "<>'00000000-0000-0000-0000-000000000000')"
		case "v4":
			x = "(" + expand("{{u:"+c+"}}") + " AND length(CAST(" + c + " AS BLOB))=36 AND substr(" + c + ",15,1)='4' AND substr(" + c + ",20,1) IN ('8','9','a','b'))"
		case "n":
			x = "(typeof(" + c + ")='integer' AND " + c + " BETWEEN 0 AND 9007199254740991)"
		case "p":
			x = "(typeof(" + c + ")='integer' AND " + c + " BETWEEN 1 AND 9007199254740991)"
		case "h":
			x = "(length(" + c + ")=64 AND " + c + " NOT GLOB '*[^0-9a-f]*')"
		case "t":
			x = "(length(" + c + ")=20 AND " + c + ">='0001-01-01T00:00:00Z' AND " + c + "<='9999-12-31T23:59:59Z' AND strftime('%Y-%m-%dT%H:%M:%SZ'," + c + ",'+0 seconds') IS " + c + ")"
		default:
			panic("unknown schema macro")
		}
		s = s[:i] + x + s[j+2:]
	}
}

// Initialize must run inside the caller's exclusive, explicit SQL transaction on
// a newly created empty database. It never adopts an existing unversioned file.
func Initialize(ctx context.Context, c *sql.Conn, id Identity) error {
	if !ValidUUID(id.BoardID) || !ValidUUID(id.Incarnation) {
		return errors.New("invalid storage identity")
	}
	for _, s := range Statements() {
		if _, err := c.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("create storage schema: %w", err)
		}
	}
	if _, err := c.ExecContext(ctx, "INSERT INTO board(singleton,board_id,incarnation) VALUES(1,?,?)", id.BoardID, id.Incarnation); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, "INSERT INTO policy(incarnation) VALUES(?)", id.Incarnation); err != nil {
		return err
	}
	_, err := c.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=%d", ApplicationID, Version))
	return err
}

// Validate checks the exact layout as well as stored constraints. It never writes,
// repairs, migrates, deletes unknown objects, or changes journal mode.
func Validate(ctx context.Context, c *sql.Conn, id Identity) error {
	return validateLayout(ctx, c, id, Version, Statements(), consistencyChecks, true)
}

// ValidateV5 is the immutable accepted V2 baseline validator; no startup path calls it.
func ValidateV5(ctx context.Context, c *sql.Conn, id Identity) error {
	return validateLayout(ctx, c, id, 5, StatementsV5(), consistencyChecksV5, false)
}
func validateLayout(ctx context.Context, c *sql.Conn, id Identity, version int, statements []string, checks []string, forest bool) error {
	if !ValidUUID(id.BoardID) || !ValidUUID(id.Incarnation) {
		return errors.New("invalid storage identity")
	}
	for name, want := range map[string]int{"application_id": ApplicationID, "user_version": version} {
		var got int
		if err := c.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil || got != want {
			return fmt.Errorf("unsupported storage %s", name)
		}
	}
	// SQLite preserves these DDL statements in sqlite_schema, removing only the
	// terminating semicolon. Exact comparison refuses extra/missing/changed objects.
	want := make(map[string]bool)
	for _, s := range statements {
		want[s] = true
	}
	rows, err := c.QueryContext(ctx, "SELECT sql FROM sqlite_schema WHERE sql IS NOT NULL AND name NOT GLOB 'sqlite_*'")
	if err != nil {
		return err
	}
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		if !want[s] {
			rows.Close()
			return errors.New("storage layout mismatch")
		}
		delete(want, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(want) != 0 {
		return errors.New("incomplete storage layout")
	}
	var base int
	if err := c.QueryRowContext(ctx, "SELECT schema_version FROM board WHERE singleton=1").Scan(&base); err != nil || base != BaseSchemaVersion {
		return errors.New("storage base schema mismatch")
	}
	var b, i string
	if err = c.QueryRowContext(ctx, "SELECT board_id,incarnation FROM board WHERE singleton=1").Scan(&b, &i); err != nil || b != id.BoardID || i != id.Incarnation {
		return errors.New("storage identity mismatch")
	}
	var integrity string
	if err = c.QueryRowContext(ctx, "PRAGMA integrity_check(1)").Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("storage integrity check failed")
	}
	rows, err = c.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	bad := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if bad {
		return errors.New("storage foreign key check failed")
	}
	var n int
	if err = c.QueryRowContext(ctx, "SELECT count(*) FROM policy WHERE incarnation=? AND policy_version=1", i).Scan(&n); err != nil || n != 1 {
		return errors.New("storage policy missing")
	}
	for _, q := range checks {
		var bad int
		if err = c.QueryRowContext(ctx, q).Scan(&bad); err != nil || bad != 0 {
			return errors.New("storage relational consistency check failed")
		}
	}
	rows, err = c.QueryContext(ctx, "SELECT sha256,bytes FROM blob")
	if err != nil {
		return errors.New("storage bytes unavailable")
	}
	for rows.Next() {
		var digest string
		var bytes []byte
		if err = rows.Scan(&digest, &bytes); err != nil {
			rows.Close()
			return errors.New("storage bytes unavailable")
		}
		if fmt.Sprintf("%x", sha256.Sum256(bytes)) != digest {
			rows.Close()
			return errors.New("storage byte integrity check failed")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return errors.New("storage byte integrity check failed")
	}
	if forest {
		return validateHierarchy(ctx, c, id)
	}
	return nil
}

// Deferred whole-transaction completeness cannot be expressed as per-statement
// CHECKs while references are inserted in sequence. Open audits these joins;
// S02/S03 must preserve them at their transaction boundary. These checks do not
// substitute for canonical-intent, authorization, workflow or archive validation.
var consistencyChecks = []string{
	`SELECT count(*) FROM source_archive_group g JOIN board b USING(incarnation) WHERE g.origin_board<>b.board_id OR g.origin_incarnation=b.incarnation`,
	`SELECT count(*) FROM cli_credential c WHERE NOT EXISTS(SELECT 1 FROM board b WHERE b.board_id=c.board_id AND b.incarnation=c.incarnation) OR NOT EXISTS(SELECT 1 FROM grant_record g WHERE g.incarnation=c.incarnation AND g.grant_id=c.grant_id AND g.actor_id=c.actor_id)`,
	`SELECT count(*) FROM entity e WHERE (e.removed_at IS NULL) <> CASE e.kind WHEN 'card' THEN EXISTS(SELECT 1 FROM card x WHERE x.incarnation=e.incarnation AND x.card_id=e.entity_id) WHEN 'comment' THEN EXISTS(SELECT 1 FROM comment x WHERE x.incarnation=e.incarnation AND x.comment_id=e.entity_id) WHEN 'link' THEN EXISTS(SELECT 1 FROM link x WHERE x.incarnation=e.incarnation AND x.link_id=e.entity_id) WHEN 'attachment' THEN EXISTS(SELECT 1 FROM attachment x WHERE x.incarnation=e.incarnation AND x.attachment_id=e.entity_id) WHEN 'approval' THEN EXISTS(SELECT 1 FROM approval x WHERE x.incarnation=e.incarnation AND x.approval_id=e.entity_id) END`,
	`SELECT count(*) FROM card c JOIN board b USING(incarnation) WHERE c.revision>b.revision OR b.revision=0`,
	`SELECT count(*) FROM ticket t JOIN board b USING(incarnation) WHERE t.ticket>=b.next_ticket`,
	`SELECT count(*) FROM card_revision h JOIN card c USING(incarnation,card_id) WHERE h.card_revision>=c.revision`,
	`SELECT count(*) FROM current_reference r JOIN entity e ON e.incarnation=r.incarnation AND e.kind=r.kind AND e.entity_id=r.entity_id WHERE e.removed_at IS NOT NULL`,
	`SELECT count(*) FROM revision_reference r JOIN card_revision h USING(incarnation,origin_board,origin_incarnation,card_id,card_revision) JOIN entity e ON e.incarnation=r.incarnation AND e.kind=r.kind AND e.entity_id=r.entity_id WHERE e.card_id<>r.card_id OR (h.state IS NOT NULL AND e.removed_at IS NOT NULL)`,
	`SELECT count(*) FROM operation o JOIN board b USING(incarnation) WHERE (o.current_ledger=1)<>(o.origin_board=b.board_id AND o.origin_incarnation=b.incarnation) OR (o.removed_at IS NULL)<>EXISTS(SELECT 1 FROM operation_full f WHERE f.incarnation=o.incarnation AND f.origin_board=o.origin_board AND f.origin_incarnation=o.origin_incarnation AND f.op_id=o.op_id) OR (o.command='attachment.add')<>EXISTS(SELECT 1 FROM byte_fact f WHERE f.incarnation=o.incarnation AND f.origin_board=o.origin_board AND f.origin_incarnation=o.origin_incarnation AND f.op_id=o.op_id)`,
	`SELECT count(*) FROM operation_reference r JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.terminal_class<>'COMMITTED'`,
	`SELECT count(*) FROM operation_blob_pin p JOIN operation o USING(incarnation,origin_board,origin_incarnation,op_id) JOIN attachment a USING(incarnation,attachment_id) LEFT JOIN byte_fact f USING(incarnation,origin_board,origin_incarnation,op_id) WHERE o.terminal_class<>'COMMITTED' OR o.command<>'attachment.add' OR o.removed_at IS NOT NULL OR a.card_id<>o.target_id OR a.sha256<>p.sha256 OR a.byte_length<>p.byte_length OR f.sha256 IS NULL OR f.sha256<>p.sha256 OR f.byte_length<>p.byte_length`,
	`SELECT count(*) FROM operation o WHERE o.terminal_class='COMMITTED' AND o.command='attachment.add' AND o.removed_at IS NULL AND NOT EXISTS(SELECT 1 FROM operation_blob_pin p WHERE p.incarnation=o.incarnation AND p.origin_board=o.origin_board AND p.origin_incarnation=o.origin_incarnation AND p.op_id=o.op_id)`,
	`SELECT count(*) FROM current_reference r WHERE r.kind='attachment' AND NOT EXISTS(SELECT 1 FROM current_attachment_pin p WHERE p.incarnation=r.incarnation AND p.attachment_id=r.entity_id)`,
	`SELECT count(*) FROM current_attachment_pin p JOIN attachment a USING(incarnation,attachment_id) WHERE p.sha256<>a.sha256 OR p.byte_length<>a.byte_length OR NOT EXISTS(SELECT 1 FROM current_reference r WHERE r.incarnation=p.incarnation AND r.kind='attachment' AND r.entity_id=p.attachment_id)`,
	`SELECT count(*) FROM revision_reference r JOIN card_revision h USING(incarnation,origin_board,origin_incarnation,card_id,card_revision) WHERE r.kind='attachment' AND h.state IS NOT NULL AND NOT EXISTS(SELECT 1 FROM history_blob_pin p WHERE p.incarnation=r.incarnation AND p.origin_board=r.origin_board AND p.origin_incarnation=r.origin_incarnation AND p.card_id=r.card_id AND p.card_revision=r.card_revision AND p.attachment_id=r.entity_id)`,
	`SELECT count(*) FROM history_blob_pin p JOIN card_revision h USING(incarnation,origin_board,origin_incarnation,card_id,card_revision) JOIN attachment a USING(incarnation,attachment_id) WHERE h.state IS NULL OR a.card_id<>p.card_id OR a.sha256<>p.sha256 OR a.byte_length<>p.byte_length`,
	`SELECT count(*) FROM (SELECT incarnation,sha256 FROM (SELECT incarnation,sha256,byte_length FROM blob UNION ALL SELECT incarnation,sha256,byte_length FROM attachment UNION ALL SELECT incarnation,sha256,byte_length FROM byte_fact) GROUP BY incarnation,sha256 HAVING min(byte_length)<>max(byte_length))`,
	`SELECT CASE WHEN coalesce(sum(byte_length),0)>67108864 THEN 1 ELSE 0 END FROM blob b WHERE EXISTS(SELECT 1 FROM current_attachment_pin p WHERE p.incarnation=b.incarnation AND p.sha256=b.sha256) OR EXISTS(SELECT 1 FROM history_blob_pin p WHERE p.incarnation=b.incarnation AND p.sha256=b.sha256) OR EXISTS(SELECT 1 FROM operation_blob_pin p WHERE p.incarnation=b.incarnation AND p.sha256=b.sha256)`,
}
