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
