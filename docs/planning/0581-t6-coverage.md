# T6 coverage carried forward

#0601 starts from `43d08076285a3ebe97317fdcbcec3ed556b4bd8e`. The table maps
the reviewed T1–T5 evidence to the accepted R01–R20 plan. A gap means the
available comparison leaves a specific claim open. R20 records results from
the integrated candidate in its qualification packet. [Accepted scope](0581-ui-plan.md)

## Evidence by turn

| Turn | Reviewed evidence |
| --- | --- |
| T1 | `e5d01c0` established the running V1 reference, fresh interfaces and fixture recipe. [V1 observations](0582-observed-contract.md), [contracts](0582-contracts.md), [verification](0582-verification.md) |
| T2 | Reviewed head `84d73ad` matches squash `9f9ced3`. Terry confirmed the integrated entry comparison. The correction packet includes the 1366 × 900 and 390 × 844 pairs. [Architect approval](https://github.com/localswim/localswim/pull/1#issuecomment-6855564295), [correction packet](0583-0584-t2-correction.md), [capture manifest](0583-0584-t2-corrected-evidence.json) |
| T3 | Reviewed head `575831f` matches squash `e17790b` and has the same application tree as qualified build `6492315`. Earlier branch builds and different fixture histories are identified per capture. [Architect approval](https://github.com/localswim/localswim/pull/3#issuecomment-6862127331), [handoff](0581-t3-handoff.md), [capture manifest](0581-t3-evidence-manifest.json) |
| T4 | Reviewed head `d11f280` matches squash `b589700`. Corrected preview, badge and damaged-image focus evidence is in the private `t4-exact/manifest.md`. Earlier editor captures align the focused card while board and comment histories differ. [Architect approval](https://github.com/localswim/localswim/pull/4#issuecomment-6881945921), [handoff](0581-t4-handoff.md) |
| T5 | Reviewed head `b71bfd5` matches squash `43d0807`. The private `t5/manifest.md` binds the browser transport proof to the final build and the earlier spacing crops to matching CSS. It identifies older interaction runs and stale whole-window captures. [Architect approval](https://github.com/localswim/localswim/pull/5#issuecomment-6883247569), [handoff](0581-t5-handoff.md) |

The private folders are relative to the output root recorded in the T1
observation packet. Rechecking the tracked T1–T3 manifests against the private
files matched all 66 listed SHA-256 values, including the T3 smoke receipts.
The T4 and T5 Markdown manifests establish build and capture provenance.

## Coverage by requirement

| Ref | Reviewed evidence | Comparison limit or integrated check |
| --- | --- | --- |
| R01 / #0582 | Running V1 reference `46d5d58`, shared interfaces, native V2 provenance, independent fixture consumers and eleven V1 observation images. | Foundation evidence. Use fresh equivalent data, recorded CSS viewport, DPR and zoom for R20. |
| R02 / #0583 | Paired populated, empty and unavailable workspace, delayed board loading at desktop, and Beta selection with keyboard focus. | Refresh the current entry comparison at both approved widths. The corrected T2 manifest has no paired narrow loading frame. |
| R03 / #0584 | Paired lane and card layout, scroll, empty board, grants and focus. Native UUIDs occupy V1's observed route shape. V2 applied a disposable stored stylesheet. | The V1 sidecar trial did not establish the legacy selector set. Later drawer/attachment captures cover additional states at other widths. |
| R04 / #0585 | Pin/unpin, keyboard reorder and focus, saved order after reload, sidebar resize/collapse, and V2 truncated pin-save recovery with native readback. | No explicit paired pointer reorder, board search, or unavailable-row recency readback in the cited evidence. |
| R05 / #0586 | Title, description and comment searches, clearing via keyboard/pointer, empty results, lane scroll reset and the older Completed toggle. | Include a paired ticket lookup in R20 if using search to reopen the journey card. |
| R06 / #0587 | Keyboard drawer opening, long literal description, comment read and navigation to an archived linked child. The literal ticket number matches the observed long-description case. | Preserve that distinction from a functional relationship link. Capture focus return and aligned drawer metadata/audit with the journey's actual fixture. |
| R07 / #0594 | Matched title/description editor behavior, owner cycling, individual priority, stale refusal with draft retained and truncated-response title inspection. Native archive and unarchive remain available. | The bounded V1 survey found no archive/restore browser action. V2 audit inspection can confirm equivalent field changes at the expected revision. The audit has no request ID to identify one client among clients using the same Human identity. |
| R08 / #0595 | V2 multiline keyboard submit, one exact native comment, held and dropped responses, original-comment inspection, draft persistence over reload/board switches and posting via an archived link. | The supplied V1 screenshots observe the composer. They do not pair a committed multiline post and its settled history with V2. |
| R09 / #0588 | Paired empty and inverse relationship drafts, initial title focus, description Enter, V2 cancellation and one recovered create with the original ordered link. T5 adds file staging. | Earlier relationship choices differ with fixture history. Compare equivalent choices, validation, cancel/focus or post-create navigation where exercised by R20. |
| R10 / #0596 | Both versions followed and removed a link and changed its direction with matching feedback. V2 verified the settled removal and inspected a committed reversal after a lost response. | The turn's exact matched screenshots focus on reversal. Different full-board histories limit whole-window claims. |
| R11 / #0589 | Permitted/denied drag, focus and pre-drag scroll preservation, native lane/revision/audit and V2 stale/lost-response checks. Terry confirmed the V1 surface is pointer drag. | Paired drag fixtures have different counts and scroll offsets. Use the same starting membership and scroll in any R20 drag comparison. |
| R12 / #0597 | V2 closes the tray on click, holds original placement until approval confirmation, then animates and preserves keyboard focus. Stale refusal and original-receipt recovery have native readback, including one Human approval comment. | Reuse the receipt tests. Earlier V2 timing evidence alone does not establish a matched V1/V2 timing comparison. |
| R13 / #0590 | Both versions approved the full lane with a partial search filter. V1 uses a single click. V2 recovered a truncated committed response by original receipt and verified one Human approval comment/activity on the focused card. | The cited paired browser run does not explicitly show custom historical approval text or stale membership refusal. Native lane approval tests cover custom text. |
| R14 / #0591 | Paired 19-card preview/apply mappings, stale refusal and successful inspection after a lost response. Completed previews retain full membership with older cards hidden. | Completed fixtures differ in counts. V1 did not open a preview under the observed search filter. Compare eligibility on equivalent fixtures before claiming filtered parity. |
| R15 / #0592 | Live V2 CLI/API batch, effective dependency floor, stale-board HTTP 412 and unchanged readback after refusal. Terry confirmed the CLI/API surface. | Retain a bounded native smoke/evidence reference for R20. The historical inventory's browser batch and keyboard-move suggestions were superseded by Terry's V1 observations. |
| R16 / #0598 | Corrected paired previews and cover, distinct comment/attachment badges, damaged-preview keyboard link, verified original bytes, detach feedback and lost cover/detach inspection. | The final matched captures cover focused assets and badges. Whole-board histories differ. The cited matched negative fixture is damaged raster data. Confirm a supported first-image fallback after cover clear if that path is used in R20. |
| R17 / #0600 | V1 picker/staging and paired paste/drawer crops. V2 created with two files, and a three-file partial batch recovered by original-ID inspection, exact reselection, explicit Retry and native hash readback. Exact final drawer transport proof covers filename and bytes. | Earlier create/partial runs precede the final journal, presentation, busy-reselection, spacing and transport changes. Corrected crop wraps with different generated clip UUIDs. Use the same filename for a strict wrapping comparison if needed. |
| R18 / #0593 | Paired retained draft through Bot edit, outage and reconnect, Human age/title cues, and V2 confirmed live-move replay and independent refresh races. | Terry approved pausing V2 submission until fresh state is confirmed. Same-second background attribution can need scoped activity alongside catalog data. Check recovery with the same draft and task as the integrated journey. |
| R19 / #0599 | Back/Forward/drawer and reload observations, cache default/settings, last-confirmed read-only state under transport failure and redaction after access refusal. Tests cover zero-budget eviction and principal isolation. | Pair the combined retained draft, filtered board and scroll state through recovery on the integrated build. Cache eviction evidence is focused automated coverage. |
| R20 / #0601 | Exact starting tree verified against the accepted T5 head. | Record exact-build paired entry/responsive states, one complete write journey, draft/reconnect recovery, authoritative native readback and strict/release checks in the qualification packet. |

## Retained surfaces and remaining gates

- The V1 observation ledger maps provisioning, parent maintenance, offline
  reads, full activity and notice administration to native workflows. It also
  maps backup/restore, close/reopen, publishing and service operations to retained
  V2 capabilities. The CLI/Bot guide documents the native routes and verbs.
  Full-gate results and the bounded native smoke supply current preservation
  evidence. [Observation mapping](0582-observed-contract.md#historical-inventory-mapping-and-remaining-observations), [CLI/Bot guide](../cli-workflows.md)
- Architect commit `ad32254` reconciled the previously stale browser workflow
  and Windows operation guidance. [Browser workflow guide](../browser-workflows.md)
- Earlier Architect decisions accept their scoped turn and evidence. R20
  failures need separately scoped defects and independent resolution before the
  epic handoff. Terry retains the final demo, cutover approval and merge to main.
  [Turn workflow](0581-pr-workflow.md)
