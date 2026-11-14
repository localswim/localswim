# Epic #0581 current rolling assignments, revision 10

The Architect accepted T3 in PR #3 and moved #0585–#0593 to Ready for Review. T4 opened on commit `e17790b`. All six T4 cards entered In Progress at revisions 6077–6082.

Each assignment uses its named epic #0581 story. Sol Medium is GPT-6.1 Sol at medium reasoning, Sol High is GPT-6.1 Sol at high reasoning, and Terra Medium is GPT-6.1 Terra at medium reasoning.

## Remaining turn 1, original T4

| Card | Authors | Fresh reviewer | Prerequisites | Readiness |
| --- | --- | --- | --- | --- |
| #0594 / R07 | Sol Medium 0594 Text<br>Sol Medium 0594 Actions | Sol High 0594 Create Review | R06 | In Progress. Paired fields and editor actions verified, with retained draft on Chromium's retried 412. Architect review of archive and restore entry evidence remains. |
| #0595 / R08 | Sol Medium 0595 Author | Sol High 0595 Review | R06 | In Progress. Unsent draft across boards and archived comment verified. Architect review remains. |
| #0596 / R10 | Sol Medium 0596 Author | Sol High 0596 Review | R06 | In Progress. Direction-change recovery, link follow, removal, and paired toast verified. Architect review remains. |
| #0597 / R12 | Sol Medium 0597 Author | Sol High 0597 Review | R06 | In Progress. Motion, keyboard focus, and stale refusal verified. Architect review remains. |
| #0598 / R16 | Sol Medium 0598 Render<br>Sol Medium 0598 Data | Sol High 0598 Review | R06 | In Progress. Download, detach audit and toast, cover and detach recovery verified. Architect review remains. |
| #0599 / R19 | Sol Medium 0599 History<br>Sol High 0599 Cache | Sol High 0599 Review | R02, R03, R06 | In Progress. Retained read-only transport and cache preference verified. Architect review remains. |

T4 opened after the Architect squash-merged T3 as e17790b.
Visual reviewer Terra Medium T4 Visual Review compared earlier disposable captures. The UI Lead owns the remaining integrated checks. The Architect owns independent review of the candidate.

The UI Lead implemented the create receipt ownership correction. Sol High 0594
Create Review reviewed the race and regression tests. See the [T4 handoff](0581-t4-handoff.md)
for remaining qualification.

## Remaining turn 2, original T5

| Card | Authors | Fresh reviewer | Prerequisites | Readiness |
| --- | --- | --- | --- | --- |
| #0600 / R17 | Sol Medium 0600 Staging<br>Sol High 0600 Recovery | Sol High 0600 Review | R09, R16 | Ready for Work. R09 accepted. R16 is In Progress. |

T4 merge and Architect T5 release govern entry.
Visual reviewer Terra Medium T5 Visual Review.

## Remaining turn 3, original T6

| Card | Authors and evidence workers | Fresh reviewer | Readiness |
| --- | --- | --- | --- |
| #0601 / R20 | Sol High 0601 Qualification<br>Sol High 0601 Coverage<br>Sol High 0601 Release | Sol High 0601 Review | Ready for Work. T3 dependencies accepted. T4 and T5 acceptance and Architect release are pending. |

Visual reviewer Terra Medium T6 Visual Review. The UI Lead owns integration and PR submission. The Architect owns turn acceptance and release. Terry owns Completed and the final merge to main. Revision 9 is preserved in the JSON plan.
