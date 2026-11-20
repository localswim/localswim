# Run localswim V2 independently on Windows

Build or install the Windows executable as described in the [README](../README.md).
Choose the installation's fixed loopback port and absolute native V2 data
directory. Run the launcher from an ordinary Windows PowerShell terminal.
Once it confirms startup, the service runs without a console and you can close
that terminal. It runs as the Windows user who launched it, for the current
Windows session.

## Provision the launch credentials

Generate two fresh, distinct base64url tokens from 32 random bytes each for
each cold start. Use your approved secret store, or run this Windows PowerShell
block to generate and save a pair using Windows DPAPI. The files are decryptable
by the same Windows user on the same PC. Keep the credential directory private
and outside the checkout and data root.

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
        $credential = [PSCredential]::new($role, $secret)
        $credential | Export-Clixml -LiteralPath (Join-Path $credentialDirectory "$role.xml")
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
command arguments, and shell history. The folder, its file names and the file
format are part of the [receipt and credential interface](#the-service-receipt-and-credential-files).

## Launch

Replace these example settings with the selected executable, native V2 data
directory and fixed port.

```powershell
$exe = 'C:\LocalSwim V2\localswim.exe'
$data = 'C:\LocalSwim V2\data'
$port = 48126

try {
    $env:LOCALSWIM_HUMAN_TOKEN = (Import-Clixml -LiteralPath (Join-Path $credentialDirectory 'human.xml')).GetNetworkCredential().Password
    $env:LOCALSWIM_BOT_TOKEN = (Import-Clixml -LiteralPath (Join-Path $credentialDirectory 'bot.xml')).GetNetworkCredential().Password
    & $exe serve --background $data $port
    if ($LASTEXITCODE -ne 0) { throw 'Localswim did not confirm startup.' }
}
finally {
    Remove-Item Env:LOCALSWIM_HUMAN_TOKEN, Env:LOCALSWIM_BOT_TOKEN -ErrorAction SilentlyContinue
}
```

A successful launch prints the loopback URL and service PID after the child
opens the data directory and binds the selected listener. Open that URL in
your browser. It provisions its own browser session.

After a confirmed launch, replace the
[service receipt](#the-service-receipt-and-credential-files) so it names this
cold start. Write it beside the receipt and rename it into place, so a reader
never sees a partial file. Skip this step after an unconfirmed launch.

```powershell
$receiptFile = Join-Path $env:LOCALAPPDATA 'localswim-v2\service.json'
$receipt = [ordered]@{
    executable           = (Resolve-Path -LiteralPath $exe).Path
    data_directory       = (Resolve-Path -LiteralPath $data).Path
    base                 = "http://127.0.0.1:$port"
    credential_directory = $credentialDirectory
}
New-Item -ItemType Directory -Path (Split-Path $receiptFile) -Force | Out-Null
[IO.File]::WriteAllText("$receiptFile.new", ($receipt | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
Move-Item -LiteralPath "$receiptFile.new" -Destination $receiptFile -Force
```

An unconfirmed launch prints the child PID when available. Inspect that process
and the selected port before attempting a new launch. If the child exited
before binding, repeat the launch block with `--background` removed to inspect
startup errors.

## Connect and stop

In a separate PowerShell terminal, set `$exe`, `$port`, and
`$credentialDirectory` to the values for the confirmed launch. Use the Bot
token for Bot CLI work. The Human credential authorizes service shutdown.

```powershell
$env:LOCALSWIM_URL = "http://127.0.0.1:$port"
$env:LOCALSWIM_TOKEN = (Import-Clixml -LiteralPath (Join-Path $credentialDirectory 'bot.xml')).GetNetworkCredential().Password
& $exe board list

$env:LOCALSWIM_TOKEN = (Import-Clixml -LiteralPath (Join-Path $credentialDirectory 'human.xml')).GetNetworkCredential().Password
& $exe service status
& $exe service stop
```

Wait for the process and listener to exit. `service stop` drains admitted work
and flushes enabled publishers. If it refuses or the outcome is uncertain,
inspect `service status` and the affected publishing status before retrying.
Clear `LOCALSWIM_TOKEN` and `LOCALSWIM_URL` when the CLI session is done.
Discard the retired credential files after the service stops.

## The service receipt and credential files

RFC 2119 keywords; the capitals are load-bearing.

The service receipt and the launch credential files are an interface. Another
program running as the same Windows user on the same PC MAY rely on everything
this section states. The known readers are agentspace's operator, which pushes
the Bot token to its box, and the board watcher in
`C:\Projects\claude-localswim-watcher`, whose `lib\env.ps1` loads the Bot token.
Neither writes these files.

### Announcing a change

- A change that removes, renames or changes the meaning or format of anything
  this section guarantees is a breaking change. It MUST land in a pull request
  that updates this section.
- Before that pull request merges, the architect MUST announce the change to
  Terry and on the agentspace board, as a card that names the change and the
  pull request, and MUST record it in the watcher's `README.md`.
- Adding a receipt member is not a breaking change and needs no announcement.

### The receipt

- The receipt is `%LOCALAPPDATA%\localswim-v2\service.json`. There is one per
  Windows user, naming the installation that user's tools reach.
- The cold-start procedure in [Launch](#launch) writes it, after the service
  confirms startup. The service itself never reads or writes it. A staged
  activation leaves it unchanged, because activation keeps the port and the
  credentials.
- It MUST be replaced whole, by renaming a complete file into place, so a reader
  sees either the previous receipt or the new one.
- It MUST be one JSON object, encoded as UTF-8. The procedure writes it without a
  byte order mark and with CRLF line breaks. A reader SHOULD also accept a UTF-8
  byte order mark.
- It MUST contain each of these members exactly once, each a JSON string.

  | Member | Value |
  | --- | --- |
  | `base` | The service's base URL, `http://127.0.0.1:PORT`, with no trailing slash. |
  | `credential_directory` | The absolute path of the folder that holds the current cold start's credentials. |
  | `executable` | The absolute path of the executable a cold start runs. |
  | `data_directory` | The absolute path of the service's data directory. |

- New members MAY be added. A reader MUST ignore members it does not use.
- A copy kept beside the receipt, such as `service.json.bak-*`, is an operator's
  backup and not part of the interface.

### The credential folder

- Each cold start MUST generate new Human and Bot tokens in a new folder, and
  the receipt's `credential_directory` MUST name that folder once the service
  confirms startup. The tokens in that folder are the ones the running service
  accepts, until its next cold start. A staged activation keeps them.
- The receipt names the folder by its path. The folder's own name and location
  are not part of the interface.
- A reader SHOULD read the receipt and the credential file afresh for each use.
  Between a cold start and the receipt's replacement, the receipt still names
  the retired folder, whose tokens the new service refuses. A reader whose token
  is refused SHOULD read the receipt again before it retries.
- A retired folder is deleted after the service it belonged to stops. A reader
  MUST NOT expect a retired folder to remain.

### The credential files

- The folder MUST hold exactly two files, `human.xml` and `bot.xml`. The role
  names `human` and `bot`, and so the file names, are fixed.
- Each file is PowerShell CLIXML for one `PSCredential`, as `Export-Clixml`
  writes it. Either PowerShell 7 or Windows PowerShell 5.1 MAY write it, so a
  reader MUST accept both encodings:

  | Writer | Encoding |
  | --- | --- |
  | PowerShell 7 | UTF-8, no byte order mark |
  | Windows PowerShell 5.1 | UTF-16LE, with the byte order mark `FF FE` |

  Neither writes an XML declaration or a final line break. Lines end in CRLF.
- Every element is in the default namespace
  `http://schemas.microsoft.com/powershell/2004/04`. With the password shown as
  `HEX`, the document is:

  ```xml
  <Objs Version="1.1.0.1" xmlns="http://schemas.microsoft.com/powershell/2004/04">
    <Obj RefId="0">
      <TN RefId="0">
        <T>System.Management.Automation.PSCredential</T>
        <T>System.Object</T>
      </TN>
      <ToString>System.Management.Automation.PSCredential</ToString>
      <Props>
        <S N="UserName">bot</S>
        <SS N="Password">HEX</SS>
      </Props>
    </Obj>
  </Objs>
  ```

- `S N="UserName"` holds the role name. `SS N="Password"` holds the protected
  token as lowercase hexadecimal with no whitespace. A reader SHOULD select
  both elements by their `N` attribute, not by position.
- The hexadecimal decodes to a Windows DPAPI blob made in user scope with no
  optional entropy. `CryptUnprotectData`, run as the same Windows user on the
  same PC with no entropy, returns the token. The blob's flags field is 0, which
  marks user scope.
- The decrypted token is UTF-16LE with no byte order mark and no terminator.
  The token itself is 32 random bytes in unpadded base64url: 43 characters from
  `A-Z`, `a-z`, `0-9`, `-` and `_`.
- This document MUST NOT contain a real token or a real protected blob.

## Recover after the service exits unexpectedly

1. Confirm the service process has exited and the configured port has no
   listener. If a process still owns the port, identify it before taking
   action. Keep the data directory and its writer lock files in place.
2. Use the [credential provisioning](#provision-the-launch-credentials) and
   [launch](#launch) steps with the installed executable, the same absolute data
   directory, and the same port. The background launcher waits up to 10 seconds
   for the child to report readiness. On an unconfirmed result, inspect the
   reported process and listener before another launch attempt.
3. Load the new credential file into each CLI terminal's `LOCALSWIM_TOKEN`.
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
