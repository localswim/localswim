package main

const localDomainCLIHelp = `  localswim --data DIR [--json] board read
  localswim --data DIR [--json] card read CARD-UUID
  localswim --data DIR [--json] card history CARD-UUID
  localswim --data DIR [--json] card create --input FILE
  localswim --data DIR [--json] card edit CARD-UUID --input FILE
  localswim --data DIR [--json] comment add CARD-UUID --input FILE
  localswim --data DIR [--json] operation read OP-UUID --evidence FILE
Use canonical card UUIDs from board read, never display ticket numbers.
--input - reads a typed mutation envelope from stdin. Rename uses card edit.
Omitted edit fields retain values; ownerId:null clears owner. CLI cannot approve.
Original lookup is deliberate and source-bound; never replay an uncertain save.
Exits: 0 confirmed; 1 invocation/IO; 2 refused or definite noncommit;
       3 unavailable before dispatch; 4 original outcome unknown.
See docs/runbooks/m02-cli-read-edit.txt for envelope and recovery examples.`
