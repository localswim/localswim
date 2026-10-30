# Continual process improvement

This is a living record of what improves delivery, what still causes friction,
and what to try next. Review and update it after each milestone as evidence grows.
Keep the current practices and open improvements useful; retain dated observations
so later revisions do not erase what we believed or what actually happened.

This document records learning, not permission to start another milestone or add
product scope. Record acceptance separately from implementation and review results.
Use public-safe summaries and repository evidence; keep private boards, credentials,
runtime profiles and raw operational records out of this document.

## Current practices to retain

- Bound milestones around a small customer outcome and an integrated demonstration.
  Supporting tasks should identify the outcome they enable and their prerequisites.
- Price complete delivery: ordinary refinement, integration, independent review,
  demonstrations, documentation, packaging, cleanup and handoff belong in base work.
- Bound and time Preflight separately. Check existing readiness at actual entry
  points; new feature uncertainty belongs in development.
- Use active elapsed time through the integrated sequence. Count parallel work
  once, include coordination and model/tool/build/test waits, and timestamp excluded
  owner-only waiting. This is not summed worker effort or a measurement of credits.
- Distinguish normal product use, controlled component checks, injected faults and
  reused evidence. Bind reuse to compatible source, executable and material inputs.
- Use useful parallel work and fresh independent review without reserving idle
  reviewer pairs. State what the reviewer actually inspected or executed.
- Keep original estimates and later forecasts. Name residual uncertainty, leave
  reserve unscheduled, and preserve spending authority separately from forecasts.
- Bound interrupted-session exclusions with recorded events. Include any reviewer
  or tool work that continues after the parent stops; exclude only the interval
  when all scoped work is inactive. Preserve the original and corrected receipts.
- Validate the extracted delivery package and its referenced guides as well as its
  hashes. Compare complete retained records for conservation claims, with any
  required identity transformation stated explicitly.
- Follow local guide and generated-schema references recursively. Distinguish
  ordinary runtime instructions from qualification procedures that require the
  full source tree, pinned tools and private execution outputs.
- Classify product defects, coverage gaps and harness assumptions separately.
  Retain failed attempts, diagnose them against the accepted contract, and use a
  fresh corrective scenario without replaying uncertain operations.

## Open improvements to evaluate

These are process changes to check at the next applicable milestone, not new
implementation tickets or automatic work authorization.

| Improvement | Why it matters | Check at the next milestone review |
|---|---|---|
| Put capacity, first failure, cleanup and restart into the early integrated prewalk. | M03 exercised these before final demonstration, but a draft-cap test initially assumed the separate submission budget must also refuse. | Identify the actual admission boundary and independent budgets; prove visible input, retained evidence and safe continuation without inferring an original outcome. |
| Check the intended handoff channel, size and package contents early. | M03 final review found a missing CLI guide. M04 traversed guide/schema references and ran the extracted kit before final review; no missing runnable step remained. Its ZIP still required local delivery. | Retain the early size check, recursive reference audit and extracted use. Record whether final review finds a missing step or an unusable delivery path. |
| Refresh the remaining-delivery forecast after the bounded risk check and material findings. | M04 preserved forecasts but still allowed 75 P80 minutes for final delivery at qualification handoff; that card took 8.7 minutes. | List retired and remaining risks at each checkpoint, then justify the residual allowance against remaining observable work. Preserve original spending authority and compare like stages. |
| Improve estimation incrementally using qualified reuse and measured results. | M04 finished 52.3% below its P80 base. That is a budget success, not calibrated confidence coverage; unchanged uncertainty allowances also warrant examination. | Explain which reusable work and named risks changed. Do not apply a universal discount or mechanically retain the full original uncertainty after risks are resolved. |
| Run existing archive/search/focus regression and relevant joined-feature scenarios before feature handoff. | M04 qualification found an ordinary Close-focus regression and missing joined hierarchy/blocking-link coverage after feature approval. The earlier-prevention goal remains partial. | Before the next authorized feature handoff, exercise existing interactions plus its new cross-feature boundary. Retain asynchronous read/window/generation, restored-original search and UNKNOWN/no-replay checks; record whether qualification still catches a covered defect. |
| Use full-record comparisons in the first restore prewalk. | M03 began with counts/existence checks. M04 delivered complete records and Go-canonical immutable bytes; transport and archive envelopes also exposed different field assumptions. | State the compared envelope, complete record set, exact bytes and allowed identity transformations at the first restore prewalk. Do not infer early coverage from a stronger final report. |
| Reduce recurring harness setup and contract assumptions. | M04 retained failures from missing private-root environment, ambiguous normal/linked card selectors and omitted-versus-null field expectations. | Before a broad run, verify fixture environment, exact target selectors and optional-field encoding against the current contract. Reuse validated setup where appropriate; keep product findings distinct from harness costs. |

