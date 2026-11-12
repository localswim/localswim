# Import a V1 board into V2

1. Pause edits to the V1 board. Verify it with the V1 CLI and export the complete
   snapshot, including archived cards. The importer accepts board schema 4 or 6
   with embedded policy schema 6 or 7. It also validates V2 constraints such
   as allowed moves into Completed, approval comment length, and actor accents.
   Incompatible inputs are refused before the board is admitted. A selected
   cover must be previewable from its verified bytes in V2.

   Keep the original export and board directory with its attachment tree as a
   separate archive. The export retains V1 policy notes, edge descriptions,
   and audit history. Keep any detached blobs still present in the attachment
   tree with that archive.

   ```powershell
   $source = 'C:\Boards\MyProject\board.json'
   $export = 'C:\Cutover\my-project-v1.json'
   uv run --project C:\Projects\localswim --frozen --offline localswim-cli $source board verify
   if ($LASTEXITCODE -ne 0) { throw 'V1 board verification failed.' }
   uv run --project C:\Projects\localswim --frozen --offline localswim-cli $source board show --json |
     Set-Content -LiteralPath $export -Encoding utf8NoBOM
   if ($LASTEXITCODE -ne 0) { throw 'V1 board export failed.' }
   ```

   The export example uses PowerShell 7. Use a new path in an existing private
   directory for `$export`.
   Legacy timestamps without numeric offsets use the importing machine's local
   timezone. Run the import in the source timezone for these records. Use an
   explicit offset for times in a repeated daylight-saving hour.

2. Stop the V2 writer for the destination data directory before importing.
   This is an offline command. A successful import admits one complete fresh
   board. Replay assigns tickets in dependency order, using source ticket
   order within each group of cards whose blocking prerequisites are ready.

   ```powershell
   $data = 'C:\LocalSwim\v2-data'
   $attachments = Join-Path (Split-Path -Parent $source) attachments
   $board = [guid]::NewGuid().ToString()
   $mapping = .\localswim.exe board import-v1 --id $board $data $export $attachments
   if ($LASTEXITCODE -ne 0) { throw "V2 import failed. Check board $board before retrying." }
   $mapping | Set-Content -LiteralPath 'C:\Cutover\my-project-v2-mapping.json' -Encoding utf8NoBOM
   ```

   The optional `--id` supplies the new board's UUIDv4 so you can find it after
   an uncertain result. For file input, omit the attachment directory to use
   `attachments` beside the input file. For stdin, use `-` in place of the JSON
   file and supply the attachment directory if the snapshot references active
   attachment bytes.

3. Run `board verify --data` to check the saved V2 snapshot and audit. Use it
   with the explicit UUID before retrying after an uncertain import result.

   ```powershell
   .\localswim.exe board verify --data $data $board
   if ($LASTEXITCODE -ne 0) { throw 'Saved V2 board verification failed.' }
   ```

   Check cards and relationships against the source. The JSON mapping reports
   `board_id`, `revision`, and `cards`. Each card pairs `old_ticket` and
   `old_card_id` with `new_ticket` and `new_card_id`.
   `detached_attachments_omitted` counts historical detached attachments recorded
   in the V1 export and omitted from the replay.
   `completed_dates_defaulted` counts completed cards that use the import time
   because the source has no completion date.
   `policy_annotations_omitted` counts source policy notes and transition
   descriptions retained in the original export.

4. Start the V2 service and configure `LOCALSWIM_URL` and `LOCALSWIM_TOKEN`
   before verifying attachment bytes. Look up the imported attachment UUID
   with `attachment list BOARD_ID NEW_TICKET` and put it in `$attachmentId`.
   Download to a new file. The CLI checks the byte count and SHA-2-256 digest
   against V2 metadata. Compare the printed digest with the `sha256` value in
   the original V1 attachment metadata before directing users to V2.

   ```powershell
   $download = 'C:\Cutover\verified-attachment.bin'
   .\localswim.exe attachment download $board $attachmentId $download
   if ($LASTEXITCODE -ne 0) { throw 'V2 attachment download failed.' }
   (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash.ToLowerInvariant()
   ```
