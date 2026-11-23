#Requires -Version 7.3
<#
.SYNOPSIS
Cold-starts localswim: fresh launch credentials, the service, the receipt, and cleanup.

.DESCRIPTION
With no arguments, starts the installation the service receipt names
(%LOCALAPPDATA%\localswim-v2\service.json), as the logon task does. If localswim already answers
on that port, it does nothing. Otherwise it creates a new credential folder, launches
`localswim serve --background`, replaces the receipt by an atomic rename, and deletes the retired
credential folder the old receipt named. docs/windows-operations.md, "Launch", describes each
step and the receipt and credential interface.

-Disposable starts a test or demo instance from -Executable, -DataDirectory, -Port and
-CredentialRoot, and never touches the receipt or another credential folder.

Each run appends to %LOCALAPPDATA%\localswim-v2\logs\start.log, or to -LogFile.

.EXAMPLE
pwsh -NoProfile -File scripts\Start-Localswim.ps1
#>
[CmdletBinding()]
param(
    [string]$Executable,
    [string]$DataDirectory,
    [ValidateRange(1, 65535)][int]$Port,
    [string]$CredentialRoot = (Join-Path $env:LOCALAPPDATA 'localswim-v2\credentials'),
    [string]$LogFile = (Join-Path $env:LOCALAPPDATA 'localswim-v2\logs\start.log'),
    [switch]$Disposable
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'localswim launches only on Windows.' }
. (Join-Path $PSScriptRoot 'LocalswimCredential.ps1')

New-Item -ItemType Directory -Path (Split-Path $LogFile) -Force | Out-Null
function Write-StartLog([string]$Message) {
    $line = "$([DateTime]::UtcNow.ToString('yyyy-MM-dd HH:mm:ss+00:00')) $Message"
    Add-Content -LiteralPath $LogFile -Value $line
    Write-Output $Message
}

$receiptFile = Join-Path $env:LOCALAPPDATA 'localswim-v2\service.json'
$receipt = $null
if ($Disposable) {
    if (-not $Executable -or -not $DataDirectory -or -not $Port) {
        throw '-Disposable needs -Executable, -DataDirectory and -Port.'
    }
}
elseif (Test-Path -LiteralPath $receiptFile) {
    $receipt = [IO.File]::ReadAllText($receiptFile) | ConvertFrom-Json
    if (-not $Executable) { $Executable = $receipt.executable }
    if (-not $DataDirectory) { $DataDirectory = $receipt.data_directory }
    if (-not $Port) { $Port = ([uri]$receipt.base).Port }
}
elseif (-not $Executable -or -not $DataDirectory -or -not $Port) {
    throw "No service receipt at $receiptFile. For a first launch, pass -Executable, -DataDirectory and -Port."
}

try {
    # A listener on the port is either this localswim, already started, or something else.
    $client = [Net.Sockets.TcpClient]::new()
    $listening = $false
    try {
        $listening = $client.ConnectAsync('127.0.0.1', $Port).Wait(2000) -and $client.Connected
    }
    catch {
        $listening = $false
    }
    finally {
        $client.Dispose()
    }
    if ($listening) {
        $page = ''
        try {
            $page = (Invoke-WebRequest -Uri "http://127.0.0.1:$Port/" -TimeoutSec 5 -UseBasicParsing).Content
        }
        catch {
            $page = ''
        }
        if ($page -match '/assets/app\.mjs') {
            Write-StartLog "Localswim already answers on port $Port; nothing to do."
            return
        }
        throw "Port $Port is in use by something other than localswim."
    }

    $credentialDirectory = New-LocalswimCredentialFolder -Root $CredentialRoot
    $launched = $false
    try {
        $env:LOCALSWIM_HUMAN_TOKEN = Read-LocalswimToken -CredentialDirectory $credentialDirectory -Role human
        $env:LOCALSWIM_BOT_TOKEN = Read-LocalswimToken -CredentialDirectory $credentialDirectory -Role bot
        $output = & $Executable serve --background $DataDirectory $Port 2>&1
        $code = $LASTEXITCODE
        $output | ForEach-Object { Write-StartLog "Serve output: $_" }
        if ($code -ne 0) {
            throw "Localswim did not confirm startup (exit $code)."
        }
        $launched = $true
    }
    finally {
        Remove-Item Env:LOCALSWIM_HUMAN_TOKEN, Env:LOCALSWIM_BOT_TOKEN -ErrorAction SilentlyContinue
        if (-not $launched) {
            Remove-Item -LiteralPath $credentialDirectory -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
    Write-StartLog "Started localswim on port $Port with credentials in $credentialDirectory."
    if ($Disposable) {
        return
    }

    # Name this cold start in the receipt. A rename that replaces the old file means a reader
    # never finds the receipt missing or partial; a reader holding it open delays the rename.
    $newFile = "$receiptFile.new"
    $next = [ordered]@{
        executable           = Convert-Path -LiteralPath $Executable
        data_directory       = Convert-Path -LiteralPath $DataDirectory
        base                 = "http://127.0.0.1:$Port"
        credential_directory = Convert-Path -LiteralPath $credentialDirectory
    }
    [IO.File]::WriteAllText($newFile, ($next | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
    for ($try = 1; ; $try++) {
        try {
            [IO.File]::Move($newFile, $receiptFile, $true)
            break
        }
        catch {
            if ($try -ge 50) { throw }
            Start-Sleep -Milliseconds 100
        }
    }
    Write-StartLog "Replaced the receipt at $receiptFile."

    # The old receipt's folder belonged to a service that is no longer running.
    $retired = if ($receipt) { [string]$receipt.credential_directory } else { '' }
    $root = (Convert-Path -LiteralPath $CredentialRoot).TrimEnd('\') + '\'
    if ($retired -and (Test-Path -LiteralPath $retired)) {
        $retiredPath = (Convert-Path -LiteralPath $retired).TrimEnd('\')
        if ($retiredPath -ne $next.credential_directory -and $retiredPath.StartsWith($root, [StringComparison]::OrdinalIgnoreCase)) {
            Remove-Item -LiteralPath $retiredPath -Recurse -Force
            Write-StartLog "Deleted the retired credential folder $retiredPath."
        }
    }
}
catch {
    Write-StartLog "Error: $($_.Exception.Message)"
    throw
}