## M02 — read and edit cards comfortably

**Accepted:** 2026-10-29. Full owner acceptance followed the final handoff; remaining
board lane changes were owner bookkeeping. The historical handoff report records
the earlier acceptance-pending state and remains evidence of that checkpoint.

### Time and estimation

Reported active elapsed minutes, rounded to one decimal:

| Phase | Approved P80 base target | Reserve | Base + reserve / P95 target | Actual |
|---|---:|---:|---:|---:|
| Preflight, separately tracked | 35 | 15 | 50 | 7.8 |
| Development through final handoff | 225 | 75 | 300 | 163.5 |

No reserve was used in either phase. Development finished 61.5 minutes below base
and 136.5 minutes below base plus reserve. Combined actual time was 171.3 minutes;
the two phase ledgers remained separate and unused Preflight allowance was not
transferred into development.

These were judgmental planning targets, not statistically validated quantiles.
The 225-minute base was a P80 target, not an expected average. Finishing 27.3% below
it is a budget success; it does not by itself prove padding or establish calibrated
P80/P95 coverage. Do not mechanically reduce the next estimate by that percentage.
Do not add phase quantiles and label the result a combined confidence guarantee.

### Comparison with the FGA lessons

The relevant FGA lessons concern actual integration and evidence categories
(LL002/LL003), scope discipline (LL009/LL011), integrated customer review (LL014),
bounded readiness (LL015), actionable UI states (LL016) and estimate calibration
(LL017). Transfer the method and checks, not FGA-specific architecture or durations.

| Lesson | M02 assessment | Supporting observation |
|---|---|---|
| Small customer outcomes and clear integration | Strong | The oversized proposal was reduced to read/edit; search/archive, hierarchy/links and workflow changes were left to separate milestones. |
| Assembled workflow before owner acceptance | Strong | Actual browser/CLI journeys and fresh independent review preceded final acceptance. Focus/caret issues were corrected and re-reviewed before handoff. |
| Evidence appropriate to the claim | Strong | The exact candidate tied together ordinary use, labeled faults, controlled probes and explicitly bounded reuse. Test counts were not substituted for the customer demonstration. |
| Complete-delivery accounting | Strong | The development actual included fixes, review, demonstrations, guides, packaging, cleanup and publication, with parallel elapsed counted once. |
| Separate readiness, reserve and owner waiting | Strong | Preflight remained separate; owner-only waits were excluded and reserve remained unused. |
| Early failure-state and recovery review | Partial | Draft capacity and the mounted-editor guard were discovered in the final prewalk, before acceptance but later than desirable. |
| Forecast calibration | Still developing | The approved envelope was respected and original figures retained. One result below P80 does not validate confidence claims or establish the right allowance for the next milestone. |

### What the final prewalk taught us

- Existing private recovery permits 20 live drafts and 2 MiB per installation/profile
  across boards and windows. Confirmed actions can retain draft copies. The normal
  demonstration filled that limit; persistence safely refused while input remained
  visible. This was an existing bounded behavior, not a newly introduced defect.
