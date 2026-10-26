# Storage ownership boundary

This package is the version-three physical schema/open boundary. It uses the Q02 qualified
`database/sql` binding on native Windows/amd64 and a fixed local NTFS drive.
It does not implement domain admission, permissions, outcome reconciliation,
history clearing, restore, HTTP or CLI behavior.

`Create(ctx, directory, identity)` requires an existing empty private directory;
`Existing` requires the same directory and exact expected board/incarnation.
The fixed files are `board.sqlite` and `board.lock`. A lock file is an OS-handle
exclusion object: its mere presence is harmless and never establishes ownership.
There is no PID/stale-sentinel takeover protocol. No unknown file, live journal,
WAL sidecar, unsupported header/version/layout, corrupt constraints or broken
stored relational reference is silently adopted or repaired. Failed creation
leaves its synthetic or real partial files in place for separately scoped recovery.
Neither API deletes a file, migrates schema, overwrites a DB, or switches an
existing database to DELETE mode.

The unreleased physical layout is 3 in the schema inventory, board metadata and
SQLite user version. Layouts 1 and 2 are refused without modifying database bytes; there
is no migration. API, logical archive and fingerprint versions remain 1. Private
CLI credential hash/public metadata survives close/reopen in this layout. G02
owns issuance, replacement, self-revocation and authentication; metadata storage
alone confers no authority. Logical exports and restore must exclude these rows.

The directory and main file must have a protected DACL with exactly current-user
and LocalSystem full-control ACEs, current-user ownership, and private inheritance
on the directory. Creation supplies that descriptor to `CREATE_NEW`; it does not
create broadly and narrow access afterward. Every ancestor is opened without
delete sharing and checked for reparse traversal. Main/lock files cannot be
reparse points or hardlink aliases. UNC, extended/device/drive-relative paths,
alternate streams, dot segments, trailing-dot/space and short-name aliases are
unsupported. Creation/exclusion uses native handles, not an existence check.

SQLite creates transient DELETE journals by inheriting the verified directory's
private current-user/System ACEs. The native test inspects an active journal.
Those transient ACLs are private at creation but are not claimed to carry an
explicit protected-DACL flag. This storage root is independent of the D01 setup
secret-generation layout; it cannot be advertised as certified D01 `ROOT`.
Same-account hostile modification and administrator privileges are outside the
ACL claim. Native guard checks are not a proof against every Windows alias or
every filesystem failure.

The Store keeps one writer and at most two separately leased readers. Obtain a
`Writer`/`Reader`, use `Lease.Do` for sequential calls on its `sql.Conn`, close all
rows inside the callback, then close the lease. Never retain that connection,
use `Raw`, or issue overlapping calls on it. The callback must use bounded SQL
contexts. Open/validation/checkout has a ten-second ceiling, and the binding's
busy timeout is 150 ms. This is an operational bound, not a domain retry policy.

Every checkout sets and reads back FK=1, DELETE, FULL=2, read-uncommitted=0,
normal locking, busy-timeout=150, and query-only on readers. The schema checks
are read-only on existing storage. Writer and reader pools cannot substitute for
each other. Domain transactions must use explicit `BEGIN IMMEDIATE`, `COMMIT`
and `ROLLBACK`; `sql.Tx.Commit` failure semantics remain unqualified.

A separate reader snapshot can block COMMIT. A COMMIT error does not prove
noncommit. S02 must perform original-identity reconciliation, decide when
recovery is established, and call the one-way `Fence` when needed. Lease cleanup
uses a fresh bounded explicit ROLLBACK. A cleanup failure closes the affected
pool and fences further use; it cannot return an uncertain transaction to a
working pool. Store.Close refuses active leases. No automatic domain retry is
provided. There is no recovery-unfence convenience method.

Run the native synthetic tests with an explicit `S01_TEST_ROOT` outside the public
repository and private `GOCACHE`/`GOTMPDIR`, under Go 1.27.3, CGO=0,
GOTOOLCHAIN=local and `-mod=readonly`:

```text
go vet ./internal/store/...
go test -count=2 -v ./internal/store/...
```

Fixtures are retained under that private root. Without it, the native tests skip;
a skipped run is not S01 evidence or qualification. The tests exercise storage
constraints, exact identity/layout/settings, tombstone preservation, attempted
refusals, provenance isolation, zero-byte pins, negative path/ACL cases, journal
ACLs, actual cross-process lock exclusion, separate reader blocking and rollback.
Credential cases verify exact metadata persistence, immutable bindings, one row
per grant, exact 30-day expiry, disabled-grant preservation, current-grant FKs,
explicit replacement/revocation and no domain revision/operation/activity changes.
They do not prove product transactions, restart recovery, interrupted creation,
write/flush faults, hostile security, OS reboot, fsync/power-loss durability,
performance, browser behavior or other platforms/filesystems. Later native
qualification remains required.

Storage regression cases additionally assert specific SQLite FK/trigger codes for
cross-card history and committed attachment fact/pin mismatches, exercise matching
deferred staging in current and source ledgers, retain valid removed-history and
compact non-pinning facts, and verify that an unsupported legal schema lookalike
fails an existing-file open without changing the database bytes.
