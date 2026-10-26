# Q02 isolated SQLite connection harness

Current author result: **PASS_ISOLATED_SCOPE_PENDING_INDEPENDENT_REVIEW**.
Three native Windows/amd64 NTFS repetitions passed with SQLite 3.54.1, its exact
source ID, all 57 observed compile options matching the frozen source inventory,
and five checked physical connections per repetition. The seven assertion groups
below passed in every repetition. This is isolated binding evidence; fresh
independent review and owner acceptance remain separate gates.

The first readonly build stopped before compilation because the original Q01
manifest lacked eight required indirect declarations. Its LIMITED_NOT_QUALIFIED
evidence remains in `evidence-initial-manifest-limit-r1.json`. The primary applied
the exact additive repair documented in
[`go-module-completeness-2026-10-25.txt`](../../docs/decisions/go-module-completeness-2026-10-25.txt).
The runner checks both manifest hashes, unchanged go.sum and the complete selected
version set against Q01. No dependency version or checksum changed.

This harness is intended to qualify only exact native Windows/amd64, local NTFS
binding behavior recorded in `evidence-r1.json`. It uses synthetic tables and bytes,
separate leased `database/sql.Conn` owners, and the accepted FK/DELETE/FULL
settings. It implements no product schema or recovery policy. Fresh independent
review and owner acceptance are separate gates.

Run from repository root in PowerShell, supplying private directories outside
this repository. Use the verified Q01 dependency cache or another verified cache
with the exact frozen module graph already available:

```powershell
./qualification/driver/run.ps1 -RuntimeRoot $privateQ02Root -ModuleCache $verifiedModuleCache -Repeats 3
```

`run.ps1` checks installed Go 1.27.3, fixes `GOTOOLCHAIN=local`,
`CGO_ENABLED=0`, `GOOS=windows`, `GOARCH=amd64`, `GOFLAGS=-mod=readonly`,
and uses private `GOCACHE`/`GOTMPDIR`. It verifies modules, runs vet, builds
with `-trimpath -buildvcs=false`, and executes three isolated children. Each
child has a fresh on-disk fixture and a 25-second deadline; any missing child,
failed assertion, unexpected error or timeout fails the run. Repeats are bounded
to 1-10; the recorded run uses three. Build/run errors and DB/journal files stay
private. The runner does not download modules, alter pins, install software,
change persistent environment settings, or clean unrelated paths.

The executable independently verifies Go, platform, CGO and exact linked
`modernc.org/sqlite v1.61.0` / `modernc.org/libc v1.79.0`. It queries the native
filesystem type, SQLite version/source ID and complete compile options. Every
physical connection, including clean reopen, must report FK=1, journal=delete,
synchronous=2, read_uncommitted=0, locking=normal and busy_timeout=150 ms, and
must reject an invalid foreign key with extended code 787.

The synthetic checks assert:

- A reserved writer excludes a second writer with SQLITE_BUSY; rollback has no
  visible effects, then the same competing connection can retry and commit.
- A pinned separate read transaction keeps scalar values, binary bytes and a
  zero-byte BLOB inventory stable while another connection modifies them.
  In DELETE mode COMMIT returns SQLITE_BUSY until that reader releases its
  snapshot; explicit retry then publishes the new values.
- A second reader-blocked COMMIT is explicitly rolled back. The previous bytes
  remain intact, and the same physical writer/reader are reused successfully.
- Six further competing writer rounds give exact final state with no lost
  updates. Integrity/FK checks pass; the DELETE journal is absent after clean
  commit; fresh connection reopen retains committed bytes and settings.

Transactions here use explicit SQL `BEGIN IMMEDIATE`, `COMMIT`, and `ROLLBACK`
on leased `sql.Conn`. Successful failed-COMMIT retry/rollback cleanup does not
qualify `sql.Tx.Commit` error semantics; future product work must explicitly
choose and verify its transaction API and cleanup/fencing behavior. No global
application lock serializes the separate connections. Simultaneous calls occur
only on different physical connections. No `Conn.Raw` or internal driver API is
used; no connection has overlapping application calls.

The compile option `MUTEX_NOOP` is not treated as a safety proof or a universal
failure claim. The locked binding's `doc.go:156-177` documents per-connection
state and forbids concurrent use of a driver connection obtained with Raw.
`conn.go:109-123` requests FULLMUTEX; Windows-selected `lib/mutex.go:5-33`
installs custom SQLite mutex callbacks and `lib/mutex.go:39-95` defines Go-backed
mutex state. These source observations explain the tested ownership boundary;
they do not prove all driver code race-free. See the
[locked binding documentation](https://pkg.go.dev/modernc.org/sqlite@v1.61.0),
[SQLite threading modes](https://www.sqlite.org/threadsafe.html), and
[SQLite rollback locking](https://www.sqlite.org/lockingv3.html).

DELETE mode allows a pinned reader alongside a reserved writer, but an active
reader blocks commit. This harness preserves that contract; it does not switch
to WAL or claim that writes can commit throughout a long export. Product export
duration, contention policy, bounds, and admission remain later work.

No race detector was run: its native Go mode requires CGO, and this qualification
retains CGO=0. No product behavior, cross-process locking, recovery, write/flush
faults, OS reboot, power loss, fsync durability, broad safety, or throughput is
qualified. Child watchdog termination is harness containment only. Evidence
contains SHA256 identities, exact commands, frozen environment, expected/actual
assertions and limitations; raw paths, logs and runtime databases remain private.
