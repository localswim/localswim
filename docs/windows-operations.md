# Run localswim V2 independently on Windows

RFC 2119 keywords; the capitals are load-bearing. A sentence without one
describes the procedure rather than stating a rule.

Build or install the Windows executable as described in the [README](../README.md).
Choose the installation's fixed loopback port and absolute native V2 data
directory. Once the launcher confirms startup, the service runs without a
console. It runs as the Windows user who launched it, for the current Windows
session. The launcher needs PowerShell 7.3 or newer.

## Launch credentials

`scripts/LocalswimCredential.ps1` defines the two functions that write and read
the launch credentials. Dot-source it; it defines functions only, and it works
in PowerShell 7 and Windows PowerShell 5.1.

- `New-LocalswimCredentialFolder -Root ROOT` creates a new folder under `ROOT`
  holding `human.json` and `bot.json`, with two fresh, distinct base64url tokens
  of 32 random bytes each, saved with Windows DPAPI. Only the same Windows user
  on the same PC can decrypt them. It returns the folder's path.
- `Read-LocalswimToken -CredentialDirectory FOLDER -Role human|bot` returns a
  role's token. It refuses any file the
  [receipt and credential interface](#the-service-receipt-and-credential-files)
  forbids, and never includes the token in an error.

Each cold start uses a new folder.

- Credential folders MUST be kept private, outside the checkout and the data
  root.
- Tokens MUST be loaded straight into process-scoped environment variables, and
  MUST NOT appear in terminal output, command arguments or shell history.
- A cold start of the installation the receipt names MUST create its credentials
  with `New-LocalswimCredentialFolder`, because its output is part of the
  interface.

## Launch

`scripts/Start-Localswim.ps1` cold-starts localswim. With no arguments it starts
the installation the [service receipt](#the-service-receipt-and-credential-files)
names, which is what the [sign-in task](#start-at-sign-in) runs:

```powershell
pwsh -NoProfile -File C:\Projects\localswim\scripts\Start-Localswim.ps1
```

It does the following, appending each step to
`%LOCALAPPDATA%\localswim-v2\logs\start.log`, with times in the laptop's time
zone:

1. It reads the executable, data directory and port from the receipt.
2. If localswim already answers on that port, it stops there and changes
   nothing. If something else holds the port, it fails.
3. It creates a new credential folder with `New-LocalswimCredentialFolder`.
4. It starts `localswim serve --background DATA PORT` with the Human and Bot
   tokens in process-scoped environment variables. If the service fails to
   start, it deletes the unused credential folder and fails. If the start is
   unconfirmed, the service may still be binding with those tokens, so it keeps
   the folder, logs its path and fails.
5. It replaces the receipt by renaming a complete file over it, so a reader
   never finds the receipt missing or partial. A reader holding the receipt
   open delays the rename; the script retries for up to about 5 seconds.
6. It deletes the retired credential folder the old receipt named, when that
   folder is under the credential root and belonged to this port. It logs any
   folder it keeps.

While a receipt exists, a run without `-Disposable` takes no paths, so it can
only restart that installation. The first launch of a new installation has no
receipt yet: pass its `-Executable`, `-DataDirectory` and `-Port`, and the
script writes the first receipt. A disposable or test launch MUST pass
`-Disposable` with its own `-Executable`, `-DataDirectory`, `-Port`,
`-CredentialRoot` and `-LogFile`, which the script requires. It then leaves the
receipt and every other credential folder alone.
`scripts/Test-StartLocalswim.ps1` exercises these cases with disposable
instances, and `check.ps1` runs it against a fresh scratch build.

A successful launch logs the loopback URL and service PID after the child opens
the data directory and binds the listener. Open that URL in your browser; it
provisions its own browser session. An unconfirmed launch logs the child PID
when available and the credential folder it kept. Inspect that process and the
port before another attempt: if the service came up, stop it with that folder's
Human token, as [Connect and stop](#connect-and-stop) shows with the folder in
place of the receipt's. To see startup errors of a child that exited before
binding, create a folder under a scratch root with
`New-LocalswimCredentialFolder`, load its tokens, and run
`localswim serve DATA PORT` in a terminal without `--background`.

If the receipt step fails after the service starts, the log names the new
credential folder. Stop the service with that folder's Human token, as above,
and run the script again.

## Start at sign-in

`scripts/Install-LocalswimLogonTask.ps1` registers the scheduled task
`localswim service`, which runs `Start-Localswim.ps1` each time this Windows user
signs in, with a PIN or any other method. Run it once from a PowerShell 7
terminal. It replaces any earlier copy of the task and starts it at once, which
does nothing while localswim is already running.

```powershell
pwsh -NoProfile -File C:\Projects\localswim\scripts\Install-LocalswimLogonTask.ps1
```

- The task runs at standard rights as an interactive principal, because the
  credentials are protected with this user's DPAPI key. It runs at normal
  priority, which the service inherits.
- It runs under `conhost.exe --headless`, so no window appears. That switch is
  undocumented but present in current Windows 11 builds.
- It has no time limit, ignores a second start while one runs, and runs on
  battery.
- Task Scheduler always reports the task's Last Run Result as 0, because
  `conhost.exe` exits 0 whatever the script does. `start.log` is the only record
  of a failure, so check it after a sign-in.
- Signing in after a shutdown or restart runs the task, including after a
  Fast Startup shutdown. Unlocking the screen or waking from sleep does not, and
  needs no launch, because the service keeps running. The task is not a
  supervisor: if the service exits during a session, run `Start-Localswim.ps1`
  by hand or sign in again.

Remove the task with
`Unregister-ScheduledTask -TaskName 'localswim service' -Confirm:$false`.

## Connect and stop

In a PowerShell terminal, load the receipt and the credential functions. Use the
Bot token for Bot CLI work. The Human credential authorizes service shutdown.

```powershell
. C:\Projects\localswim\scripts\LocalswimCredential.ps1
$receipt = Get-Content -Raw (Join-Path $env:LOCALAPPDATA 'localswim-v2\service.json') | ConvertFrom-Json
$exe = $receipt.executable
$env:LOCALSWIM_URL = $receipt.base
$env:LOCALSWIM_TOKEN = Read-LocalswimToken -CredentialDirectory $receipt.credential_directory -Role bot
& $exe board list

$env:LOCALSWIM_TOKEN = Read-LocalswimToken -CredentialDirectory $receipt.credential_directory -Role human
& $exe service status
& $exe service stop
```

Wait for the process and listener to exit. `service stop` drains admitted work
and flushes enabled publishers. If it refuses or the outcome is uncertain,
inspect `service status` and the affected publishing status before retrying.
Clear `LOCALSWIM_TOKEN` and `LOCALSWIM_URL` when the CLI session is done. The
next cold start deletes the stopped service's credential folder.

## The service receipt and credential files

The service receipt and the launch credential files are an interface. Another
program running as the same Windows user on the same PC MAY rely on everything
this section states as a rule. The known readers are:

- agentspace's operator, which pushes the Bot token to its box
  (`C:\Projects\agentspace`, `docs/operator.md`, "The token push").
- The board watcher, whose `lib\env.ps1` loads the Bot token
  (`C:\Projects\claude-localswim-watcher`).
- swimcoach's `scripts/localswim.ps1`, which loads the Bot token
  (`C:\Projects\swimcoach`).

None of them writes these files.

### Announcing a change

- A change that removes, renames or changes the meaning or format of anything
  this section states as a rule is a breaking change. It MUST land through a
  pull request that updates this section, even when it touches only
  documentation.
- Before that pull request merges, the architect MUST announce the change to
  Terry in the session and in the pull request. The architect MUST also file a
  card for each known reader that names the change and the pull request: on
  the reader's own board, or on the localswim board when it has none.
- A breaking change MAY merge before its readers handle it. Terry coordinates
  the readers separately (Terry, 2026-11-20).
- Adding a receipt member is not a breaking change and needs no announcement.

### The receipt

- The receipt is `%LOCALAPPDATA%\localswim-v2\service.json`. There is one per
  Windows user, naming the installation that user's tools reach.
- [`Start-Localswim.ps1`](#launch) writes it after the service confirms startup. The
  service itself never reads or writes it. A staged activation leaves it
  unchanged, because activation keeps the port and the credentials. A
  disposable or test cold start MUST NOT write it.
- It MUST be replaced by renaming a complete file over it, as `MoveFileEx` does
  with `MOVEFILE_REPLACE_EXISTING`, so a reader never finds it missing or
  partial. Deleting it and then renaming the new file MUST NOT be used.
- Opening it can still fail briefly with a sharing violation while it is
  replaced. A reader SHOULD retry such a failure after a short pause before it
  treats the receipt as unreadable. A reader SHOULD close the receipt as soon as
  it has read it, because any open handle, whatever its sharing mode, makes the
  launcher's rename wait.
- It MUST be one JSON object, in UTF-8 with no byte order mark. A reader SHOULD
  also accept a UTF-8 byte order mark. Whitespace, line breaks and member order
  carry no meaning.
- It MUST match the JSON Schema
  [`docs/schemas/service-receipt.schema.json`](schemas/service-receipt.schema.json),
  which `check.ps1` tests against an example and a set of invalid documents.
- It MUST contain each of these members exactly once, each a JSON string. A
  member's name MUST NOT differ from another's only in ASCII case.

  | Member | Value |
  | --- | --- |
  | `executable` | The absolute path of the executable a cold start runs. |
  | `data_directory` | The absolute path of the service's data directory. |
  | `base` | The service's base URL, `http://127.0.0.1:PORT`, where `PORT` is the listening port in decimal, 1 to 65535, with no leading zero. It has no trailing slash. |
  | `credential_directory` | The absolute path of the folder that holds the current cold start's credentials. |

- New members MAY be added. A reader MUST ignore members it does not use.
- A copy kept beside the receipt, such as `service.json.bak-*`, is an operator's
  backup and not part of the interface.

### The credential folder

- Each cold start of the installation the receipt names MUST generate new
  Human and Bot tokens in a new folder, and the receipt's
  `credential_directory` MUST name that folder once the service confirms
  startup. The tokens in that folder are the ones the running service accepts,
  until its next cold start. A staged activation keeps them.
- The receipt names the folder by its path. The folder's own name and location
  are not part of the interface.
- A reader SHOULD read the receipt and the credential file afresh for each use.
  Between a confirmed start and the receipt's replacement, which the launcher
  does at once, the receipt still names the retired folder, whose tokens
  the new service refuses. A reader whose token is refused SHOULD read the
  receipt again before it retries.
- A retired folder MAY be deleted at any time after its service stops, so a
  reader MUST NOT depend on it.

### The credential files

- The folder MUST hold `human.json` and `bot.json`. The role names `human` and
  `bot`, and so the file names, are fixed. A reader MUST ignore any other file
  in the folder.
- Each file MUST be one JSON object in UTF-8 with no byte order mark, matching
  the JSON Schema
  [`docs/schemas/launch-credential.schema.json`](schemas/launch-credential.schema.json),
  which `check.ps1` tests against an example and a set of invalid documents.
  A reader SHOULD also accept a UTF-8 byte order mark.
  `New-LocalswimCredentialFolder` writes the same format from PowerShell 7 and
  Windows PowerShell 5.1, with
  different indentation. Whitespace, line breaks and member order carry no
  meaning.
- It MUST contain exactly these four members, each once, and no others. A
  member's name MUST NOT differ from another's only in ASCII case. `role` MUST
  match the file name. A reader MUST refuse a file that breaks these rules,
  except that a reader SHOULD refuse duplicate member names when its JSON
  parser reports them. PowerShell's `ConvertFrom-Json` keeps the last value and
  does not report a duplicate.

  | Member | Value |
  | --- | --- |
  | `format` | `"localswim-credential"` |
  | `format_version` | `1` |
  | `role` | `"human"` or `"bot"`, matching the file name. |
  | `token_dpapi_hex` | The protected token as lowercase hexadecimal, as `ConvertFrom-SecureString` writes it. |

- `format_version` MUST be written as the integer `1`, with no fraction or
  exponent. A reader MUST refuse a `format` or `format_version` it does not
  know, comparing `format` exactly and `format_version` as an integer. A change
  to the format MUST raise `format_version`.
- The schema checks the members, their values and the blob's header and flags.
  It cannot check duplicate or case-variant member names, `role` against the
  file name, whether the blob was made with entropy, or the decrypted
  plaintext. A reader MUST check the case-variant names, the role and the
  plaintext itself, and SHOULD check duplicates as stated above. The schema
  also accepts `1.0` for `format_version`. A reader MUST refuse it.
- `token_dpapi_hex` MUST decode to a Windows DPAPI blob made in user scope with
  no optional entropy, so `CryptUnprotectData`, run as the same Windows user on
  the same PC with no entropy, returns the token. In PowerShell,
  `ConvertTo-SecureString` does this. The blob starts with DPAPI version 1 and
  the provider GUID `df9d8cd0-1501-11d1-8c7a-00c04fc297eb`, and its flags field
  is 0, which marks user scope.
- The decrypted plaintext MUST be the token in UTF-16LE, with no byte order mark
  and no terminator.
- The token MUST be 32 random bytes in unpadded base64url: 43 characters from
  `A-Z`, `a-z`, `0-9`, `-` and `_`.
- This document and the schema examples MUST NOT contain a real token or a real
  protected blob. The examples' `token_dpapi_hex` values are a DPAPI header
  followed by bytes of value zero, which `CryptUnprotectData` refuses.

## Recover after the service exits unexpectedly

1. Confirm the service process has exited and the configured port has no
   listener. If a process still owns the port, identify it before taking
   action. Keep the data directory and its writer lock files in place.
2. Run [`Start-Localswim.ps1`](#launch). It reuses the receipt's executable,
   data directory and port with fresh credentials. The background launcher
   waits up to 10 seconds for the child to report readiness. On an unconfirmed
   result, inspect the reported process and listener before another launch
   attempt.
3. Load the new token into each CLI terminal's `LOCALSWIM_TOKEN`, as
   [Connect and stop](#connect-and-stop) shows.
   Use `service status` to check the port and running state, and `board get`
   with the original board UUID to confirm the board identity. Inspect the last
   affected ticket with `card inspect --prose BOARD_ID TICKET`. If a write's
   result was lost during the crash, check the saved state before submitting
   it again.
4. Leave the original board tab open. Live updates reconnect and obtain a new
   browser session. Wait up to 30 seconds after startup confirmation for
   **Live updates connected**, then check the last saved edit on the board.
   If the board has an update error, use **Retry board update**. Reopen the same
   board URL if the tab still cannot recover after the service and board respond
   through the CLI.

If the service starts but the board is unavailable, use the
[board recovery procedure](v1-parity-milestones/m84.md) with a verified input.

## Activate a staged Windows build

1. Place the verified candidate executable at `candidate.exe` in the same data
   directory. Write `candidate.json` as UTF-8 with a `version` string and the
   candidate file's lowercase SHA-256 in `sha256`. Use `Get-FileHash -Algorithm
   SHA256` on the staged file, and check that `service builds` reports one valid
   candidate.
2. From the CLI, use `service activate BUILD_ID` with the Human credential and
   keep the activation ID from the receipt or error. An accepted receipt means
   the service started the handoff. It drains admitted work and flushes enabled
   publishers before replacing the listener.
3. For inspection, use `service activation ACTIVATION_ID` and `service
   status` on the same origin. Allow up to 30 seconds for these read-only checks.
   Confirm `activated`, the staged build ID, a new service instance ID, and the
   original port. If the receipt remains uncertain or records failure, inspect
   the service and any publisher error before attempting another action.
4. Keep the existing Human and Bot CLI credentials. Activation preserves them
   and invalidates the old browser session. Return to the original board URL
   and confirm the last saved edit and a subsequent authorized write. Use
   `card inspect` for independent readback.

Activation runs a private copy of the staged executable. A later cold start
runs the executable the receipt names, so install the build you want there.

The V1-equivalent browser UI has no service activation panel. Use the CLI for
activation and the browser for the reconnect verification described below.

## Qualify with disposable data

From the repository root, run the Windows lifecycle check in PowerShell 7.3 or
newer with a prebuilt executable. Pass `-Candidate` to exercise a separately built staged
image. With the default, the same image still goes through native replacement.

```powershell
pwsh -NoProfile -File .\scripts\Test-WindowsReconnect.ps1 -Executable .\localswim.exe
```

The check creates a fresh temporary data directory and uses a fixed loopback
port for its entire run. It forces the fixture service to exit after a confirmed
write, cold-starts with fresh credentials, and then exercises staged activation.
It verifies retired browser capabilities, the original board and card IDs,
saved revisions, writes after both replacements, and final offline readback.
For the browser walkthrough, keep the same board tab open through each
replacement and verify that it reconnects, shows the saved card, and accepts an
edit that the CLI can read back.
