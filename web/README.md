# Turn 1 browser API client

`api.mjs` is a dependency-free ES module for the same-origin V2 API. The embedding
page supplies a trusted `getCapability` callback for the current browser capability.
The callback is called per request. Production browser session setup arrives in M04.

```js
import { createBrowserApi, readWorkspaceOverview } from "./api.mjs";

const api = createBrowserApi({ getCapability: currentBrowserCapability });
const { workspace, boards } = await readWorkspaceOverview(api);
```

JSON payload fields and query names use `snake_case`. The JavaScript adapter
keeps its existing language-convention method, argument, and error property names.

Each successful result exposes the JSON object as `data` and the server's ETag as
`etag`. Pass the workspace ETag back to `setPinnedBoards(boardIds, etag)`. A
`412` calls for a fresh workspace read and a user decision on the original change.
Use the board ETag for `configureOwnerNotices(boardId, settings, etag)`. Pass only the
notice settings to change. An explicit `target_thread_id: null` clears the target.

When `outcomeUnknown` is true, inspect authoritative state before another write.

Run the focused checks with `node --test web/api.test.mjs`.
