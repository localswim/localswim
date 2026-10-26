Separate native restore publication and status

Restore and Status are local library boundaries for a future native CLI adapter.
There is no HTTP/browser restore, activation, scheduler, automatic replay, trust
action or credential bootstrap here. Paths never enter receipts or response JSON.
The trusted adapter supplies all known protected live/source roots. Source archive
directory overlap is checked independently. The package supports qualified local
Windows/amd64 fixed NTFS paths only; unsupported platforms conclude unknown.

Physical composition

NEWDIR.partial-REQUESTID contains restore-receipt.json, restore.lock, and data/.
The data directory contains only S01 board.sqlite and board.lock. The entire
prepared directory is published as NEWDIR by MoveFileEx WRITE_THROUGH without
REPLACE_EXISTING. Existing final/partial destinations are never adopted or removed.
Parent directories are pinned against rename/deletion and checked for reparse
traversal. Paths reject UNC/device/relative/ADS/DOS-device/short-name aliases,
dot segments, trailing dot/space and unsupported lengths. Source files are opened
without write/delete sharing and reject reparse points or multiple hardlinks.

A sibling .restore-REQUESTID.lock in NEWDIR's parent is private operational
metadata retained independently of the destination. Its fixed digest binds the
request and selected final/partial paths; it contains no raw path or domain prose.
Restore creates it exclusively after complete source validation/preflight. Status
opens only an existing matching lease and never creates or repairs one. Missing,
inaccessible or mismatched lease evidence stays unknown.

The sibling lease remains held across construction, native no-overwrite rename,
post-publication validation and complete receipt publication. Windows cannot rename
a directory while a descendant file lease remains open: close restore.lock only
under the held sibling lease, then reopen that exact existing file after rename.
Check the directory, descendant lock and sibling native file IDs across publication;
there is no name-rebinding fallback. Every created directory/file uses a protected
current-user/System-only full-control ACL at creation. Reads check owner/ACL,
reparse and hardlink metadata. Writes flush, close and read back exact bytes.
SQLite FULL synchronization and native rename/readback are observed evidence;
neither proves arbitrary reboot or power-loss durability. Same-account hostile
modification and administrator access are outside the private ACL boundary.

Receipt and conclusion rules

The first receipt follows whole-source validation, representability preflight and
fresh incarnation allocation. Preparing has no target anchor. Prepared is written
only after independent native reprojection proves the entire source transform and
protected byte inventory. Native schema/FK/revision/bytes, logical inventory and
inactive authority tables are checked again after publication. Only then can the
receipt become published/complete. Request/source/destination/creation facts and
the prepared anchor remain immutable; phase/time advance monotonically.

Status examines the exact final and request-qualified partial locations under
existing exclusive leases. Only one valid complete final, no partial, and complete
native validation gives confirmed/exit 0. A verified terminal pre-publication
failure with no final gives failed/exit 1. Missing, incomplete, corrupt, inaccessible,
wrong-request, mismatched binding, both-location or uncertain publication gives
unknown/exit 4. Context cancellation/interruption/flush/rename uncertainty never
manufactures failure or completion. Later activation makes prepared-state checking
inapplicable and yields unknown. Complete evidence is never downgraded due to
subsequent response delivery failure. No status result classifies source noncommit.

Actual synthetic Windows tests cover authenticated A02 to A03 to restore/status,
source byte preservation, source ledger/authority isolation, exact prepared anchors,
no-overwrite/overlap/hardlink/junction exclusion, native cross-process lease
exclusion, returned preparation failure, interruption before/after rename, injected
flush failure, metadata/domain/lineage corruption, absent locks and status without
repair. Failed candidates and qualification logs are retained privately. Fault
injection exercises conservative conclusions; process termination alone cannot
establish native power-loss safety. Later full native qualification remains required.
