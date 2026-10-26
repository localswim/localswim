//go:build windows

// Command driver is a synthetic, isolated Q02 qualification harness.
// It is not a product store, recovery implementation, or durability test.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"modernc.org/sqlite"
)

const sourceID = "2026-10-16 14:45:20 555e07ef33f69a0957d24d8e555b702464a131d6f1607f4c3c1e4551e8e6ca76"
const fixture = "q02-synthetic-parent-child-counter-blob-r1"

type connection struct {
	Role            string `json:"role"`
	ForeignKeys     int    `json:"foreign_keys"`
	JournalMode     string `json:"journal_mode"`
	Synchronous     int    `json:"synchronous"`
	ReadUncommitted int    `json:"read_uncommitted"`
	BusyTimeout     int    `json:"busy_timeout_ms"`
	LockingMode     string `json:"locking_mode"`
	FKRejected      bool   `json:"invalid_fk_rejected"`
}
type report struct {
	Status             string            `json:"status"`
	Fixture            string            `json:"fixture"`
	GoVersion          string            `json:"go_version"`
	Platform           string            `json:"platform"`
	Filesystem         string            `json:"filesystem"`
	Modules            map[string]string `json:"modules"`
	BuildSettings      map[string]string `json:"build_settings"`
	SQLiteVersion      string            `json:"sqlite_version"`
	SourceID           string            `json:"sqlite_source_id"`
	CompileOptions     []string          `json:"compile_options"`
	Connections        []connection      `json:"connections"`
	Assertions         []string          `json:"assertions"`
	FinalFixtureSHA256 string            `json:"final_synthetic_database_sha256"`
}

