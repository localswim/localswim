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
command arguments, and shell history.

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
    & $exe serve --background --data $data --port $port
    if ($LASTEXITCODE -ne 0) { throw 'Localswim did not confirm startup.' }
}
finally {
    Remove-Item Env:LOCALSWIM_HUMAN_TOKEN, Env:LOCALSWIM_BOT_TOKEN -ErrorAction SilentlyContinue
}
```

A successful launch prints the loopback URL and service PID after the child
opens the data directory and binds the selected listener. Open that URL in
your browser. It provisions its own browser session.

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
2. In the workspace **Service** panel, select **Activate staged build** once.
   From the CLI, use `service activate BUILD_ID` with the Human credential and
   keep the activation ID from the receipt or error. An accepted receipt means
   the service started the handoff. It drains admitted work and flushes enabled
   publishers before replacing the listener.
3. Use **Refresh service status** if the browser reports an unconfirmed result.
   For CLI inspection, use `service activation ACTIVATION_ID` and `service
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

## Qualify with disposable data

From the repository root, run the Windows lifecycle check in PowerShell 7 with a
prebuilt executable. Pass `-Candidate` to exercise a separately built staged
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
