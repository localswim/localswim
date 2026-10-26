S03 - recovery previews, retention and protected-byte lifetimes

Authority and boundary
Accepted architecture 03/06/18 and corrected 09/20/21, D02's exact recovery
preview section, C01 frozen intents, C03 DTO/canonical validation and D03 terminal
envelopes control this package. Public MIT FOSS; synthetic qualification only.
Prior exposure is disclosed; no independently isolated clean-room claim is made.

The package uses the actual S01 physical Store, S02 transaction/outcome engine
and G01 current grant admission. It implements no HTTP/CLI/UI route, attachment
upload, ZIP/package IO, restore, production scheduler, migration, grant issuance,
actor bootstrap or operating-system durability promise.

Recovery composition
- New requires the actual Store and G01 Service. G02/T01 consumers supply their
  existing live verifier to G01; frozen actor IDs and previews establish no
  authentication. Native-owner credentials and automation cannot acquire the
  ordinary human read plus history-admin rights required at both calls.
- Preview accepts only C03 PreviewRequest and independently resolved route/actor.
  Current rights, board identity, revision, policy and selection share one actual
  reader transaction. It writes no database row, event, revision or receipt.
  Its errors expose stable categories; adapters must retain the ordinary D03
  read/refusal transport rules rather than inventing a terminal outcome.
- The server stores exact immutable canonical response bytes in a private process
  cache. The hash covers the C03 response with only previewHash removed. The
  sampled previewedAt is UTC at whole-second precision; expiry is exactly 300
  seconds later. Returned DTO mutations cannot change the cached response.
  Restart makes the cache unavailable. New preview publication discards expired
  entries; no persistent preview table or recovery sidecar is populated.
- Confirm requires a C01 frozen recovery intent with D03 preconditions and an
  independently resolved matching route. S02 resolves an existing exact terminal
  identity first, under current original read-own-outcome authority, including
  after cache loss, expiry or full-payload compaction. That path needs no pin
  guard, current preview or renewed command rights. Original lookup remains S02
  Engine.Lookup; source-archive evidence never becomes destination authority.
- For a new identity, S02 rechecks current admission and exact board revision.
  S03 checks current ordinary read/history-admin again and recomputes the exact
  selection, retention version and byte inventory using the ORIGINAL previewedAt
  and expiresAt. Current time only determines whether expiry has been reached.
  Current export pin changes alter the hash even without a board revision.
- Missing, expired or mismatched previews produce their D02 stable refusal codes.
  S02 atomically records eligible NOT_COMMITTED evidence. If its COMMIT, cleanup
  or readback is uncertain, only S02's exact retained evidence may resolve it;
  an absent receipt or unchanged domain state never proves noncommit. There is
  no automatic confirm or transaction retry.

Selection and compaction
- history.clear is board-wide and inclusive throughTime, covering full earlier
  snapshots and full terminal payloads across current and source provenance.
  throughTime cannot exceed the original preview time. Clear selects no activity
  or current content and leaves expiry floors unchanged.
- retention.set requires all three day values in 1..3650 and immediately prunes
  eligible history, activity and terminal payloads in the policy transaction.
  S01 defaults remain 90/365/180. Eligibility is storedTime plus days*86400 less
  than or equal to the sampled time; timestamp/counter overflow refuses.
- Floors advance monotonically for the current origin, even when no new record
  is selected. Gaps are recorded only for origin/class combinations actually
  selected, retaining reason, inclusive cutoff, actual commit time and revision.
  No synthetic history/event or unavailable interval is invented.
- Snapshot state/prose and full operation intent/result are removed while exact
  identity, attribution, times, references, compact terminal facts, byte facts,
  approval facts and ticket allocations survive. Entities used only by known
  compacted records become metadata tombstones; arbitrary missing rows never do.
  Current references, surviving full history and surviving full COMMITTED
  operation references retain their required complete entity metadata.
