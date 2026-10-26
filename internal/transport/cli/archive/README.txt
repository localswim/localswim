CLI attachment, export and offline restore composition

Runner.Run owns these command bindings, without executable registration:
  attachment add C --input FILE --file PATH
  attachment download A --destination NEW
  export create --destination NEW.zip
  restore create --source ZIP --destination NEWDIR
  restore status --destination NEWDIR --request-id U

Ordinary commands require the shared prefix, with every selector exactly once:
  [--json] --installation ID --board B --incarnation I --grant G
Offline restore/status accept only optional prefix --json. Unknown, duplicate,
missing, contradictory or misplaced arguments refuse before dispatch. Input -
reads bounded JSON stdin. Paths are local-only and never enter public envelopes.

The explicitly injected core.Provider must supply the exact current protected
credential. The shared core.Open performs pinned TLS and current session binding
before board dispatch. There is no default provider, fallback credential, actor
flag, alternate authority, HTTP downgrade, retry or replacement operation ID.
Composition must supply the protected live/source tree inventory; nil fails
closed. Offline commands require no board credential or implicit bootstrap.

Attachment source files are bounded native reads with ancestor pins, reparse and
hardlink exclusion. The byte buffer is complete before dispatch. Core's shared
DispatchAttachment validates the supplied normal C01/C02 metadata against raw
SHA-256 and length, freezes the authenticated actor/route/intent, then saves and
reads back K02 immutable private evidence before constructing the exact two-part
A01 multipart request. The source path, framing and credentials never enter the
fingerprint. A changed source file cannot alter the captured upload bytes.
Lost responses remain unknown; only an explicit original operation lookup through
K02 can reconcile them. No attachment re-upload follows an uncertain response.

Download observes one authenticated current BoardRead for the selected B/I and
finds the requested immutable attachment metadata there. Exact authorized raw
bytes, forced-download headers, complete length and SHA-256 must agree before
any local candidate is written. JSON success is that existing BoardRead only;
its body is observed metadata, not a new delivery receipt. Exit 0 in this command
context additionally requires verified local publication. Bytes are never dumped
to stdout, rendered inline or treated as executable media.

Export keeps the accepted --destination grammar. One authenticated current board
read freezes exact B/I/revision; one H02 POST carries exactly incarnation,
expectedRevision and input={}. A revision race refuses without another read,
revision adoption or export retry. The entire bounded stored ZIP must pass A03,
including manifest, inventory, protected bytes and COMPLETE, and its source and
revision must match that frozen observation before local publication. Confirmed
JSON uses the shared ExportStatus with destination=null. Offered browser delivery
never satisfies this native verification.

Native local saves require canonical absolute paths on local fixed NTFS. They
reject aliases, junction/symlink traversal, overlap and existing targets. Ancestor
handles stay pinned. A fresh sibling partial is exclusively created with the same
owner+SYSTEM protected-DACL policy as K02/A04, flushed and exactly read back.
The exclusive byte handle stays open through no-overwrite write-through rename,
then exact bytes, normalized final name, single-link identity and private ACLs
are verified again. Incomplete candidates are retained without a verified-save
claim; publication ambiguity returns unknown. No replacement or cleanup destroys
an existing file. Later stdout failure returns exit 4 while the completed file
or receipt remains complete; it never triggers publication or network replay.

Restore delegates directly to A04 publication and status. Create allocates a fresh
request UUID and must first deliver RESTORE_REQUEST <U> on stderr. Failure to
deliver that reference prevents native construction/publication. It is a request
reference, never receipt or commit proof. A04 validates A03 completely, preserves
the source, constructs a separate fresh incarnation and publishes exclusively.
Status uses the exact final/partial request locations and shared read-only native
physical-v4 inspection. Confirmed/failed/unknown use the standalone D02 status
and exits 0/1/4; ambiguous, missing or damaged candidates never become restored.
No activation, authority transfer, grant issuance or implicit human bootstrap is
performed. Fresh destination human bootstrap remains a later explicit action.

JSON stdout contains one shared public envelope plus its final LF. Diagnostics
are inert categories and the restore request UUID; no private path or received
body is echoed. Human output labels the selected board and outcome. A current
read or export refusal cannot prove an attachment mutation did not commit.

Tests use only synthetic sources and private fixtures. The k03_archive_native
suite requires a newly generated root-only N01 overlay and compiled sentinel;
missing overlay fails compilation. It composes actual pinned TLS, K02 evidence,
A01/H02, A03 validation, native file publication and A04 restore/status, with
denial, stale state, response loss, byte/ACL tamper, alias, interruption and output
failure cases. Native flush/readback and process interruption do not prove
power-loss durability. P01/P02 executable and script registration remain separate.