func main() {
	root := flag.String("runtime-root", "", "required disposable private directory outside the repository")
	repeats := flag.Int("repeats", 3, "bounded repetitions, 1 through 10")
	worker := flag.Bool("worker", false, "internal isolated worker")
	flag.Parse()
	if *root == "" || *repeats < 1 || *repeats > 10 {
		fatal("runtime-root required; repeats must be 1..10")
	}
	if *worker {
		r, err := qualify(*root)
		if err != nil {
			fatal(err.Error())
		}
		emit(r)
		return
	}
	// Each child owns a fresh directory; only that child is terminated on timeout.
	// Raw subprocess error text stays on stderr, never inside sanitized evidence.
	if err := os.MkdirAll(*root, 0700); err != nil {
		fatal("create private runtime root")
	}
	exe, err := os.Executable()
	if err != nil {
		fatal("locate executable")
	}
	var results []report
	for i := 0; i < *repeats; i++ {
		dir, err := os.MkdirTemp(*root, "synthetic-")
		if err != nil {
			fatal("create private fixture directory")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		cmd := exec.CommandContext(ctx, exe, "-worker", "-runtime-root", dir)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, runErr := cmd.Output()
		cancel()
		if runErr != nil {
			fmt.Fprint(os.Stderr, stderr.String())
			fatal(fmt.Sprintf("isolated repeat %d failed or exceeded 25-second bound", i+1))
		}
		var r report
		if err := json.Unmarshal(out, &r); err != nil || r.Status != "PASS_ISOLATED_SCOPE" {
			fatal("invalid worker evidence")
		}
		results = append(results, r)
	}
	emit(struct {
		Scope                 string   `json:"scope"`
		Repetitions           int      `json:"repetitions"`
		WorkerDeadlineSeconds int      `json:"worker_deadline_seconds"`
		Results               []report `json:"results"`
	}{"Q02 isolated binding/connection behavior only; no product recovery or power-loss qualification", *repeats, 25, results})
}

func fatal(s string) { fmt.Fprintln(os.Stderr, s); os.Exit(1) }
func emit(v any) {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	if err := e.Encode(v); err != nil {
		fatal("encode evidence")
	}
}

func qualify(dir string) (r report, err error) {
	r = report{Status: "FAIL", Fixture: fixture, GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH, Modules: map[string]string{}, BuildSettings: map[string]string{}}
	if r.GoVersion != "go1.27.3" || r.Platform != "windows/amd64" {
		return r, errors.New("unexpected toolchain/platform")
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return r, errors.New("missing build info")
	}
	for _, d := range bi.Deps {
		if d.Path == "modernc.org/sqlite" || d.Path == "modernc.org/libc" {
			if d.Replace != nil {
				return r, errors.New("replaced dependency")
			}
			r.Modules[d.Path] = d.Version
		}
	}
	for _, s := range bi.Settings {
		if s.Key == "CGO_ENABLED" || s.Key == "GOOS" || s.Key == "GOARCH" {
			r.BuildSettings[s.Key] = s.Value
		}
	}
	if r.Modules["modernc.org/sqlite"] != "v1.61.0" || r.Modules["modernc.org/libc"] != "v1.79.0" || r.BuildSettings["CGO_ENABLED"] != "0" {
		return r, errors.New("frozen build/module pins mismatch")
	}
	r.Filesystem, err = filesystem(dir)
	if err != nil || r.Filesystem != "NTFS" {
		return r, errors.New("native NTFS fixture required")
	}
	dsn := filepath.Join(dir, "synthetic.db") + "?" + url.Values{"_pragma": {"foreign_keys(ON)", "journal_mode(DELETE)", "synchronous(FULL)", "read_uncommitted(OFF)", "busy_timeout(150)"}}.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return r, err
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	ctx := context.Background() // External process watchdog bounds calls without concurrent Raw/interrupt use.
	var cs []*sql.Conn
	for i := 0; i < 4; i++ {
		c, e := db.Conn(ctx)
		if e != nil {
			return r, e
		}
		cs = append(cs, c)
		defer c.Close()
	}
	a, b, reader, observer := cs[0], cs[1], cs[2], cs[3]
	if err = a.QueryRowContext(ctx, "SELECT sqlite_version(), sqlite_source_id()").Scan(&r.SQLiteVersion, &r.SourceID); err != nil {
		return r, err
	}
	if r.SQLiteVersion != "3.54.1" || r.SourceID != sourceID {
		return r, errors.New("SQLite version/source identity mismatch")
	}
	rows, err := a.QueryContext(ctx, "PRAGMA compile_options")
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			rows.Close()
			return r, err
		}
		r.CompileOptions = append(r.CompileOptions, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return r, err
	}
	sort.Strings(r.CompileOptions)
	for _, required := range []string{"THREADSAFE=1", "MUTEX_NOOP"} {
		if !contains(r.CompileOptions, required) {
			return r, fmt.Errorf("required observed option %s missing", required)
		}
	}
	// Deliberately synthetic schema; no product/domain tables or migrations.
	for _, q := range []string{"CREATE TABLE p(id INTEGER PRIMARY KEY)", "CREATE TABLE c(id INTEGER PRIMARY KEY, parent INTEGER NOT NULL REFERENCES p(id))", "CREATE TABLE s(id INTEGER PRIMARY KEY, n INTEGER NOT NULL, payload BLOB NOT NULL)", "INSERT INTO p VALUES(1)", "INSERT INTO s VALUES(1,0,x'0001ff'),(2,0,x'')"} {
		if err = execSQL(a, q); err != nil {
			return r, err
		}
	}
	roles := []string{"writer_a", "writer_b", "snapshot_reader", "observer"}
	for i, c := range cs {
		v := connection{Role: roles[i]}
		for _, p := range []struct {
			key  string
			dest any
		}{{"foreign_keys", &v.ForeignKeys}, {"journal_mode", &v.JournalMode}, {"synchronous", &v.Synchronous}, {"read_uncommitted", &v.ReadUncommitted}, {"busy_timeout", &v.BusyTimeout}, {"locking_mode", &v.LockingMode}} {
			if err = c.QueryRowContext(ctx, "PRAGMA "+p.key).Scan(p.dest); err != nil {
				return r, err
			}
		}
		if v.ForeignKeys != 1 || v.JournalMode != "delete" || v.Synchronous != 2 || v.ReadUncommitted != 0 || v.BusyTimeout != 150 || v.LockingMode != "normal" {
			return r, errors.New("connection settings mismatch")
		}
		_, e := c.ExecContext(ctx, "INSERT INTO c VALUES(99,999)")
		var se *sqlite.Error
		if !errors.As(e, &se) || se.Code() != 787 {
			return r, errors.New("expected SQLITE_CONSTRAINT_FOREIGNKEY on every connection")
		}
		v.FKRejected = true
		r.Connections = append(r.Connections, v)
	}
	r.Assertions = append(r.Assertions, "all_four_physical_connections_fk_delete_full_clean_reads_and_fk_enforcement")
	// Separate owners execute simultaneous calls on distinct physical connections.
	// SQLite owns writer exclusion; no application-wide mutex hides contention.
	if err = execSQL(a, "BEGIN IMMEDIATE"); err != nil {
		return r, err
	}
	if err = execSQL(a, "UPDATE s SET n=10 WHERE id=1"); err != nil {
		return r, err
	}
	busy := make(chan error, 1)
	go func() { busy <- execSQL(b, "BEGIN IMMEDIATE") }()
	if err = number(observer, 0); err != nil {
		return r, err
	}
	if err = expectBusy(<-busy); err != nil {
		return r, err
	}
	if err = execSQL(a, "ROLLBACK"); err != nil {
		return r, err
	}
	if err = number(observer, 0); err != nil {
		return r, err
	}
	if err = execSQL(b, "BEGIN IMMEDIATE"); err != nil {
		return r, err
	}
	if err = execSQL(b, "UPDATE s SET n=1 WHERE id=1"); err != nil {
		return r, err
	}
	if err = execSQL(b, "COMMIT"); err != nil {
		return r, err
	}
	if err = number(observer, 1); err != nil {
		return r, err
	}
	r.Assertions = append(r.Assertions, "writer_b_busy_while_writer_a_reserved_then_rollback_has_no_visible_effect_and_b_retry_commits")
	// Pin one read transaction before the writer modifies scalar and binary data.
	if err = execSQL(reader, "BEGIN"); err != nil {
		return r, err
	}
	if err = snapshot(reader, 1, []byte{0, 1, 255}); err != nil {
		return r, err
	}
	if err = execSQL(a, "BEGIN IMMEDIATE"); err != nil {
		return r, err
	}
	if err = execSQL(a, "UPDATE s SET n=2,payload=x'ff0002' WHERE id=1"); err != nil {
		return r, err
	}
	if err = snapshot(reader, 1, []byte{0, 1, 255}); err != nil {
		return r, err
	}
	// DELETE rollback mode permits reserved writer+reader, but the pinned reader
	// prevents writer COMMIT. COMMIT busy leaves the transaction active.
	commit := make(chan error, 1)
	go func() { commit <- execSQL(a, "COMMIT") }()
	if err = snapshot(reader, 1, []byte{0, 1, 255}); err != nil {
		return r, err
	}
	if err = expectBusy(<-commit); err != nil {
		return r, err
	}
	if err = snapshot(reader, 1, []byte{0, 1, 255}); err != nil {
		return r, err
	}
	if err = execSQL(reader, "ROLLBACK"); err != nil {
		return r, err
	}
	if err = execSQL(a, "COMMIT"); err != nil {
		return r, err
	}
	if err = snapshot(reader, 2, []byte{255, 0, 2}); err != nil {
		return r, err
	}
	r.Assertions = append(r.Assertions, "pinned_separate_reader_retains_scalar_binary_empty_blob_inventory_writer_commit_busy_until_reader_release_then_commit_visible")
	// A second blocked COMMIT is explicitly rolled back instead of retried.
	// Prove both cleanup and reuse of the same physical writer and reader.
	if err = execSQL(reader, "BEGIN"); err != nil {
		return r, err
	}
	if err = snapshot(reader, 2, []byte{255, 0, 2}); err != nil {
		return r, err
	}
	if err = execSQL(a, "BEGIN IMMEDIATE"); err != nil {
		return r, err
	}
	if err = execSQL(a, "UPDATE s SET n=99,payload=x'99' WHERE id=1"); err != nil {
		return r, err
	}
	go func() { commit <- execSQL(a, "COMMIT") }()
	if err = expectBusy(<-commit); err != nil {
		return r, err
	}
	if err = execSQL(a, "ROLLBACK"); err != nil {
		return r, err
	}
	if err = snapshot(reader, 2, []byte{255, 0, 2}); err != nil {
		return r, err
	}
	if err = execSQL(reader, "ROLLBACK"); err != nil {
		return r, err
	}
	if err = snapshot(observer, 2, []byte{255, 0, 2}); err != nil {
		return r, err
	}
	r.Assertions = append(r.Assertions, "busy_commit_explicit_rollback_preserves_previous_bytes_and_same_physical_connections_are_reused")
	// Bounded repeated competing writers with explicit per-transaction ownership.
	// A controls admission, B must fail while A is reserved, then writes after release.
	for i := 0; i < 6; i++ {
		if err = execSQL(a, "BEGIN IMMEDIATE"); err != nil {
			return r, err
		}
		if err = execSQL(a, "UPDATE s SET n=n+1 WHERE id=1"); err != nil {
			return r, err
		}
		go func() { busy <- execSQL(b, "BEGIN IMMEDIATE") }()
		if err = expectBusy(<-busy); err != nil {
			return r, err
		}
		if err = execSQL(a, "COMMIT"); err != nil {
			return r, err
		}
		if err = execSQL(b, "BEGIN IMMEDIATE"); err != nil {
			return r, err
		}
		if err = execSQL(b, "UPDATE s SET n=n+1 WHERE id=1"); err != nil {
			return r, err
		}
		if err = execSQL(b, "COMMIT"); err != nil {
			return r, err
		}
	}
	if err = number(observer, 14); err != nil {
		return r, err
	}
	r.Assertions = append(r.Assertions, "six_competing_writer_rounds_have_busy_exclusion_retry_and_exact_final_count_no_lost_updates")
	var check string
	if err = observer.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		return r, errors.New("integrity_check not ok")
	}
	fk, err := observer.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return r, err
	}
	bad := fk.Next()
	e := fk.Err()
	fk.Close()
	if bad || e != nil {
		return r, errors.New("foreign_key_check not empty")
	}
	if _, e = os.Stat(filepath.Join(dir, "synthetic.db-journal")); !errors.Is(e, os.ErrNotExist) {
		return r, errors.New("DELETE journal remained after completed transactions")
	}
	r.Assertions = append(r.Assertions, "integrity_check_ok_foreign_key_check_empty_and_delete_journal_absent_after_clean_commit")
	// Close/reopen through a new database/sql pool, then validate fresh settings
	// and persisted data. No abnormal termination or recovery claim is involved.
	for _, c := range cs {
		if err = c.Close(); err != nil {
			return r, err
		}
	}
	if err = db.Close(); err != nil {
		return r, err
	}
	db2, err := sql.Open("sqlite", dsn)
	if err != nil {
		return r, err
	}
	defer db2.Close()
	db2.SetMaxOpenConns(1)
	c, err := db2.Conn(ctx)
	if err != nil {
		return r, err
	}
	defer c.Close()
	if err = snapshot(c, 14, []byte{255, 0, 2}); err != nil {
		return r, err
	}
	v := connection{Role: "clean_reopened_connection"}
	for _, p := range []struct {
		key  string
		dest any
	}{{"foreign_keys", &v.ForeignKeys}, {"journal_mode", &v.JournalMode}, {"synchronous", &v.Synchronous}, {"read_uncommitted", &v.ReadUncommitted}, {"busy_timeout", &v.BusyTimeout}, {"locking_mode", &v.LockingMode}} {
		if err = c.QueryRowContext(ctx, "PRAGMA "+p.key).Scan(p.dest); err != nil {
			return r, err
		}
	}
	if v.ForeignKeys != 1 || v.JournalMode != "delete" || v.Synchronous != 2 || v.ReadUncommitted != 0 || v.BusyTimeout != 150 || v.LockingMode != "normal" {
		return r, errors.New("reopen settings mismatch")
	}
	_, e = c.ExecContext(ctx, "INSERT INTO c VALUES(99,999)")
	var se *sqlite.Error
	if !errors.As(e, &se) || se.Code() != 787 {
		return r, errors.New("reopen FK enforcement mismatch")
	}
	v.FKRejected = true
	r.Connections = append(r.Connections, v)
	r.Assertions = append(r.Assertions, "clean_reopen_new_connection_retains_committed_data_and_fk_delete_full_settings")
	if err = c.Close(); err != nil {
		return r, err
	}
	if err = db2.Close(); err != nil {
		return r, err
	}
	fixtureBytes, err := os.ReadFile(filepath.Join(dir, "synthetic.db"))
	if err != nil {
		return r, err
	}
	r.FinalFixtureSHA256 = fmt.Sprintf("%x", sha256.Sum256(fixtureBytes))
	r.Status = "PASS_ISOLATED_SCOPE"
	return r, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
