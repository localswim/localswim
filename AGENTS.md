# Repository guidance

This is the shared Go/TypeScript localswim implementation monorepo. Follow the
accepted Spike 01 contracts and reviewed staged task decomposition in the public
v2-architecture repository; newer owner direction takes precedence.

- Keep authoritative domain, permissions, revisions and admission in Go. Browser
  feedback is advisory; generated boundary contracts must remain consistent.
- The Go module and web/ package are separate build boundaries in one repository.
  Ship only verified embedded assets. Do not introduce project-local board forks.
- Public MIT FOSS: UTF-8, LF, final newline; preserve binary bytes. Never commit
  real board data, runtime credentials, private rosters or recovery stores.
- Use synthetic fixtures. Do not copy v1 source/tests/internal designs or claim
  independently isolated clean-room certification; prior exposure is disclosed.
- Distinguish candidate availability, isolated qualification and product evidence.
  Do not install issuer trust or issue human grants without the required explicit
  owner action. Mocks and process termination do not prove native power-loss safety.
- Use the shared installed CLI for administrative boards; never edit their JSON
  directly. Move an eligible task In Progress before work or delegation. Scoped
  dependent work stays Ready for Work; dependencies and acceptance gates remain.
- Keep a task In Progress through implementation and fresh non-author review.
  Only the owner controls Completed. Review/approval alone belongs Ready for Review.
