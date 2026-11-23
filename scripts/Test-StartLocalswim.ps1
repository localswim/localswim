#Requires -Version 7.3
<#
.SYNOPSIS
Exercises Start-Localswim.ps1 with disposable instances, and never touches production.

.DESCRIPTION
Builds nothing; pass a localswim executable. Every instance uses a scratch data directory,
credential root, log and LOCALAPPDATA, and a port in 48970-48979. Each started service is
stopped with its own Human token. check.ps1 runs this with a fresh build.

.EXAMPLE
pwsh -NoProfile -File scripts\Test-StartLocalswim.ps1 -Executable $env:TEMP\localswim-dev.exe
#>
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Executable)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'This test requires Windows.' }
$start = Join-Path $PSScriptRoot 'Start-Localswim.ps1'
. (Join-Path $PSScriptRoot 'LocalswimCredential.ps1')
$Executable = Convert-Path -LiteralPath $Executable
$busy = @(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | Where-Object { $_.LocalPort -ge 48970 -and $_.LocalPort -le 48979 })
if ($busy) {
    throw "Test ports are in use ($(($busy.LocalPort | Sort-Object -Unique) -join ', ')); stop whatever holds them first."
}
$scratch = Join-Path ([IO.Path]::GetTempPath()) "localswim-start-test-$([guid]::NewGuid())"
New-Item -ItemType Directory -Path $scratch | Out-Null
$failures = [Collections.Generic.List[string]]::new()
$running = [Collections.Generic.List[object]]::new()

function Assert([bool]$Condition, [string]$What) {
    if ($Condition) { Write-Output "Pass: $What" } else { Write-Output "FAIL: $What"; $failures.Add($What) }
}
# Runs the launcher in a child process, optionally under a scratch LOCALAPPDATA.
function Invoke-Start([string[]]$Arguments, [string]$LocalAppData) {
    $saved = $env:LOCALAPPDATA
    if ($LocalAppData) { $env:LOCALAPPDATA = $LocalAppData }
    try {
        $out = & pwsh -NoProfile -NonInteractive -File $start @Arguments 2>&1 | ForEach-Object { "$_" }
        [pscustomobject]@{ Code = $LASTEXITCODE; Text = ($out -join "`n") }
    }
    finally {
        $env:LOCALAPPDATA = $saved
    }
}
function Stop-Instance([int]$Port, [string]$CredentialDirectory) {
    $env:LOCALSWIM_URL = "http://127.0.0.1:$Port"
    $env:LOCALSWIM_TOKEN = Read-LocalswimToken -CredentialDirectory $CredentialDirectory -Role human
    try {
        & $Executable service stop | Out-Null
        for ($i = 0; $i -lt 50 -and (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue); $i++) {
            Start-Sleep -Milliseconds 200
        }
    }
    finally {
        Remove-Item Env:LOCALSWIM_URL, Env:LOCALSWIM_TOKEN -ErrorAction SilentlyContinue
    }
}
function Get-Folders([string]$Root) { @(Get-ChildItem -LiteralPath $Root -Directory -ErrorAction SilentlyContinue) }