- Selected draft cleanup retained all original operations and committed facts.
  Private discard could leave an already mounted editor guarded. The guide explained
  keeping that input for inspection and using a fresh window for new work. This
  documented continuation did not change the underlying behavior or prove ideal UX.
- The board's 10 MiB attachment limit refused the approximately 11.9 MB review ZIP.
  The verified kit stayed local, with its path/checksum recorded and smaller guides,
  manifest and evidence attached. Handoff-channel suitability should be checked early.
- These discoveries were handled before final acceptance and within base time.
  That demonstrates correction and budget discipline; it does not prove that the
  earlier prevention checks were sufficient or measure avoidable rework.

### Evidence and limits

- [Joined qualification](../qualification/m02/README.txt) and
  [structured results](../qualification/m02/results.json): two isolated clean
  builds, 1,560 tests each, 70 equal output hashes and a 95-assertion joined journey,
  with normal/fault/controlled/reused evidence distinguished.
- [Final handoff](../qualification/m02/HANDOFF.txt) and
  [structured handoff results](../qualification/m02/handoff-results.json): a new
  108-assertion normal demonstration, independent guide/evidence/package review,
  archive integrity checks and six clean owned-host exits across retained attempts.
  Independent review was not a second execution of the entire normal journey.
- [Startup/use/recovery guide](runbooks/m02-start-use-recovery.txt) and
  [customer prewalk](runbooks/m02-customer-demo.txt) describe the accepted bounded use.
- These results concern the scoped synthetic localhost milestone. They do not
  establish a general release, full V1 parity, installed OS IME, whole-product
  accessibility or physical power-loss certification.

## Review format for subsequent milestones

Add a dated entry and update the current practices/open improvements above:

1. **Outcome and acceptance:** What can the user now do? What remains deliberately
   outside scope? Distinguish tested, handed off and owner-accepted states.
2. **Time:** Original base/reserve, separately timed Preflight, revised forecasts,
   actuals and excluded owner waits. Preserve scope and accounting differences.
3. **Prevention versus correction:** Which checks caught problems early? What first
   appeared at final prewalk or owner review? What was corrected or accepted as a limit?
4. **Evidence and review:** Actual changed boundaries demonstrated, reused evidence,
   independent reviewer role, cleanup and remaining assurance limits.
5. **Learning:** Which open improvements helped, failed or remain untested? Change
   them with reasons and supporting evidence; avoid turning sparse results into rules.
6. **Next small adjustment:** Name a concrete process change and how the next review
   will judge it. Keep effort proportionate and distinguish proposals from authorization.

## M03 — find and retire work

**Accepted:** 2026-10-29. Terry approved M03 after the final handoff. The earlier
handoff reports retain their acceptance-pending state and provisional timing as
historical checkpoints; this entry records acceptance and the closed actuals.

The delivered customer outcome is literal current-text search and typed filters,
unfinished-card archive/unarchive, explicit archived inspection and re-find through
the ordinary browser and real CLI. Explicit priority, read-only Completed cards,
board/window identity and original UNKNOWN/no-replay rules remain intact.

### Final time and estimation

Active elapsed minutes, rounded to one decimal:

| Phase | Approved P80 base target | Reserve | Base + reserve / P95 target | Final actual |
|---|---:|---:|---:|---:|
| Preflight, separately tracked | 25 | 15 | 40 | 8.8 |
| Development through final handoff | 270 | 90 | 360 | 202.9 |

No reserve was used in either phase. Development finished **67.1 minutes below
base** and **157.1 minutes below cap**. Combined actual was 211.7 minutes, about
3 hours 32 minutes; the phase ledgers remained separate and no unused Preflight
allowance was transferred into development.

The exact closed development actual is 202.91726443833332 minutes; Preflight is
8.785161245 minutes. Development includes implementation, fixes, coordination,
model/tool/build/test waits, independent review, demonstrations, guides, packaging,
cleanup and source/board publication, counting overlap once. This later requested
post-acceptance documentation update is outside that closed delivery measurement.

