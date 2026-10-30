package main

const localDomainCLIHelp = `  localswim --data DIR [--json] board read
  localswim --data DIR [--json] card read CARD-UUID
  localswim --data DIR [--json] card history CARD-UUID
  localswim --data DIR [--json] card children CARD-UUID [--page N]
  localswim --data DIR [--json] card set-parent|clear-parent CARD-UUID --input FILE
  localswim --data DIR [--json] card create --input FILE
  localswim --data DIR [--json] card edit CARD-UUID --input FILE
  localswim --data DIR [--json] card archive|unarchive CARD-UUID --input FILE
  localswim --data DIR [--json] card list [--archive active|archived|all] [--cursor TOKEN]
  localswim --data DIR [--json] card search [--query TEXT] [--lane LANE] [--owner unassigned|UUID] [--priority Pn] [--archive active|archived|all] [--cursor TOKEN]
  localswim --data DIR [--json] comment add CARD-UUID --input FILE
  localswim --data DIR [--json] operation read OP-UUID --evidence FILE
  localswim --data DIR [--json] storage inspect --board-id BOARD-UUID --incarnation INC-UUID
  localswim --data DIR [--json] storage upgrade --from-version 5 --to-version 6 --board-id BOARD-UUID --incarnation INC-UUID
Use canonical card UUIDs from board read, never display ticket numbers.
Archive aliases require the full card.edit request with only input.archived true/false.
List returns at most 20 cards and an explicit next cursor when more remain.
--input - reads a typed mutation envelope from stdin. Rename uses card edit.
Omitted edit fields retain values; ownerId:null clears owner. CLI cannot approve.
Parent aliases require exactly input.parent CardRef/null; create may include parent.
Children uses a complete snapshot: readable20rows/page, full JSON rejects --page.
Storage inspect/upgrade are offline existing-only operations, never startup migration.
Physical6 retains base-schema marker5; uncertain upgrade needs deliberate inspection.
Original lookup is deliberate and source-bound; never replay an uncertain save.
Exits: 0 confirmed; 1 invocation/IO; 2 refused or definite noncommit;
       3 unavailable before dispatch; 4 original outcome unknown.
See docs/runbooks/m02-cli-read-edit.txt for envelope and recovery examples.`
