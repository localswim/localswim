# V1 drop-in UI remit

Terry's clarification, 2026-11-13:

> I want V2 web UI to be a drop in replacement for V1, meaning look and act
> just like V1 did but with a freshly designed TS frontend with less tech debt
> because the end state/requirements are known 100% ahead of time.
> Look and work EXACTLY like this fully functional V1 site did.

V1's observable appearance and behavior define acceptance. Reproduce its layout,
styling, spacing, density, control placement, navigation, drawers, dialogs,
keyboard interactions, feedback and workflows in the new TypeScript frontend.
Use the V2 service and its existing API contracts.

The previous candidate passed technical and functional checks but failed Terry's
UI/UX review. Its RFR handoff did not establish visual acceptance. Epic #0514 is
back In Progress for correction. Prior technical evidence remains evidence only
for the behavior and candidate it actually exercised.

Compare the running implementations at matching data, viewport and interaction
state. Cover workspace entry as well as selected-board views. Evaluate a complete
user journey through its dialogs, keyboard actions, pending states and recovery.
Screenshots alone cannot establish behavioral equivalence.

A user-visible deviation requires Terry's explicit approval. The prior acceptance
of broad similarity or disclosure of visual differences is superseded by this
remit. Produce a concrete correction inventory from V1 before implementation,
then demonstrate the corrected interface early and verify it throughout the work.

Production cutover follows Terry's hands-on demos and explicit approval.
