# UI minimal acceptance test

Drafted with Terry during the final UI walkthrough. Add a check only when its
workflow and expected result are agreed; this is not the full regression suite.

## Setup

- Run the reviewed candidate on a separate loopback port with fresh disposable
  data. Create and pin `UI Demo Alpha` and `UI Demo Beta` using the Terry preset.
- Open the demo URL in Chrome. Record the commit, URL and viewport for each run.
- Bot uses supported Chrome controls, observes each result, and records failures
  instead of marking them passed. Use the shared diagnostics queue and native
  CLI readback where a later check requires evidence beyond the visible UI.
- Keep production data untouched. Terry retains final release approval.

## Operation matrix

Availability describes the final candidate and does not mark a test passed.
`Yes` means the interface can perform the named operation. Parenthetical text
limits the scope or describes the interface-specific action.

| Operation group | Operation | In CLI? | In Web UI? |
| --- | --- | --- | --- |
| Board lifecycle | Create a board | Yes | No |
| Board lifecycle | List/find boards and inspect a board | Yes | Yes |
| Board lifecycle | Open a board by URL | No | Yes |
| Board lifecycle | Rename a board | Yes | No |
| Board lifecycle | Change board configuration | Yes | No |
| Board lifecycle | Export a board | Yes | No |
| Board lifecycle | Restore a board export | Yes | No |
| Board lifecycle | Close/reopen a board | Yes | No |
| Board lifecycle | Verify/recover saved board data | Yes | No |
| Workspace | Pin/unpin/reorder boards | Yes | Yes |
| Workspace | Resize/collapse the sidebar | No | Yes |
| Workspace | Adjust the retained-board cache | No | Yes |
| Lanes | Inspect lanes and allowed transitions | Yes | Yes (displayed lanes and permission captions) |
| Lanes | Add/rename/retire a lane | Yes | No |
| Lanes | Change lane key or approval-button configuration | Yes | No |
| Lanes | Grant additional creation/movement permissions | Yes | No |
| Cards | Create a card with title, description, owner and priority | Yes | Yes |
| Cards | Cancel an unsaved card | No | Yes |
| Cards | List/find/inspect cards | Yes | Yes |
| Cards | Open/close a card drawer or direct card URL | No | Yes |
| Cards | Edit title/description | Yes | Yes |
| Cards | Change owner | Yes | Yes |
| Cards | Set individual priority | Yes | Yes |
| Cards | Set priority for multiple cards, a lane or a board | Yes | No |
| Cards | Move a card through allowed lanes | Yes | Yes (drag/drop) |
| Cards | Archive/unarchive a card | Yes | No |
| Cards | Inspect archived cards | Yes | Yes (direct or linked card) |
| Cards | Find/read Completed cards | Yes | Yes |
| Cards | Show/hide older Completed cards in the board view | No | Yes |
| Relationships | Create links, including during card creation | Yes | Yes |
| Relationships | Inspect/follow links and inverse relationships | Yes (inspect) | Yes (inspect/follow) |
| Relationships | Change link kind/direction or remove a link | Yes | Yes |
| Hierarchy | Set/clear a parent | Yes | No |
| Hierarchy | Inspect parent/children and follow their cards | Yes (inspect) | Yes (inspect/follow) |
| Comments | Read/post multiline comments | Yes | Yes |
| Attachments | Upload files to an existing card | Yes | Yes |
| Attachments | Stage files while creating a card | No (upload after creation) | Yes |
| Attachments | Paste an image | No (file or stdin upload) | Yes |
| Attachments | List/inspect attachment metadata | Yes | Yes |
| Attachments | Preview images | No | Yes |
| Attachments | Download original bytes | Yes | Yes |
| Attachments | Select/clear cover and display first-image fallback | Yes (select/clear) | Yes |
| Attachments | Detach an attachment | Yes | Yes |
| Review | Approve one card or a reviewed lane | No | Yes |
| Ordering | Preview/apply dependency ticket order | Yes | Yes |
| Activity | Inspect card history | Yes | Yes |
| Activity | Filter board-wide audit history | Yes | No |
| Live state | Observe another writer's changes | Yes (query current state) | Yes (live updates) |
| Recovery | Handle stale writes and uncertain results | Yes (revision guards and explicit inspection) | Yes (draft retention and recovery controls) |
| Persistence | Verify saved state after reload/restart | Yes (read back) | Yes (reload/reconnect) |

The test run exercises each available interface. Rejected transitions, immutable
Completed fields, exact text/bytes and duplicate prevention are expected outcomes
within the corresponding operation checks.

## Walkthrough checks

| ID | Action | Expected result | Result |
| --- | --- | --- | --- |
| MAT-01 | Open the demo. Switch between Alpha and Beta, return to Alpha, and refresh. | Correct board and lanes appear, both pins remain, and the connection returns to `CONNECTED` without errors. Each board retains the same GUID in its URL across repeated selections. | Passed by Terry, 2026-11-14. |
| MAT-02 | Create a card. Edit its title and multiline description, change owner and priority, and post a comment. Refresh. Start another edit and cancel it. | Saved values and comment persist exactly. Cancel preserves the previous saved value. | Passed by Terry, 2026-11-14; reported changes persisted, including a lane change. |
| MAT-03 | Move the card through its allowed workflow into RFR, then approve it as Terry. | The card reaches Completed with the configured approval comment. Bot cannot perform Terry's reserved approval. | Pending. |
| MAT-04 | Add a blocking relationship between two cards, inspect both ends, follow the link, then remove it. | Both cards show the correct directional labels, navigation opens the right card, and removal clears the link. | Pending. |
| MAT-05 | Upload an image, preview it, select it as cover, download it, then detach it. Also paste an image. | Original downloaded bytes match, cover and preview display correctly, and removal updates the card. Paste creates one attachment. | Pending. |
| MAT-06 | Find a card by title and comment text, open the result, clear search, and toggle older Completed cards. | Results and opened cards are correct. Clearing search restores the board. The toggle changes older Completed visibility. | Pending. |
| MAT-07 | Make a Bot CLI edit while Terry has an unsaved draft. Exercise a stale save and a brief disconnect. | Live changes appear, the draft survives, stale-save feedback is clear, and reconnect recovers without duplicate writes. | Pending. |

## Current walkthrough

- Candidate: `8e64fa9`, integrated as `123fdf0`.
- Demo: `http://127.0.0.1:8813`; runtime details are in the Architect's private
  `output/final-ui-demo-20261114/demo.json`.
- Initial load observed: Alpha, both pinned boards, empty lanes, `CONNECTED`.
- MAT-01 passed by Terry. He also verified that each board's URL GUID stays
  consistent every time he returns to it.
- MAT-02 passed by Terry in the Web UI. He reported that all changes appeared
  to persist, including a lane change.
- MAT-03 through MAT-07 await the walkthrough.
- MAT-02 CLI creation passed on 2026-11-14. Using the Bot credential, created
  Alpha ticket `0002`, `Bot CLI creation test`, in Backlog at revision 3.
  `card get` returned the same UUID, title and exact multiline description,
  including `café — 泳ぐ`. Card UUID `87352a88-e37c-4c76-8eec-2879b884782e`.