- S02 owns the one recovery-confirm board revision increment. S03 adds one
  attributable activity and typed recovery result. Only retention.set increments
  retentionVersion; workflow policyVersion stays 1. No current card revision,
  lane, priority or currentApproval changes, including for Completed cards.

Trusted maintenance seam
- Expire requires an explicit trusted internal MaintenanceVerifier in each call.
  Its principal must identify a preexisting automation actor and the exact
  legitimate system label, checked in the same writer transaction. The callback
  establishes server lifecycle authority; its returned label is inert historical
  attribution and is never accepted from a request or inferred from grant rights.
- P01 must explicitly configure that legitimate attribution. No default actor,
  scheduler, human/native-owner substitution, actor insertion or actor rewrite is
  supplied. Expire applies only the persisted policy, never caller day values or
  arbitrary clear inputs. It increments one board revision, records one
  recovery.expire event with opId=null and returns an internal revision result.
  RetentionVersion, current content and compact terminal facts remain unchanged.
- Maintenance failures have no invented terminal operation. A COMMIT/cleanup
  uncertainty returns unknown and fences this Store; explicit validated reopen
  is required. This seam does not claim a production maintenance service exists.

Shared export and byte seam
- RegistryFor returns the same process registry for every consumer of one S01
  Store. A01/A02 must use that registry; independent pin registries are invalid.
  A registry lasts with its Store for the process lifetime. A closed Store cannot
  be reused; active export readers must close before S01 Store.Close succeeds.
- BeginExportSnapshot obtains the writer slot, the cancellable registry guard,
  then an actual S01 reader lease/transaction. It freezes identity, revision and
  its distinct protected digest set before publishing its pins and releasing the
  writer/guard. It provides bounded SELECT-only access to that same snapshot.
  The trusted caller must establish current export authority inside that exact
  snapshot before exposing data or output. Read callbacks close all Rows, honor
  cancellation and do no external IO. This seam supplies no export permission.
- Lock order is writer -> registry -> reader checkout -> BEGIN/SELECT. Preview
  takes registry -> reader checkout -> BEGIN/SELECT. Confirm takes its guard
  INSIDE S02 Admit, after writer admission, and holds it through S02 completion,
  including terminal reconciliation. An idle reader therefore never consumes a
  pool slot while waiting for recovery's guard. All guard waits honor context.
- Export Close first ends/closes its reader WITHOUT waiting for writer or registry.
  Only then does it acquire writer -> registry, remove its registration and
  reclaim any final unprotected BLOBs. This order lets a reader-blocked DELETE
  COMMIT finish without a registry deadlock. No-op byte cleanup opens no write
  transaction. A cleanup failure conservatively retains its pin registration;
  COMMIT/cleanup uncertainty fences the Store. Byte housekeeping adds no domain
  revision, activity or operation. Failed cleanup requires recovery, not retry.
- Distinct current, full-history, full-COMMITTED-operation and active-export
  digests form the protected union, inclusive 64 MiB total and 10 MiB per BLOB.
  PinGuard.CheckQuota is the shared A01 admission check for that complete union;
  call it inside writer admission and keep the guard through transaction cleanup.
  Equal digests require equal lengths. Zero-length BLOBs remain real entries.
  Compact/refused byte facts preserve evidence but never pin bytes. Reclaimable
  is domainBefore minus domainAfter and active exports. Deletion checks actual
  remaining references; dropping one of several pins cannot reclaim a digest.

Verification scope
The native tests use private synthetic SQLite stores with current/source history,
terminal receipts, approval facts, immutable entities and real BLOB rows. They
exercise read-only previews, original-time hashes/selection, receipt-first replay,
rights/revocation/revision checks, policy boundaries/floors/gaps, compaction,
Completed preservation, source/refused non-pinning facts, quota arithmetic,
trusted maintenance rollback/commit uncertainty, actual reader-blocked COMMIT,
export/close/queued reader races and guarded S02 reconciliation. The test verifier
is synthetic and does not qualify G02 credentials. These tests prove neither
archive output nor power-loss durability; later A/K/P/native qualification remains.