Documented inactive owner waits are excluded. An interrupted turn initially left
the timer counting idle time. App event metadata established **54.6167 inactive
minutes** after the last reviewer finished and before the parent resumed, at
one-second event precision. The correction retained reviewer work after the parent
stopped and all resumed publication/timing work. Original and corrected receipts
remain evidence; the earlier 250.5-minute receipt is superseded. The historical
199.9139-minute report snapshot preceded final review/publication and is not the
final actual.

Remaining-delivery forecasts were preserved as preparation snapshots, including
the work still needed for each handoff:

| Forecast checkpoint | Active at preparation | Projected whole P80 | Projected whole P95 |
|---|---:|---:|---:|
| After archive, before its final handoff publication | 97.9 | 273.1 | 331.8 |
| After search, before its final handoff publication | 168.4 | 253.7 | 282.9 |
| After joined qualification, before its final handoff publication | 188.8 | 216.6 | 227.0 |

The approved 270 + 90 envelope did not change. Forecasts retained the original
remaining story sizes and uncertainty rather than fitting a faster pace or
discounting setup. The exceptional correction cycle belonged to qualification
and was not charged again to final handoff once qualification finished.

Finishing 24.8% below the development base is a budget success, not evidence that
P80/P95 coverage is statistically calibrated or that future work should receive
that percentage discount. Base is a confidence target, not an expected average.
Do not add phase quantiles and label their sum a combined confidence guarantee.

### What worked and what still needs improvement

| Lesson | M03 observation | Practical consequence |
|---|---|---|
| Complete customer slices and owner gates | Archive and search each had ordinary client proof and fresh review before acceptance; joined qualification and final kit stayed separate. | Preserve turn-sized outcomes and explicit prerequisite acceptance; RFR alone does not authorize dependent execution. |
| Useful parallel work and independent review | Disjoint Go/CLI and browser authors worked alongside primary integration; finished work passed to fresh reviewers. | Keep ownership clear and use fresh review without reserving idle reviewer pairs. |
| Early failure and recovery prewalk | Native-response loss, restart, UNKNOWN/no replay, capacity and delivery-channel checks occurred before the final normal demo. | Retain early integrated checks; this does not establish that every feature edge was tested early enough. |
| Native interaction and restored state | Review/prewalk found focused archive details, first pending search revision, actual opener/page focus and retained-commit reload ordering issues. | Material corrections were re-reviewed before acceptance. Move native asynchronous focus and restored-original search checks earlier than the broad feature gate. |
| Test assumptions versus product defects | The first capacity test wrongly assumed a full editable-draft budget must prohibit every deliberate submission. | Identify the separate admission budgets and inspect exact durable original evidence before declaring unsafe dispatch. Preserve failed assumptions and their correction. |
| Strength of conservation evidence | Initial restore checks used history counts and link existence; review required full comparisons. | Compare complete card/comment/attachment/history records, links after only required incarnation rebasing, and exact blob bytes from the first restore prewalk. |
| Usable delivery beyond byte integrity | Size checks selected local delivery early; final review still found a referenced runnable CLI guide missing from the kit. | Traverse guide dependencies as well as validating hashes. The corrected 17-file kit includes CLI and keyboard guides. |
| Timing across interruptions | A stopped parent did not immediately stop the reviewer; later idle time inflated the first receipt. | Derive exclusions from the last active worker through actual resumption, preserve correction history and keep post-acceptance maintenance separate. |

### Capacity, evidence and delivery limits

- Ordinary UI checks created 20 live drafts, then demonstrated refusal of draft 21
  while visible input remained. Search preserved the existing drafts and UNKNOWN
  original without lookup or replay. Editable drafts use a 20-draft/2 MiB budget;
  immutable submissions have a separate 100-unresolved/8 MiB budget. A deliberate
  Save at draft capacity was safe because exact original bytes were strictly
  stored and read back before native dispatch. Visible input alone is not durable
  recovery proof. The fresh check covered the draft-count boundary; other unchanged
  storage boundaries remained explicitly qualified reuse.
