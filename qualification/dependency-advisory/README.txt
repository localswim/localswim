DEPENDENCY PRESSURE / READINESS CORRECTION - 0063
2026-10-27

Status: PASS_CORRECTIVE_CANDIDATE. See the independent review below.
This addresses the bounded R01-PRODUCT-01 display gap. Fresh R02 final closure
and release assessment remain separate on #0064 after owner acceptance.

Behavior
- The ordinary browser board separately shows incoming direct blocking dependents
  and prerequisite acceptance/readiness from its current complete board snapshot.
- The ordinary CLI board read command shows equivalent readable advisory facts.
  Its --json envelope and the HTTP/archive/database contracts are unchanged.
  Partial card reads do not invent readiness; use board read for this advisory.
- Assigned priority never changes due to dependency pressure. Current human
  approval, policy, revision and exact identity determine prerequisite acceptance.
  Missing/foreign targets are not satisfied. Go still decides deliberate starts.
- The advisory neither starts work automatically nor adds a scheduling score,
  transitive policy, permission, TLS/LAN mode or recovery replay.

Executed candidate checks
- 1,444 ordinary Go test events passed; zero skipped tests. Contract generation,
  TypeScript check/build, embedded-asset stage/check, module verification, vet
  and the reproducible-flags candidate build passed.
- The full required normal browser/CLI journey passed all 21 cases once, with
  zero retry/skip/error and owned browser/fixture teardown. The original 20 cases
  remain mandatory. New V02-DEPENDENCY-01 covers pressure, RFR awaiting approval,
  actual premature refusal, explicit human approval, refreshed satisfaction,
  deliberate start and unlink while Alpha remains P3.
- Browser application bytes match the checked candidate executable. results.json
  binds source-manifest and executable hashes. Detailed synthetic logs, source
  observations and original failures are retained privately.
- The initial missing runtime-asset declaration was caught by integration checks,
  corrected and verified in a new candidate and fresh browser attempt. Failed
  attempts do not count as passing evidence.

Review and limits
- Independent review: ../../docs/reviews/dependency-advisory-review.txt.
- R01 and prior release/native records are unchanged historical evidence. This
  report does not relabel them as execution of this changed candidate, perform
  R02, approve a release or claim a new two-clean-build comparison/native dispatch.
- SQLite, approval/admission and original UNKNOWN/no-replay behavior are unchanged.
  There is no power-loss, hostile-account, full V1 parity or live cutover proof.
- Only the owner controls Completed. Accepting this correction does not replace
  the separately tracked final closure review.