func execSQL(c *sql.Conn, q string) error {
	_, err := c.ExecContext(context.Background(), q)
	return err
}
func expectBusy(err error) error {
	var e *sqlite.Error
	if !errors.As(err, &e) || e.Code()&255 != 5 {
		return fmt.Errorf("expected SQLITE_BUSY, got %v", err)
	}
	return nil
}
func number(c *sql.Conn, want int) error {
	var n int
	if err := c.QueryRowContext(context.Background(), "SELECT n FROM s WHERE id=1").Scan(&n); err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("counter mismatch got %d want %d", n, want)
	}
	return nil
}
func snapshot(c *sql.Conn, want int, payload []byte) error {
	if err := number(c, want); err != nil {
		return err
	}
	rows, err := c.QueryContext(context.Background(), "SELECT id,payload FROM s ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int
		var b []byte
		if err := rows.Scan(&id, &b); err != nil {
			return err
		}
		count++
		if (id == 1 && !bytes.Equal(b, payload)) || (id == 2 && len(b) != 0) || (id != 1 && id != 2) {
			return errors.New("binary inventory mismatch")
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != 2 {
		return errors.New("inventory count mismatch")
	}
	return nil
}

func filesystem(dir string) (string, error) {
	full, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	p, err := syscall.UTF16PtrFromString(full)
	if err != nil {
		return "", err
	}
	k := syscall.NewLazyDLL("kernel32.dll")
	volume := make([]uint16, 261)
	fs := make([]uint16, 32)
	r, _, _ := k.NewProc("GetVolumePathNameW").Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&volume[0])), uintptr(len(volume)))
	if r == 0 {
		return "", errors.New("volume path query failed")
	}
	r, _, _ = k.NewProc("GetVolumeInformationW").Call(uintptr(unsafe.Pointer(&volume[0])), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&fs[0])), uintptr(len(fs)))
	if r == 0 {
		return "", errors.New("filesystem query failed")
	}
	return strings.ToUpper(syscall.UTF16ToString(fs)), nil
}
