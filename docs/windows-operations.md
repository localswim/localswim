# Run localswim V2 independently on Windows

Build or install the Windows executable as described in the [README](../README.md).
Choose the installation's fixed loopback port and absolute native V2 data
directory. Run the launcher from a PowerShell 7 or Windows PowerShell 5.1
terminal.
Once it confirms startup, the service runs without a console and you can close
that terminal. It runs as the Windows user who launched it, for the current
Windows session.

## Provision the launch credentials

Generate two fresh, distinct base64url tokens from 32 random bytes each for
each cold start with this block, which saves them using Windows DPAPI. The files
are decryptable by the same Windows user on the same PC. Keep the credential
directory private and outside the checkout and data root. Its output is part of
the [receipt and credential interface](#the-service-receipt-and-credential-files),
so a cold start of the installation the receipt names MUST use this block.

```powershell
$credentialDirectory = Join-Path $env:LOCALAPPDATA "localswim-v2\credentials\$([guid]::NewGuid())"
New-Item -ItemType Directory -Path $credentialDirectory -Force | Out-Null
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
try {
    foreach ($role in 'human', 'bot') {
        $bytes = [byte[]]::new(32)
        $rng.GetBytes($bytes)
        $token = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
        $secret = ConvertTo-SecureString -String $token -AsPlainText -Force
        $credential = [ordered]@{
            format          = 'localswim-credential'
            format_version  = 1
            role            = $role
            token_dpapi_hex = ConvertFrom-SecureString -SecureString $secret
        }
        [IO.File]::WriteAllText((Join-Path $credentialDirectory "$role.json"),
            ($credential | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
    }
}
finally {
    $rng.Dispose()
    Remove-Variable token, secret, credential -ErrorAction SilentlyContinue
}
$credentialDirectory
```

Keep the printed directory path for the CLI session. Load tokens directly into
process-scoped environment variables. Keep token values out of terminal output,
command arguments, and shell history. Define this function in each terminal
that loads a token. It reads a role's token from the credential folder:

```powershell
function Read-LocalswimToken([string]$Role) {
    $ErrorActionPreference = 'Stop'
    $file = Join-Path $credentialDirectory "$Role.json"
    $text = [IO.File]::ReadAllText($file)
    $credential = $text | ConvertFrom-Json
    $names = @($credential.PSObject.Properties.Name)
    $expected = 'format', 'format_version', 'role', 'token_dpapi_hex'
    $version = $credential.format_version
    if ($text -notmatch '^\s*\{' -or $names.Count -ne 4 -or
        @($names | Where-Object { $_ -cnotin $expected }).Count -ne 0 -or
        $credential.format -cne 'localswim-credential' -or
        -not ($version -is [int] -or $version -is [long]) -or $version -ne 1 -or
        $credential.role -cne $Role) {
        throw "Unsupported credential format in $file."
    }
    $token = [Net.NetworkCredential]::new('', (ConvertTo-SecureString -String $credential.token_dpapi_hex)).Password
    if ($token -cnotmatch '^[A-Za-z0-9_-]{43}\z') {
        throw "The token in $file is not 43 base64url characters."
    }
    $token
}
```

## Launch

Replace these example settings with the selected executable, native V2 data
directory and fixed port. Set `$writeReceipt` to `$false` for a disposable or
test launch, which MUST NOT write the receipt.

```powershell
$exe = 'C:\LocalSwim V2\localswim.exe'
$data = 'C:\LocalSwim V2\data'
$port = 48126
$writeReceipt = $true

try {
    $env:LOCALSWIM_HUMAN_TOKEN = Read-LocalswimToken human
    $env:LOCALSWIM_BOT_TOKEN = Read-LocalswimToken bot
    & $exe serve --background $data $port
    if ($LASTEXITCODE -ne 0) { throw 'Localswim did not confirm startup.' }
    if ($writeReceipt) {
        # Name this cold start in the receipt. A rename that replaces the old
        # file means a reader never finds the receipt missing or partial.
        $receiptFile = Join-Path $env:LOCALAPPDATA 'localswim-v2\service.json'
        $newFile = "$receiptFile.new"
        $receipt = [ordered]@{
            executable           = Convert-Path -LiteralPath $exe
            data_directory       = Convert-Path -LiteralPath $data
            base                 = "http://127.0.0.1:$port"
            credential_directory = Convert-Path -LiteralPath $credentialDirectory
        }
        New-Item -ItemType Directory -Path (Split-Path $receiptFile) -Force -ErrorAction Stop | Out-Null
        [IO.File]::WriteAllText($newFile, ($receipt | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
        if ($PSVersionTable.PSVersion.Major -ge 7) {
            $replace = { [IO.File]::Move($newFile, $receiptFile, $true) }
        }
        else {
            Add-Type -AssemblyName Microsoft.VisualBasic
            $replace = { [Microsoft.VisualBasic.FileIO.FileSystem]::MoveFile($newFile, $receiptFile, $true) }
        }
        # A reader holding the receipt open blocks the rename briefly.
        for ($try = 1; ; $try++) {
            try { & $replace; break }
            catch { if ($try -ge 50) { throw }; Start-Sleep -Milliseconds 100 }
        }
    }
}
finally {
    Remove-Item Env:LOCALSWIM_HUMAN_TOKEN, Env:LOCALSWIM_BOT_TOKEN -ErrorAction SilentlyContinue
}
```

A successful launch prints the loopback URL and service PID after the child
opens the data directory and binds the selected listener, and then replaces
the [service receipt](#the-service-receipt-and-credential-files). Open that URL
in your browser. It provisions its own browser session. An unconfirmed launch
leaves the receipt unchanged. If the receipt step fails after a confirmed
start, the service is running: rerun only the `if ($writeReceipt)` part of the
block with the same variables.

An unconfirmed launch prints the child PID when available. Inspect that process
and the selected port before attempting a new launch. If the child exited
before binding, repeat the launch block with `--background` removed to inspect
startup errors.

## Connect and stop

In a separate PowerShell terminal, set `$exe`, `$port`, and
`$credentialDirectory` to the values for the confirmed launch, and define
`Read-LocalswimToken` from [Provision the launch credentials](#provision-the-launch-credentials).
Use the Bot token for Bot CLI work. The Human credential authorizes service
shutdown.

```powershell
$env:LOCALSWIM_URL = "http://127.0.0.1:$port"
$env:LOCALSWIM_TOKEN = Read-LocalswimToken bot
& $exe board list

$env:LOCALSWIM_TOKEN = Read-LocalswimToken human
& $exe service status
& $exe service stop
```

Wait for the process and listener to exit. `service stop` drains admitted work
and flushes enabled publishers. If it refuses or the outcome is uncertain,
inspect `service status` and the affected publishing status before retrying.
Clear `LOCALSWIM_TOKEN` and `LOCALSWIM_URL` when the CLI session is done.
Delete the retired credential folder after the service stops.

## The service receipt and credential files

RFC 2119 keywords; the capitals are load-bearing.

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
  Terry in the session and in the pull request, and MUST post a card on the
  agentspace board (`c74ddc68-9c3a-4434-8435-563c596d3229`) that names the
  change and the pull request.
- The pull request MUST NOT merge until Terry confirms that each known reader
  handles the change.
- Adding a receipt member is not a breaking change and needs no announcement.

### The receipt

- The receipt is `%LOCALAPPDATA%\localswim-v2\service.json`. There is one per
  Windows user, naming the installation that user's tools reach.
- The [Launch](#launch) block writes it after the service confirms startup. The
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
  Launch block's rename wait.
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
  Between a confirmed start and the receipt's replacement, which the Launch
  block does at once, the receipt still names the retired folder, whose tokens
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
  A reader SHOULD also accept a UTF-8 byte order mark. The provisioning block
  writes the same format from PowerShell 7 and Windows PowerShell 5.1, with
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
  also accepts `1.0` for `format_version`, which a reader refuses.
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
2. Use the [credential provisioning](#provision-the-launch-credentials) and
   [launch](#launch) steps with the installed executable, the same absolute data
   directory, and the same port. The background launcher waits up to 10 seconds
   for the child to report readiness. On an unconfirmed result, inspect the
   reported process and listener before another launch attempt.
3. Load the new token into each CLI terminal's `LOCALSWIM_TOKEN` with
   `Read-LocalswimToken`.
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

Activation runs a private copy of the staged executable. A later cold launch
uses the executable selected in the launch command. Use the approved installation
path for the build you want to run on the next cold launch.

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
