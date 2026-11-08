package main

const localDomainCLIHelp = `  localswim --data NEWDIR [--json] board init --workflow FILE
  localswim --data DIR [--json] board inspect
  localswim --data DIR [--json] workflow show
  localswim --data DIR [--json] attention show
  localswim --data DIR [--json] workflow preview --input FILE
  localswim --data DIR [--json] workflow edit --input FILE
  localswim --data DIR [--json] board read
  localswim --data DIR [--json] activity read [--from UTC --through UTC | --cursor TOKEN]
  localswim --data DIR [--json] recipient status
  localswim --data DIR [--json] recipient configure --input PRIVATE.json
  localswim --data DIR [--json] notices configure|start|status|inbox|stop|recover|reload
  localswim --data DIR [--json] notices retarget --target-id UUID
  localswim --data DIR [--json] notices lookup --target-id ID --delivery-id ID
  localswim --data DIR notices text --target UUIDv4 --delivery DIGEST
  localswim --data DIR [--json] card read CARD-UUID
  localswim --data DIR [--json] card history CARD-UUID
  localswim --data DIR [--json] card children CARD-UUID [--page N]
  localswim --data DIR [--json] card relationships CARD-UUID [--page N]
  localswim --data DIR [--json] card relationships-replace CARD-UUID --input FILE
  localswim --data DIR [--json] card link-add|link-remove CARD-UUID --input FILE
  localswim --data DIR [--json] card set-parent|clear-parent CARD-UUID --input FILE
  localswim --data DIR [--json] card create --input FILE
  localswim --data DIR [--json] card edit CARD-UUID --input FILE
  localswim --data DIR [--json] card move CARD-UUID --input FILE
  localswim --data DIR [--json] card archive|unarchive CARD-UUID --input FILE
  localswim --data DIR [--json] card list [--archive active|archived|all] [--cursor TOKEN]
  localswim --data DIR [--json] card search [--query TEXT] [--lane LANE] [--owner unassigned|UUID] [--priority Pn] [--archive active|archived|all] [--cursor TOKEN]
  localswim --data DIR [--json] comment add CARD-UUID --input FILE
  localswim --data DIR [--json] operation read OP-UUID --evidence FILE
  localswim --data DIR [--json] storage inspect --board-id BOARD-UUID --incarnation INC-UUID
  localswim --data DIR [--json] storage upgrade --from-version 5 --to-version 6 --board-id BOARD-UUID --incarnation INC-UUID
  localswim --data DIR [--json] storage upgrade --from-version 6 --to-version 7 --board-id BOARD-UUID --incarnation INC-UUID
  localswim --data DIR [--json] storage upgrade --from-version 7 --to-version 8 --board-id BOARD-UUID --incarnation INC-UUID
  localswim --data DIR [--json] storage upgrade --from-version 8 --to-version 9 --board-id BOARD-UUID --incarnation INC-UUID
  localswim --data DIR [--json] storage upgrade --from-version 9 --to-version 10 --board-id BOARD-UUID --incarnation INC-UUID
  localswim --data DIR [--json] storage upgrade --from-version 10 --to-version 11 --board-id BOARD-UUID --incarnation INC-UUID
  localswim --data DIR [--json] storage upgrade --from-version 11 --to-version 12 --board-id BOARD-UUID --incarnation INC-UUID
Use canonical card UUIDs from board read, never display ticket numbers.
Archive aliases require the full card.edit request with only input.archived true/false.
Move requires the full card.edit request with only input.laneId (an exact current lane ID).
Board init accepts a complete configured revision1 workflow for a new board.
Workflow preview/edit accept complete replacement candidates for an existing board.
Preview requires exact board/policy revisions; edit submits the same candidate.
List returns at most 20 cards and an explicit next cursor when more remain.
Activity: start with activity read; copy Next cursor to activity read --cursor TOKEN.
Start fresh with activity read without the cursor. Gapped or expired coverage is incomplete evidence.
Explicit activity bounds use UTC whole seconds, e.g. 2026-11-06T12:00:00Z.
--input - reads the command's typed request from stdin. Rename uses card edit.
Omitted edit fields retain values; ownerId:null clears owner. CLI cannot approve.
Set or clear a cover with card edit and only input.coverAttachmentId (attachment UUID or null).
Parent aliases require exactly input.parent CardRef/null; create may include parent.
Children uses a complete snapshot: readable20rows/page, full JSON rejects --page.
Relationships uses a complete snapshot: readable20rows/page, full JSON rejects --page.
Replace owned relationships requires card.edit with only input.relationships array.
Incoming facts retain their actual owner; open that owner to deliberately edit them.
Storage inspect/upgrade are offline existing-only operations, never startup migration.
Physical12 retains base-schema marker5; older stores require separate adjacent steps.
An uncertain upgrade needs deliberate inspection, never a skipped-version attempt.
Original lookup is deliberate and source-bound; never replay an uncertain save.
Exits: 0 confirmed; 1 invocation/IO; 2 refused or definite noncommit;
       3 unavailable before dispatch; 4 original outcome unknown.
See docs/runbooks/m02-cli-read-edit.txt for envelope and recovery examples.
See docs/runbooks/m11-activity-cli.txt for activity examples and
docs/runbooks/m12-notices-cli.txt for notice controls and
docs/runbooks/m13-notice-recovery-cli.txt for interrupted notice recovery.`
