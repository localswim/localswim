# Relationship direction and card labels

The API stores one relationship between two cards. `source_card_id`, `kind` and
`target_card_id` form a sentence. For example, A `blocks` B. The same relationship
appears as **Blocks B** on A and **Blocked by A** on B. Both views refer to the
same relationship ID and revision.

## Canonical kinds and presentation

| API `kind` | Viewed from source A | Viewed from target B |
| --- | --- | --- |
| `blocks` | Blocks B | Blocked by A |
| `duplicates` | Duplicates B | Duplicated by A |
| `references` | References B | Referenced by A |
| `relates_to` | Relates to B | Relates to A |

The first three kinds are directional. `relates_to` is symmetric, and reversing
its endpoints describes the same link for duplicate detection. UI choices such
as “Blocked by” are perspective labels. Requests use the four canonical kinds
above. Input accepts ASCII mixed case for those kinds and mixed-case UUIDs;
responses use lowercase canonical values. `blocked`, `blocked_by`,
`duplicated_by` and `referenced_by` are not accepted kind values.

## Linking existing cards

`POST /api/2026-11-10/relationships` takes the board identity, its current
`expected_board_etag`, and explicit source, target and kind. To make A blocked by
B, send B as `source_card_id`, A as `target_card_id`, and `blocks` as `kind`.
Both cards must belong to the same board. A card cannot link to itself.

`GET /api/2026-11-10/relationships?board_id=BOARD_ID&card_id=CARD_ID` returns
links touching either endpoint, preserving their stored source and target.
The focused card-inspection representation also supplies `direction` relative
to the selected card: `outgoing`, `incoming`, or `symmetric`. Use that direction
with the table above when choosing the label.

`PATCH /relationships/{relationship_id}` changes the canonical kind and can
reverse direction while retaining the relationship ID. Omit both endpoint
fields to keep their current order. To set the order explicitly, supply both
`source_card_id` and `target_card_id`; they must identify the same existing pair
of cards, in either order. Supplying only one endpoint is invalid.

For example, if A currently references B and A's UI selects **Referenced by B**,
PATCH that relationship with `kind: "references"`, source B and target A.
Include the current `expected_board_etag` and relationship HTTP precondition.
The direction change, revisions, audit and applicable dependency effects commit
atomically. A conflict with another resulting relationship refuses the change.
Reversing an unchanged `relates_to` link is a semantic no-op and retains its
stored order. Removing a relationship uses its ID and removes the one shared
link from both cards' views.

## Seeding links while creating a card

`POST /api/2026-11-10/cards` accepts a `relationships` array. Creation, all seeded
links, applicable priority-floor changes and audit effects commit atomically.
An invalid seed refuses the entire creation. The actor needs both the applicable
card-creation permission and `manage_relationships`.

Each seed has `kind` and `target_card_id`. An omitted `source_card_id` defaults
to the new card. Every seed with an explicit source requires `id` in the outer
card request. An explicit source enables incoming links. Exactly one endpoint
must be the new card; the other must already exist on the same board.

For an incoming link, supply the new card's UUID in the outer card request's `id`
and use that same UUID as the seed's target. This example uses new card N with ID
`11111111-1111-4111-8111-111111111111` and existing card E with ID
`22222222-2222-4222-8222-222222222222`.

**N blocks E**, using the concise outgoing form:

```json
{
  "kind": "blocks",
  "target_card_id": "22222222-2222-4222-8222-222222222222"
}
```

**N is blocked by E**, using ordered endpoints:

```json
{
  "kind": "blocks",
  "source_card_id": "22222222-2222-4222-8222-222222222222",
  "target_card_id": "11111111-1111-4111-8111-111111111111"
}
```

**N relates to E**, a symmetric link:

```json
{
  "kind": "relates_to",
  "target_card_id": "22222222-2222-4222-8222-222222222222"
}
```

These are seed objects inside `relationships`, not complete card requests.
The outer request still supplies its required board, lane, title and current
board validator. The API assigns relationship IDs. The same rules apply to
`duplicates`/“Duplicated by” and `references`/“Referenced by”.

## Validation and recovery

- Reject self-links, seeds connecting two existing cards, missing/foreign-board
  endpoints and repeated links. Symmetric duplicates compare both endpoint orders.
- Blocking links affect dependency ordering and effective priority. Use the saved
  response/readback rather than assuming the requested priority is the result.
- A changed board validator refuses stale creation. After an uncertain response,
  inspect the original card UUID and its relationships before another submission.
  A missing response does not establish that the atomic operation failed.

The [OpenAPI contract](openapi.yaml) defines exact payloads, responses,
preconditions and headers. Keep this guide, that contract, implementation and
tests aligned whenever relationship behavior changes.
