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

A first launch, with no receipt yet, passes -Executable, -DataDirectory and -Port. While a
receipt exists, a non-disposable run takes no paths, so it can only restart that installation.

-Disposable starts a test or demo instance from -Executable, -DataDirectory, -Port,
-CredentialRoot and -LogFile, all required, and never touches the receipt or another credential
folder.

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
if (-not $IsWindows) { throw 'Localswim launches only on Windows.' }
. (Join-Path $PSScriptRoot 'LocalswimCredential.ps1')

New-Item -ItemType Directory -Path (Split-Path $LogFile) -Force | Out-Null
# Times are in the laptop's own time zone, as h:mmam, because a person reads this log.
function Write-StartLog([string]$Message) {
    $now = Get-Date
    $stamp = $now.ToString('yyyy-MM-dd ', [cultureinfo]::InvariantCulture) +
        $now.ToString('h:mmtt', [cultureinfo]::InvariantCulture).ToLowerInvariant()
    Add-Content -LiteralPath $LogFile -Value "$stamp $Message"
    Write-Output $Message
}

function Test-Listening([int]$OnPort) {
    $client = [Net.Sockets.TcpClient]::new()
    try {
        return ($client.ConnectAsync('127.0.0.1', $OnPort).Wait(2000) -and $client.Connected)
    }
    catch {
        return $false
    }
    finally {
        $client.Dispose()
    }
}

$receiptFile = Join-Path $env:LOCALAPPDATA 'localswim-v2\service.json'
$receipt = $null
$explicit = @('Executable', 'DataDirectory', 'Port') | Where-Object { $PSBoundParameters.ContainsKey($_) }
if ($Disposable) {
    $missing = @('Executable', 'DataDirectory', 'Port', 'CredentialRoot', 'LogFile') |
        Where-Object { -not $PSBoundParameters.ContainsKey($_) }
    if ($missing) {
        throw "-Disposable needs its own $(($missing | ForEach-Object { "-$_" }) -join ', ')."
    }
}
elseif (Test-Path -LiteralPath $receiptFile) {
    if ($explicit) {
        throw "A receipt exists at $receiptFile, so this run restarts that installation and takes no paths. Use -Disposable for a test instance."
    }
    $receipt = [IO.File]::ReadAllText($receiptFile) | ConvertFrom-Json
    $Executable = $receipt.executable
    $DataDirectory = $receipt.data_directory
    $Port = ([uri]$receipt.base).Port
}
elseif (@($explicit).Count -ne 3) {
    throw "No service receipt at $receiptFile. For a first launch, pass -Executable, -DataDirectory and -Port."
}

try {
    # A listener on the port is either this localswim, already started, or something else.
    if (Test-Listening $Port) {
        $page = ''
        try {
            $page = (Invoke-WebRequest -Uri "http://127.0.0.1:$Port/" -TimeoutSec 5 -NoProxy).Content
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
    $keep = $false
    try {
        $env:LOCALSWIM_HUMAN_TOKEN = Read-LocalswimToken -CredentialDirectory $credentialDirectory -Role human
        $env:LOCALSWIM_BOT_TOKEN = Read-LocalswimToken -CredentialDirectory $credentialDirectory -Role bot
        $output = @(& $Executable serve --background $DataDirectory $Port 2>&1 | ForEach-Object { "$_" })
        $code = $LASTEXITCODE
        $output | ForEach-Object { Write-StartLog "Serve output: $_" }
        if ($code -ne 0) {
            # An unconfirmed child can still bind after the launcher gives up, using these tokens.
            if (($output -join "`n") -match 'unconfirmed' -or (Test-Listening $Port)) {
                $keep = $true
                Write-StartLog "Kept $credentialDirectory, because the unconfirmed service may still be starting with its tokens."
            }
            throw "Localswim did not confirm startup (exit $code)."
        }
        $launched = $true
    }
    finally {
        Remove-Item Env:LOCALSWIM_HUMAN_TOKEN, Env:LOCALSWIM_BOT_TOKEN -ErrorAction SilentlyContinue
        if (-not $launched -and -not $keep) {
            Remove-Item -LiteralPath $credentialDirectory -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
    Write-StartLog "Started localswim on port $Port with credentials in $credentialDirectory."
    if ($Disposable) {
        return
    }

    # Name this cold start in the receipt. A rename that replaces the old file means a reader
    # never finds the receipt missing or partial; a reader holding it open delays the rename.
    New-Item -ItemType Directory -Path (Split-Path $receiptFile) -Force | Out-Null
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

    # The old receipt's folder belonged to the service on this port, which the probe found
    # stopped. Anything else is kept.
    $retired = if ($receipt) { [string]$receipt.credential_directory } else { '' }
    if ($retired) {
        $root = [IO.Path]::GetFullPath((Convert-Path -LiteralPath $CredentialRoot)).TrimEnd('\') + '\'
        if (-not [IO.Path]::IsPathRooted($retired)) {
            Write-StartLog "Kept the retired credential folder $retired, because the old receipt named it by a relative path."
        }
        elseif (-not (Test-Path -LiteralPath $retired)) {
            Write-StartLog "The retired credential folder $retired is already gone."
        }
        else {
            $retiredPath = [IO.Path]::GetFullPath((Convert-Path -LiteralPath $retired)).TrimEnd('\')
            $newPath = [IO.Path]::GetFullPath($next.credential_directory).TrimEnd('\')
            if ($retiredPath -ne $newPath -and $retiredPath.StartsWith($root, [StringComparison]::OrdinalIgnoreCase) -and
                ([uri]$receipt.base).Port -eq $Port) {
                Remove-Item -LiteralPath $retiredPath -Recurse -Force
                Write-StartLog "Deleted the retired credential folder $retiredPath."
            }
            else {
                Write-StartLog "Kept the retired credential folder $retiredPath, because it is not a separate folder under $root for this port."
            }
        }
    }
}
catch {
    Write-StartLog "Error: $($_.Exception.Message)"
    throw
}
