B01 card commands

New receives the caller's real grants.Service; Callbacks captures a separately
resolved board/incarnation/card route. Use these callbacks with S02 Engine.Submit.
No principal, actor claim, rights cache or fallback verifier grants authority.
G01 performs live credential and current grant checks in the writer snapshot.

Scope: card.create, card.edit, comment.add, and the fixed Ready for Work to
In Progress transition. C01 freezes and validates input. S02 owns exact board/card
preconditions, terminal refusal persistence, atomic commit and reconciliation.
Creation allocates a UUID and a never-reused ticket, starts at revision 1 and
Ready for Work, and defaults priority to P3. Comments preserve immutable text and
credential-bound attribution. Completed refuses edits and comments.

An empty edit or exactly identical supplied values records durable NOT_COMMITTED
with code NO_CHANGE, preserving revisions, history, activity, approval and counters.
Comparison preserves exact Unicode and values, with no trimming or normalization.
An already-current In Progress lane is redundant; with another real change the
edit commits normally. Only an actual start transition checks prerequisites.

Every existing-card mutation preserves the complete prior scalar state, current
references, approval reference and attachment pins, then clears current approval.
Historical approval/entity facts remain untouched. Snapshot origin, actor and time
come from unique retained COMMITTED operation metadata and its exact card result
reference. Compact restored-source facts supply provenance only, never current
authority. Missing or ambiguous provenance fails closed through S02 rollback.

Start checks direct active blocking links against nonarchived Completed cards with
current human approval of the immediately preceding revision and current policy.
Informational and inactive links do not gate start. B02 owns graph mutations,
handoff and approval; this package adds no archive, reopen or assignee command.

Native Windows tests require an explicit private S01_TEST_ROOT outside the public
repository. Fixtures create fresh synthetic SQLite stores with private ACLs and
use the real S01/S02/G01 providers with an explicitly test-only snapshot verifier.
They do not establish native power-loss, browser, transport or production service
qualification. Production composition must supply G02's real verifier.

Assignment
ownerId optionally selects an existing actor in the current board incarnation.
Create omission/null is unassigned. Edit omission retains ownership; null clears
it. Membership, stale revision, current rights and no-change checks run inside
the current writer transaction. Assignment shares normal revision/history and
approval invalidation and never changes authenticated actor, rights or workflow.