- Two isolated clean builds passed 1,596 Go tests each and matched all 74 generated,
  staged and executable output hashes. Fresh joined qualification recorded 45
  assertions including separately labeled response loss. Exact unchanged reuse
  comprised 175 controlled DOM/IndexedDB assertions, 137 joined browser/CLI
  assertions including labeled delayed reads, native mandatory inventories and
  21 browser cases. These categories are not all fresh ordinary executions.
- The final extracted-kit normal demonstration passed 42 assertions with two
  boards and two headless browser pages, including complete committed-fact/query
  preservation across graceful restart. Both final demo hosts exited cleanly and
  the listener closed. Fresh independent final review included package extraction,
  CRC, allowlist, manifest hashes, help and evidence checks; it was not a second
  execution of the whole customer journey or full suite.
- The 21,476,864-byte application and final 11,750,665-byte ZIP exceed the board's
  10 MiB attachment channel. The verified local kit and small report/manifest
  attachments supplied the usable alternative. Guide-only package revisions kept
  the tested executable bytes unchanged and were independently rechecked.
- Acceptance covers the scoped synthetic localhost milestone. It does not imply
  real-data import/adoption, M04+ parity, a general release, physical power-loss
  certification, full installed OS IME or whole-product accessibility assurance.

### Next small adjustments

1. Run native asynchronous focus and restored-original search checks before the
   broad feature gate. Record whether the next scoped defect is caught earlier.
2. Start the first restore prewalk with complete retained-record comparisons and
   an explicit identity-rebasing rule; avoid upgrading count/existence evidence
   into a conservation claim later.
3. Audit referenced guides and run the extracted package while checking channel
   size. Judge success by whether final review still finds missing runnable steps.

These are process checks for the next authorized milestone, not permission to
start it or add product scope. Original estimates, superseded forecasts, failed
attempts and historical handoff snapshots remain unchanged.

See [joined qualification](../qualification/m03/joined-qualification-r1.txt),
[structured qualification](../qualification/m03/joined-qualification-r1.json),
[final handoff](../qualification/m03/HANDOFF.txt),
[structured handoff](../qualification/m03/handoff-results.json) and the
[start/use/recover guide](runbooks/m03-start-use-recovery.txt). Final private clock
receipts and the interruption reconciliation supply the closed timing above;
historical public handoff snapshots retain their original checkpoint meaning.

## M04 — organize cards with hierarchy

**Accepted:** 2026-10-30. Terry approved the final card and the M04 milestone after
handoff. Earlier reports and receipts retain their acceptance-pending checkpoint
meaning. This post-acceptance review does not reopen those cards or start M05.

The delivered outcome is bounded parent/child hierarchy through the ordinary
browser and actual CLI: create a child, navigate exact parents and complete
children, reparent and explicitly clear. Archived relatives and Completed parent
targets remain usable under the accepted rules. Hierarchy preserves independent
priority, lane, owner, approval and existing blocking/informational link semantics.
Explicit offline compatibility and retained original-outcome safeguards accompany
the feature. Expanded typed-link and atomic-batch work remains in M05.

The assessment is **strong delivery and evidence, partial early prevention, and
forecast calibration needing improvement**. The four-hour P80 milestone limit
helped separate a complete hierarchy outcome from the larger relationship scope.

### Final time and estimation

Active elapsed minutes, rounded to one decimal:

| Phase | Approved P80 base target | Reserve | Base + reserve / P95 target | Final actual |
|---|---:|---:|---:|---:|
| Preflight, separately tracked | 20 | 15 | 35 | 4.4 |
| Development through final handoff | 235 | 75 | 310 | 112.1 |

No reserve was used in either phase. Development finished **122.9 minutes below
base** and **197.9 minutes below cap**. Combined actual was 116.5 minutes; the
separate phase ledgers and unused Preflight allowance were preserved.

