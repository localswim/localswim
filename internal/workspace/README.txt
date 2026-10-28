M01.03 explicit workspace registry and lifecycle

Start owns a private workspace root before binding the fixed numeric-loopback
port. The first start requires an explicit port and generates a workspace UUID;
later starts reuse both and refuse conflicting ports or live owners. Board data
and durable identities remain independent. No scan, import or default board exists.

workspace.json is strict schema 1, at most 1 MiB, 256 members and 256 projects. Projects have
immutable keys, labels, canonical local roots and physical identity keys. Members
bind project/board keys to a strict descendant data root and full durable tuple.
Byte-exact native CAS plus expected config revision guards every control change.
Physical aliases, duplicate BoardID, key rebinding and replacement identities
refuse. No database or board identity schema changes are introduced.

The authenticated workspace channel exposes projects, member list and key resolve.
Board identities/epochs require the selected member's currently configured human
or automation read grant; otherwise the ready projection is opaque unavailable.
Private paths and physical keys occur only in private control requests/config.
Project creation, board create/register and focused status are automation-only.
Catalog projection and its shared bounded scheduler are implemented in catalog.go.
Current authorized snapshots cover all joining polls; failed probes disclose no
prior board facts. Preferences remain a subsequent package.

Initialization and registration are separate durable steps. Any ambiguous config
publication returns UNKNOWN and retains initialized bytes. A deliberate status
read compares registry/path/tuple; an explicit register may publish the existing
identity. No automatic reinitialization, adoption, deletion or POST replay occurs.

Activation is coalesced per member with four active slots and a one-second attempt
context, separate from the attached engine lifetime. Missing, busy and replaced
members remain visible independently of healthy siblings. This does not bound
arbitrary native disk/driver execution or claim joined product qualification.

Close stops admission, drains host requests/engines, removes only the owned epoch
rendezvous and releases native ownership last. Unresolved handles/cleanup
capabilities remain reachable for retry. Start can return a nonnil Runtime with
an error only when failed-start cleanup retains ownership; its caller must retain
and retry Close. Done closes only after full registry cleanup. A completed host
shutdown diagnostic may remain an error even after Done confirms release.

ReadClient is a bounded private rendezvous reader, not a health check or board
credential source. Consumers must verify the live workspace UUID/epoch before
control dispatch and the exact board session before domain dispatch.

Synthetic tests cover actual Windows ownership and loopback HTTP, strict config,
CAS refusal, uncertain publication and explicit status/register, read-grant
revocation in both channels, fixed-port reopen and independent missing/busy/
replacement status. Offline SQL right removal is labeled technical test setup;
it is not a product seed path. No native power-loss certification is claimed.
