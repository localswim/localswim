#Requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Executable,
    [string]$Candidate = $Executable
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'This qualification requires Windows.' }

function New-Capability {
    [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)).
        TrimEnd('=').Replace('+', '-').Replace('/', '_')
}

function Invoke-FixtureExecutable([string[]]$Arguments, [hashtable]$Environment, [int]$TimeoutMs = 15000) {
    $start = [Diagnostics.ProcessStartInfo]::new($fixtureExe)
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    foreach ($argument in $Arguments) { $start.ArgumentList.Add($argument) }
    foreach ($key in $Environment.Keys) { $start.Environment[$key] = $Environment[$key] }
    $process = [Diagnostics.Process]::Start($start)
    try {
        $stdout = $process.StandardOutput.ReadToEndAsync()
        $stderr = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit($TimeoutMs)) {
            $process.Kill()
            [void]$process.WaitForExit(2000)
            throw "Fixture $($Arguments[0]) command exceeded its deadline."
        }
        if (-not [Threading.Tasks.Task]::WaitAll(@($stdout, $stderr), 2000)) {
            throw 'Fixture command output did not close before its deadline.'
        }
        [pscustomobject]@{ ExitCode = $process.ExitCode; Output = $stdout.Result; Error = $stderr.Result }
    }
    finally { $process.Dispose() }
}

function Invoke-FixtureCli([string[]]$Arguments, [string]$Token) {
    $reply = Invoke-FixtureExecutable $Arguments @{ LOCALSWIM_URL = $origin; LOCALSWIM_TOKEN = $Token }
    if ($reply.ExitCode -ne 0) { throw "Fixture $($Arguments[0]) $($Arguments[1]) failed." }
    $reply.Output | ConvertFrom-Json
}

function Get-OwnedServiceProcesses {
    $runPrefix = Join-Path $data '.localswim-run-'
    foreach ($process in Get-Process -Name 'm93-launcher', 'localswim' -ErrorAction SilentlyContinue) {
        $image = $process.Path
        if ($image -eq $fixtureExe -or ($image -and $image.StartsWith($runPrefix, [StringComparison]::OrdinalIgnoreCase) -and
                [IO.Path]::GetFileName($image) -eq 'localswim.exe' -and
                [IO.Path]::GetDirectoryName([IO.Path]::GetDirectoryName($image)) -eq $data)) {
            [void]$process.Handle
            $process
        }
        else { $process.Dispose() }
    }
}

function Start-FixtureService([string]$Human, [string]$Bot) {
    $reply = Invoke-FixtureExecutable @('serve', '--background', '--data', $data, '--port', "$port") @{
        LOCALSWIM_HUMAN_TOKEN = $Human
        LOCALSWIM_BOT_TOKEN = $Bot
    }
    if ($reply.ExitCode -ne 0 -or $reply.Output.Trim() -notmatch "^$([regex]::Escape($origin)) \(PID ([0-9]+)\)$") {
        throw 'Fixture background launch did not confirm its listener and process.'
    }
    $reportedPID = [int]$Matches[1]
    $owned = @(Get-OwnedServiceProcesses)
    if ($owned.Count -ne 1 -or $owned[0].Id -ne $reportedPID) {
        $owned | ForEach-Object { $_.Dispose() }
        throw 'Fixture launcher did not identify one owned service process.'
    }
    $owned[0]
}

function Get-BrowserCapability([string]$ApiVersion) {
    $result = Invoke-RestMethod -Uri "$origin/api/$ApiVersion/browser-session" -Method Post `
        -Headers @{ Origin = $origin; 'Sec-Fetch-Site' = 'same-origin' } `
        -NoProxy -MaximumRedirection 0 -TimeoutSec 3
    if ($result.kind -cne 'human' -or $result.expires_on_service_replacement -ne $true -or
        $result.access_token -cnotmatch '^[A-Za-z0-9_-]{43}$') {
        throw 'Fixture browser bootstrap returned an invalid session.'
    }
    $result.access_token
}

function Assert-StaleCapability([string]$Token) {
    $reply = Invoke-FixtureExecutable @('service', 'status') @{ LOCALSWIM_URL = $origin; LOCALSWIM_TOKEN = $Token }
    if ($reply.ExitCode -eq 0 -or $reply.Error -notmatch '\bHTTP 401\b') {
        throw 'Fixture service did not refuse the retired capability with HTTP 401.'
    }
}

function Assert-SavedCard($Expected, [string]$Token) {
    $saved = Invoke-FixtureCli @('card', 'get', $Expected.id) $Token
    if ($saved.id -cne $Expected.id -or $saved.board_id -cne $Expected.board_id -or
        $saved.title -cne $Expected.title -or $saved.revision -ne $Expected.revision) {
        throw 'Fixture card identity or durable revision changed.'
    }
}

