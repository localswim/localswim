B02 links, handoff and approval

New receives the caller's real grants.Service. Callbacks binds an independently
resolved board/incarnation/owner-card route for S02 Engine.Submit. G01 verifies
the live credential, actor kind and current grant rights in the serialized writer
snapshot. Supplied actors, assignments and earlier authorization checks grant no
authority. Production composition supplies G02's real verifier.

Scope: link.add, link.remove, review-handoff and approve. A link's route card is
its owner/dependent; the payload targetCardId is a prerequisite or related peer
in the same board incarnation. No cross-board authority or link creation follows
from a UUID match. Add rejects self-links and new active blocking cycles. Active
typed pairs are unique. Informational and inactive links never gate start; B01
continues to check direct blocking prerequisites when a card starts.

An active duplicate typed pair refuses with LINK_ALREADY_EXISTS. Removing an
inactive or noncurrent association refuses with NOT_FOUND. These terminal
refusals change no domain state, references, approval, history or activity; an
exact original-operation replay resolves its recorded outcome through S02.

Removal requires an existing current link belonging to the route owner. It
deactivates the immutable link identity and removes its current reference; prior full
snapshots and committed result references retain that identity. Adding a pair
after removal allocates a fresh link, preserving the old link's provenance.

Handoff only moves In Progress to Ready for Review and requires the
review-handoff right. It creates no approval or approve right. Approval only
moves Ready for Review to Completed, requiring a current human actor,
approve right and the current policy version. The approval fact binds the exact
pre-approval board/card revisions; currentApproval points to that new fact while
the card revision advances once. Other card mutations clear currentApproval and
retain historical facts. Completed refuses all further mutation in this slice.
These transitions preserve the archived flag; an archived Completed card still
cannot satisfy a start prerequisite. There is no reopen, archive, assignment or
priority propagation command here.

Every successful command advances only the owner card once, and creates one
board revision, attributable activity event and retained committed receipt. B01's
shared snapshot provider preserves the complete prior scalar state, references,
approval and byte pins. Its retained COMMITTED metadata supplies the original
revision's origin, actor and time, including compact restored-source provenance.
That provenance never supplies current authority. Missing provenance fails closed.
S02 owns exact preconditions, atomic commit, rollback and terminal refusal fences.

The Windows tests use fresh private synthetic S01/S02/G01 stores and B01 commands.
They require S01_TEST_ROOT outside the public repository and use a test-only live
snapshot verifier. They do not qualify production credentials, browser/HTTP/CLI
composition, restore execution or native power-loss durability. No issuer trust,
real board or actual grant action is involved.
