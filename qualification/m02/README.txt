M02.06 JOINED READ/EDIT QUALIFICATION | 2026-10-29

Tested product source: 4ef21bb35be537e321be790277e34d73e1109210
Candidate SHA256: f94146e1f194a3f50c2e2a7febbfd6ecee85a1085c7f28e7b66a8c36182bbd6f
This report is documentation only; report commit is distinct from tested source.

Outcome
The scoped M02 read/edit acceptance matrix is covered by joined runtime evidence,
exact-candidate accepted focused journeys, controlled boundary probes and pinned
source/build checks. M02.07 customer demonstration and final handoff remain gated
by owner acceptance of this qualification. No product source changed for M02.06.

Runtime and case trace
- New actual browser/built-CLI journey: 95 assertions, two projects/three boards,
  daily fields/owner-clear/explicit priority/repeated comments, NO_CHANGE, stale
  two-window conflict, private reload and existing human-only approval/Completed.
- Labeled native faults: real CardRead and HistoryRead 200 held while another
  same-ticket board paints; one committed PATCH response dropped. Original
  UNKNOWN survives owned host restart/reload and settles only by deliberate
  matching opId/fingerprint lookup; reconnect/read never replay.
- GET fault forwarding supplies explicit same-origin Origin because Playwright
  API forwarding omits browser fetch metadata. Native bodies are unchanged; this
  mediation is separate from ordinary browser/security evidence.
- Pins/Attention shared-window smoke and private-input boundaries pass. Three
  owned hosts stop exit 0; original listener closes. All failed attempts retained.
- Exact same candidate reused from accepted M02.04/.05: 100 ordinary keyboard
  assertions (25 current comments/25 prior states/all local pages), 55 actual CLI
  checks (workspace and --data selectors/refusals/lookup), 26 prior independent
  CLI audit assertions plus byte-identical rebuild. These are audited reuse,
  not newly rerun or independently executed ordinary journeys.
- Accepted M02.02 earlier-candidate mixed-attribution fixture: 48 assertions,
  13 browser-human / 12 CLI-automation comments. Five unchanged reader/client/
  history/CLI projection production sources, three embedded modules and two tests
  match accepted R3 and commit 5a478f9 bytes; current-candidate
  page/edit proof bridges later view/editor changes. Not a same-executable claim.
- New controlled supplements: editor 20, assignment 7, dependency 8, details 27,
  window identity 9, private reload 4, pending reload 3. Independent current compiled
  boundary probes execute 21 author history + 9 author current + 6 reviewer cases.
- R01 facts/pages/errors, R02 conflicts, R03 no-op/comments, R04 late A/B,
  R05 UNKNOWN/restart, R06 keyboard/IME/focus, R07 selectors, R08 authority,
  R09 private-window lifecycle: exact private trace records evidence roles.

Build integrity
- Two isolated clean builds pass 1,560 tests each, zero failed/skipped test cases;
  four known no-test-file packages are accounted separately. All 70 generated/
  staged/schema/executable output hashes match across builds and candidate.
- Pinned production TypeScript and explicit component compilation, contractgen
  and assetstage checks, go mod verify, go vet and native executable build.
- Fresh source trees, node_modules/npm caches, empty independent Go caches and
  new protected synthetic fixture children; locked Go module cache reused
  read-only/offline. No dependency/tool/config upgrade or existing ACL mutation.
- Build A commands passed before a private classifier confused four package
  no-test-file events with skipped tests. Original failure retained; corrected
  parser independently audited against command/log/source/archive hashes,
  requires zero test skips/failures and exact known package accounting.
- Native writer/storage/recovery/compiler/driver production inputs unchanged
  from accepted M01 a0722d3. 235 independent reuse assertions bind 218 files,
  public results/reviews and private M01 packet (423 unchanged input bindings).
  Tagged native/compiler suites are retained bounded evidence, not newly run
  by the ordinary suite. Historical compiler sample retains its original
  Node 24 context; current pinned Node 26 product outputs are separately built.

Independent review
Fresh non-author Sol / High review passes the scoped qualification with no
remaining material finding. Independent source/binding 40, boundary 30 author + 6
reviewer cases, classifier 24, unchanged reuse 235, final product 22 and final
static/build/trace 73 audits are retained privately. Reviewer verified original
logs, complete output sets, source/archive stability and R01-R09 provenance.
This is an independent source/evidence/probe audit, not a second execution of
the entire ordinary customer journey.

Limits
No physical power-loss/OS reboot, installed OS IME, full accessibility or release
certification, V1 parity, live-data import/cutover, permission/workflow/storage
redesign, M02.07 completion or owner approval is implied. Raw runtime profiles,
credentials, fixtures and logs remain private. Original failed attempts and
prior reviews are retained; no production guard or test expectation weakened.