The closed development actual is 112.09733566666667 minutes; Preflight is
4.365245533333334 minutes. Development includes refinement, fixes, coordination,
model/tool/build/test waits, independent review, demonstrations, documentation,
packaging, cleanup and source/board handoff publication. Parallel elapsed counts
once; timestamped inactive owner-only waiting is excluded. This later requested
retrospective documentation work is outside that closed delivery measurement.

The retained development checkpoints allow a comparison of forecasts with the
work actually remaining at each handoff:

| Handoff checkpoint | Cumulative active | Remaining P80 allowance | Remaining P95 allowance | Subsequent actual to final handoff |
|---|---:|---:|---:|---:|
| Feature handoff, before qualification | 81.4 | 105 | 180 | 30.7 |
| Qualification handoff, before final kit | 103.4 | 75 | 150 | 8.7 |

These are closing handoff checkpoints, distinct from earlier forecast-preparation
snapshots. The approved 235 + 75 envelope remained unchanged. After qualification,
the forecast still retained a 20-minute central final-kit anchor, the full original
55-minute ordinary uncertainty allowance and 75-minute tail allowance. Preserving
the original estimate and spending authority was useful; mechanically carrying
the full uncertainty forward made the late forecast less informative.

Finishing 52.3% below a P80 target does not establish padding or statistically
calibrated P80/P95 coverage. P80 is not an expected average, and the milestones
have different scopes and reuse opportunities. The late remaining-work comparison
warrants reviewing which risks had been retired. Revise residual uncertainty with
explicit reasons rather than applying a blanket percentage discount to M05 or
treating unchanged allowances as automatically justified. Phase quantiles must
not be added into a combined confidence guarantee.

### Prevention, correction and delivery learning

| Area | M04 assessment | Observation and consequence |
|---|---|---|
| Small customer outcome and owner gates | Strong | The split held: hierarchy was complete, relationship expansion stayed outside M04, and each dependent story waited for owner acceptance. |
| Complete-delivery accounting | Strong | The final 112.1 minutes includes corrections, review, qualification, the extracted-kit demo, guides, cleanup and publication; Preflight and inactive owner waits remained separate. |
| Early native interaction regression | Partial | Qualification exposed an ordinary hidden-opener Close-focus regression after feature approval. It was corrected and independently reviewed before milestone acceptance; the existing regression belongs before feature handoff. |
| Joined boundary coverage | Partial | The first hierarchy journey retained informational links, while dependency gating was tested separately. Review required both legacy link types alongside hierarchy, exact edge conservation and unchanged blocked-start behavior through reparent/clear. |
| Test assumptions and safe correction | Safe handling; avoidable friction | Missing fixture environment, linked-versus-normal opener ambiguity and omitted-versus-null fields caused failed runs. Contract-based diagnosis and fresh attempts preserved evidence and avoided uncertain-operation replay; earlier harness checks should reduce recurrence. |
| Conservation evidence | Strong final evidence | Full records, history, approvals, legacy links, attachment bytes and canonical original intents/outcomes were compared with explicit identity rules. This final strength does not establish that every check was present in the first attempt. |
| Usable package and guide closure | Improved | Recursive guide/schema dependencies, extracted use, channel size and local delivery were checked before final review. No missing runnable guide step remained; size above the attachment cap was an explicit delivery constraint. |
| Useful staffing and independent review | Strong within observed scope | Disjoint authors and fresh reviewers had clear responsibilities. Terra's bounded guide/dependency assignment was useful; one assignment does not establish a general quality, latency or credit-cost comparison with Sol. |
| Remaining-delivery forecasts | Needs improvement | Original and revised figures were preserved, but the final-kit P80 allowance remained 75 minutes for work that took 8.7. Retire named risks and explain the residual allowance at each checkpoint. |

The Close-focus correction preserved two accepted behaviors: ordinary unavailable
openers fall back to Find cards, while hierarchy-origin navigation retains its
board-read fallback. Existing regression and independent focused probes verified
the distinction. This was successful correction before final acceptance; the
earlier-prevention goal remains open.