$Executable = (Get-Item -LiteralPath $Executable).FullName
$Candidate = (Get-Item -LiteralPath $Candidate).FullName
$scratch = [IO.Directory]::CreateTempSubdirectory('localswim-m93-').FullName
$data = Join-Path $scratch 'data'
$fixtureExe = Join-Path $scratch 'm93-launcher.exe'
$human = New-Capability
$bot = New-Capability
$phase = 'initial launch'
try {
    Copy-Item -LiteralPath $Executable -Destination $fixtureExe
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    try { $port = $listener.LocalEndpoint.Port }
    finally { $listener.Stop() }
    $origin = "http://127.0.0.1:$port"

    $service = Start-FixtureService $human $bot
    try {
        $initial = Invoke-FixtureCli @('service', 'status') $bot
        $initialBrowser = Get-BrowserCapability $initial.api_version
        $board = Invoke-FixtureCli @('board', 'create', '--name', 'M93 disposable board', '--preset', 'terry') $human
        $lane = (Invoke-FixtureCli @('board', 'get', '--view', 'lanes', $board.id) $bot).lanes |
            Where-Object key -CEQ 'backlog'
        $card = Invoke-FixtureCli @('card', 'create', '--title', 'Durable before crash', $board.id, $lane.id) $bot
        $phase = 'forced exit'
        $service.Kill()
        if (-not $service.WaitForExit(5000)) { throw 'Fixture service did not exit before the deadline.' }
    }
    finally { $service.Dispose() }

    $phase = 'same-port cold recovery'
    $oldBot = $bot
    $human = New-Capability
    $bot = New-Capability
    (Start-FixtureService $human $bot).Dispose()
    $recovered = Invoke-FixtureCli @('service', 'status') $bot
    if ($initial.port -ne $port -or $recovered.port -ne $port -or
        $recovered.state -cne 'running' -or $recovered.service_instance_id -ceq $initial.service_instance_id) {
        throw 'Cold recovery did not replace the service instance on the selected port.'
    }
    Assert-StaleCapability $oldBot
    Assert-StaleCapability $initialBrowser
    $recoveredBrowser = Get-BrowserCapability $recovered.api_version
    Assert-SavedCard $card $recoveredBrowser
    $edit = Invoke-FixtureCli @('card', 'title', $card.id, 'Durable before activation') $bot
    if ($edit.revision -le $card.revision) { throw 'The first post-recovery write did not advance revision.' }

    $phase = 'staged activation'
    $artifact = Join-Path $data 'candidate.exe'
    Copy-Item -LiteralPath $Candidate -Destination $artifact
    @{
        version = 'm93-qualification'
        sha256 = (Get-FileHash -LiteralPath $artifact -Algorithm SHA256).Hash.ToLowerInvariant()
    } | ConvertTo-Json -Compress | Set-Content -LiteralPath (Join-Path $data 'candidate.json') -Encoding utf8NoBOM
    $staged = @( (Invoke-FixtureCli @('service', 'builds') $human).builds )
    if ($staged.Count -ne 1 -or $staged[0].validation -cne 'valid') { throw 'Fixture candidate did not validate.' }
    $accepted = Invoke-FixtureCli @('service', 'activate', $staged[0].id) $human
    $deadline = [Diagnostics.Stopwatch]::StartNew()
    do {
        $probe = Invoke-FixtureExecutable @('service', 'activation', $accepted.id) @{
            LOCALSWIM_URL = $origin
            LOCALSWIM_TOKEN = $human
        } 2000
        if ($probe.ExitCode -eq 0) {
            $receipt = $probe.Output | ConvertFrom-Json
            if ($receipt.state -ceq 'failed') { throw 'Fixture activation receipt recorded failure.' }
            if ($receipt.state -ceq 'activated' -and $receipt.build_id -ceq $staged[0].id) { break }
        }
        Start-Sleep -Milliseconds 100
    } while ($deadline.Elapsed.TotalSeconds -lt 20)
    if ($deadline.Elapsed.TotalSeconds -ge 20) { throw 'Fixture activation did not complete before the deadline.' }

    $phase = 'same-port activation readback'
    $activated = Invoke-FixtureCli @('service', 'status') $bot
    if ($activated.port -ne $port -or $activated.state -cne 'running' -or
        $activated.service_instance_id -ceq $recovered.service_instance_id -or $activated.build_id -cne $staged[0].id) {
        throw 'Staged activation did not replace the service instance on the selected port.'
    }
    Assert-StaleCapability $recoveredBrowser
    $activatedBrowser = Get-BrowserCapability $activated.api_version
    Assert-SavedCard $edit $activatedBrowser
    $last = Invoke-FixtureCli @('card', 'title', $card.id, 'Written after activation') $bot
    if ($last.revision -le $edit.revision) { throw 'The first post-activation write did not advance revision.' }
    Assert-SavedCard $last $bot

    $phase = 'graceful stop and offline readback'
    $owned = @(Get-OwnedServiceProcesses)
    try {
        if ($owned.Count -ne 1) { throw 'Expected exactly one fixture process after activation.' }
        $null = Invoke-FixtureCli @('service', 'stop') $human
        if (-not $owned[0].WaitForExit(5000)) { throw 'Fixture service did not stop before the deadline.' }
    }
    finally { $owned | ForEach-Object { $_.Dispose() } }
    $offline = Invoke-FixtureCli @('card', 'inspect', '--data', $data, $board.id, "$($last.ticket)") $bot
    if ($offline.board_id -cne $board.id -or $offline.card.id -cne $last.id -or $offline.card.title -cne $last.title -or
        $offline.card.revision -ne $last.revision) { throw 'Final fixture edit did not persist on disk.' }
}
catch { throw "Windows reconnect qualification failed during $phase. $($_.Exception.Message)" }
finally {
    foreach ($process in Get-OwnedServiceProcesses) {
        try {
            if (-not $process.HasExited) { $process.Kill() }
            if (-not $process.WaitForExit(5000)) { throw 'A fixture process remained after cleanup.' }
        }
        finally { $process.Dispose() }
    }
    $resolvedScratch = (Resolve-Path -LiteralPath $scratch).Path
    if ($resolvedScratch -cne $scratch -or [IO.Path]::GetDirectoryName($resolvedScratch) -ne
        [IO.Path]::TrimEndingDirectorySeparator([IO.Path]::GetTempPath())) {
        throw 'Fixture cleanup did not resolve to the generated temporary directory.'
    }
    Remove-Item -LiteralPath $resolvedScratch -Recurse -Force
}

Write-Output 'Windows cold recovery, staged activation, credential refresh and durable CLI writes passed.'
