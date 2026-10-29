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

## Open improvements to evaluate

These are process changes to check at the next applicable milestone, not new
implementation tickets or automatic work authorization.

| Improvement | Why it matters | Check at the next milestone review |
|---|---|---|
| Put capacity, first failure, cleanup and restart into the early integrated prewalk. | A successful path can conceal awkward state transitions and recovery limits. | Record when these paths were first exercised, what they revealed and whether refinements happened before final handoff. |
| Check the intended handoff channel and size limits early. | A verified artifact can still be inconvenient to deliver or retrieve. | Confirm the artifact fits its intended channel, or establish a usable alternative before closeout. |
| Refresh the remaining-delivery forecast after the bounded risk check and material findings. | Passing implementation does not remove review, feedback, packaging or cleanup work. | Preserve the original and revised forecast, assumptions, timing and remaining observable outcomes; compare like stages. |
| Improve estimation incrementally using qualified reuse and measured results. | Both optimistic late forecasts and excessive padding can mislead planning. | Explain which reusable work or uncertainty changed; do not apply a universal discount from one result. |

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
