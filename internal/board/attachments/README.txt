Attachment byte authority

This package implements only attachment.add and an authorized raw read. Existing
attachment associations remain immutable. It provides no detach, replacement,
archive, export, restore, CLI or UI operation.

Buffer owns at most one 10 MiB raw upload plus its one-byte oversize sentinel.
Its storage is inaccessible to consumers; Add verifies that exact buffer's length
and SHA-256 before writer checkout. A bad raw upload is a nonterminal refusal.
Read cursors expose only Read, without a promoted WriterTo path that could pass
the private backing slice to a destination Writer; cursors share no position.
Source reads use a 4 KiB scratch buffer copied into final storage, so even a
source retaining its destination cannot retain the upload's private allocation.
The immutable C02 intent contains semantic metadata, never multipart framing,
part filenames, credentials or headers.

Add composes current G01 attach authority, S02 identity/CAS/terminal processing,
the B01 snapshot facade and the shared S03 pin registry. The registry guard is
acquired inside S02 admission after the writer slot, retained through COMMIT
cleanup and readback, and omitted for matching retained receipts. CheckQuotaWith
adds pending byte facts to the same distinct current/full-history/full-operation/
active-export union, including zero-byte facts, with inclusive 10/64 MiB limits.

The transaction preserves the complete prior snapshot, original provenance and
prior references/pins, clears approval, advances the owner card revision, creates
the immutable attachment/entity/current reference/current pin and activity, and
lets S02 co-commit its board revision, typed result and full-operation byte pin.
Existing digest reuse compares both lengths and SQLite's complete BLOB equality.
Integrity disagreement fences store writes and export snapshot acquisition; it
never overwrites or deletes evidence to manufacture success.

Read checks current ordinary read authority and exact credential board,
incarnation and actor in one actual S01 reader snapshot. Attach authority and
native ownership do not imply read authority. The complete BLOB length and hash
must match immutable attachment metadata before any download is returned.
Affected damaged reads fail closed. Transport delivery interruption does not
establish a verified local save or a successful retry.

Tests use synthetic private stores and explicit S01_TEST_ROOT. They never invoke
the native installation root, bind the native HTTPS port or use real board data.
The transport package's separate a01_native qualification requires a private
root-only overlay with a compiled sentinel; ordinary go test cannot reach it.