try {
    # Disposable launch, a no-op rerun, and isolation from the receipt.
    $port = 48970
    New-Item -ItemType Directory -Path "$scratch\d1\data", "$scratch\d1\creds", "$scratch\d1\local" | Out-Null
    $d1 = @('-Disposable', '-Executable', $Executable, '-DataDirectory', "$scratch\d1\data", '-Port', "$port",
        '-CredentialRoot', "$scratch\d1\creds", '-LogFile', "$scratch\d1\start.log")
    $r = Invoke-Start $d1 "$scratch\d1\local"
    $folders = @(Get-Folders "$scratch\d1\creds")
    if ($folders.Count -eq 1) { $running.Add(@($port, $folders[0].FullName)) }
    Assert ($r.Code -eq 0 -and $folders.Count -eq 1) 'A disposable launch starts with one credential folder.'
    Assert (-not (Test-Path "$scratch\d1\local\localswim-v2\service.json")) 'A disposable launch writes no receipt.'
    $r = Invoke-Start $d1 "$scratch\d1\local"
    Assert ($r.Code -eq 0 -and $r.Text -match 'already answers' -and @(Get-Folders "$scratch\d1\creds").Count -eq 1) 'A rerun while running does nothing.'

    # Refusals before anything starts.
    $r = Invoke-Start @('-Disposable', '-Executable', $Executable, '-DataDirectory', "$scratch\d1\data", '-Port', '48971') "$scratch\d1\local"
    Assert ($r.Code -ne 0 -and $r.Text -match 'CredentialRoot') 'A disposable launch without its own credential root and log is refused.'
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 48972)
    $listener.Start()
    try {
        $foreign = $d1.Clone(); $foreign[$foreign.IndexOf('-Port') + 1] = '48972'
        $r = Invoke-Start $foreign "$scratch\d1\local"
        Assert ($r.Code -ne 0 -and $r.Text -match 'something other than localswim' -and @(Get-Folders "$scratch\d1\creds").Count -eq 1) 'A foreign listener is refused and leaves no folder.'
    }
    finally {
        $listener.Stop()
    }

    # A failed serve deletes its folder; an unconfirmed one keeps it.
    New-Item -ItemType Directory -Path "$scratch\fail\creds" | Out-Null
    Set-Content -LiteralPath "$scratch\fail\not-a-directory" -Value 'x'
    $r = Invoke-Start @('-Disposable', '-Executable', $Executable, '-DataDirectory', "$scratch\fail\not-a-directory", '-Port', '48973',
        '-CredentialRoot', "$scratch\fail\creds", '-LogFile', "$scratch\fail\start.log") "$scratch\d1\local"
    Assert ($r.Code -ne 0 -and @(Get-Folders "$scratch\fail\creds").Count -eq 0) 'A failed launch deletes its unused credential folder.'
    New-Item -ItemType Directory -Path "$scratch\unconfirmed\creds" | Out-Null
    Set-Content -LiteralPath "$scratch\unconfirmed\serve.cmd" -Value "@echo Localswim did not confirm startup: unconfirmed (PID 1)`r`n@exit /b 1"
    $r = Invoke-Start @('-Disposable', '-Executable', "$scratch\unconfirmed\serve.cmd", '-DataDirectory', "$scratch\d1\data", '-Port', '48974',
        '-CredentialRoot', "$scratch\unconfirmed\creds", '-LogFile', "$scratch\unconfirmed\start.log") "$scratch\d1\local"
    Assert ($r.Code -ne 0 -and @(Get-Folders "$scratch\unconfirmed\creds").Count -eq 1) 'An unconfirmed launch keeps its credential folder.'

    # Receipt mode under a scratch LOCALAPPDATA: first launch, restart, and the guard.
    $port = 48975
    $local = "$scratch\r\local"
    New-Item -ItemType Directory -Path "$scratch\r\data", $local | Out-Null
    $r = Invoke-Start @() $local
    Assert ($r.Code -ne 0 -and $r.Text -match 'first launch') 'With no receipt, a launch without paths is refused.'
    $r = Invoke-Start @('-Executable', $Executable, '-DataDirectory', "$scratch\r\data", '-Port', "$port") $local
    $receiptFile = "$local\localswim-v2\service.json"
    $first = if (Test-Path $receiptFile) { Get-Content -Raw $receiptFile | ConvertFrom-Json } else { $null }
    Assert ($r.Code -eq 0 -and $first -and (Test-Path $first.credential_directory)) 'A first launch writes the receipt.'
    $schema = Join-Path (Split-Path $PSScriptRoot) 'docs\schemas\service-receipt.schema.json'
    Assert ([bool]$first -and (Test-Json -LiteralPath $receiptFile -SchemaFile $schema)) 'The receipt matches its schema.'
    $r = Invoke-Start @('-DataDirectory', "$scratch\r\data", '-Port', '48976') $local
    Assert ($r.Code -ne 0 -and $r.Text -match 'takes no paths' -and (Test-Path $first.credential_directory)) 'With a receipt, a launch with paths is refused and the running folder is kept.'
    if ($first) { Stop-Instance $port $first.credential_directory }
    $r = Invoke-Start @() $local
    $second = Get-Content -Raw $receiptFile | ConvertFrom-Json
    $running.Add(@($port, $second.credential_directory))
    Assert ($r.Code -eq 0 -and $second.credential_directory -ne $first.credential_directory) 'A restart replaces the receipt.'
    Assert (-not (Test-Path $first.credential_directory) -and -not (Test-Path "$receiptFile.new")) 'A restart deletes the retired folder and leaves no temporary receipt.'
}
finally {
    foreach ($instance in $running) {
        try {
            if (Get-NetTCPConnection -LocalPort $instance[0] -State Listen -ErrorAction SilentlyContinue) {
                Stop-Instance $instance[0] $instance[1]
            }
        }
        catch {
            Write-Output "FAIL: Could not stop the instance on port $($instance[0]): $($_.Exception.Message)"
            $failures.Add("Stop port $($instance[0])")
        }
    }
    Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
}
if ($failures.Count) { throw "$($failures.Count) launcher checks failed." }
Write-Output 'All launcher checks passed.'