The final demo's first failure expected explicit `ownerId:null` for a new
unassigned card. The accepted optional field can be omitted. The corrected harness
accepted that encoding while preserving parent, priority, lane and approval
assertions; a fresh workspace passed. This was a harness assumption, not a product
defect. Failed and superseded attempts remain separately classified.

### Evidence, review and delivery boundaries

- Current-candidate qualification passed two isolated clean builds with 1,641 Go
  tests each, zero failures/skips and all 78 generated/staged/executable hashes
  equal. Complete mandatory native inventories and all 21 browser cases passed.
- Qualification recorded 349 controlled hierarchy DOM checks and a fresh
  browser/actual CLI journey with 69 counted assertions plus expectations. Its
  ordinary steps and separately labeled response-loss check remain distinct. The
  controlled 1,000-card family exhausted all 999 descendants in 50 local pages;
  it is distinct from ordinary small-board creation.
- Separately labeled response loss after a real commit preserved UNKNOWN through
  graceful restart and prevented replay. Two runnable restores conserved complete
  records and exact Go-canonical bytes for 57 and 60 retained original operations.
  Current parent rebasing and historical Origin were explicitly checked. Actual
  supported offline upgrade/refusal/NO_CHANGE and old-data conservation also passed.
- The final extracted-kit normal demo passed 45 counted browser/CLI assertions
  plus five companion checks and DOM expectations, across two boards and two
  headless pages. Complete BoardRead and Active/Archived/All query facts matched
  across graceful reopen. Both final owned hosts exited cleanly; the listener
  closed. This normal demo injected no faults.
- Fresh independent final review executed separate package extraction, CRC,
  exact-member/manifest/checksum, schema-reference, help and cleanup probes and
  audited the demo evidence. It did not duplicate the full normal journey or all
  qualification suites. Final-kit full-suite/fault claims are explicit unchanged-
  application reuse of accepted qualification.
- The 34-file kit contains 32 allowlisted payloads, complete local guide/reference
  dependencies, generated schemas, license and reports. The 11,862,349-byte ZIP
  exceeds the 10 MiB attachment channel. Verified local ZIP/extracted delivery and
  small board attachments supplied the usable path. Full qualification reproduction
  still needs the source checkout, pinned tools and private run outputs.
- Acceptance covers synthetic Windows amd64/local NTFS/localhost use. It does not
  imply expanded M05 features, real V1 adoption, general release, physical
  power-loss certification, full installed OS IME, whole-product accessibility or
  independent clean-room certification. Prior V1 exposure remains disclosed.

### Next small adjustment

**RECOMMENDED:** before the next authorized feature handoff, run the existing
archive/search/focus regression together with the relevant joined-feature
scenario. Retain asynchronous read/window/generation and UNKNOWN/no-replay checks.
Judge success by whether qualification still finds a defect those checks should
already have caught, and record the checkpoint where each finding first appears.

Supporting forecast reviews should name resolved and residual risks instead of
carrying all original uncertainty forward. Before a broad run, check fixture
environment, exact target selectors and optional-field encoding. These are
proportionate process checks, not new implementation tickets or permission to
start M05. Preserve original estimates, failed attempts, historical reports and
the closed delivery clock.

See [hierarchy feature evidence](../qualification/m04/hierarchy-slice-r1.txt),
[joined qualification](../qualification/m04/joined-qualification-r1.txt),
[structured qualification](../qualification/m04/joined-qualification-r1.json),
[final handoff](../qualification/m04/handoff-r1.txt),
[structured handoff](../qualification/m04/handoff-r1.json),
[start/use/recovery guide](runbooks/m04-start-use-recovery.txt) and
[normal customer demo](runbooks/m04-customer-demo.txt). Final private handoff/clock
receipts supply closed actuals and closing forecast checkpoints; public reports
retain their original tested, handed-off and acceptance-pending checkpoint scope.
