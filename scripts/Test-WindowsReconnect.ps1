#Requires -Version 7.3
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Executable,
    [string]$Candidate = $Executable
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'This qualification requires Windows.' }

Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Text;
using Microsoft.Win32.SafeHandles;

public sealed class M93FixtureProcess : IDisposable
{
    private readonly SafeProcessHandle handle;
    public int Id { get; }
    public string Image { get; }

    private M93FixtureProcess(SafeProcessHandle handle, int id, string image)
    {
        this.handle = handle;
        Id = id;
        Image = image;
    }

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern SafeProcessHandle OpenProcess(uint access, bool inheritHandle, int id);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool QueryFullProcessImageName(SafeProcessHandle process, uint flags, StringBuilder image, ref uint size);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool TerminateProcess(SafeProcessHandle process, uint exitCode);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern uint WaitForSingleObject(SafeProcessHandle process, uint milliseconds);

    public static M93FixtureProcess Capture(int id)
    {
        const uint terminate = 0x0001, queryLimitedInformation = 0x1000, synchronize = 0x100000;
        var handle = OpenProcess(terminate | queryLimitedInformation | synchronize, false, id);
        bool captured = false;
        try
        {
            if (handle.IsInvalid)
            {
                int error = Marshal.GetLastWin32Error();
                if (error == 87) return null; // The process ID has already exited.
                throw new Win32Exception(error);
            }
            uint size = 32768;
            var image = new StringBuilder((int)size);
            if (!QueryFullProcessImageName(handle, 0, image, ref size))
            {
                int error = Marshal.GetLastWin32Error();
                if (Wait(handle, 0)) return null;
                throw new Win32Exception(error);
            }
            if (Wait(handle, 0)) return null;
            var process = new M93FixtureProcess(handle, id, image.ToString());
            captured = true;
            return process;
        }
        finally { if (!captured) handle.Dispose(); }
    }

    private static bool Wait(SafeProcessHandle process, uint milliseconds)
    {
        uint status = WaitForSingleObject(process, milliseconds);
        if (status == 0) return true;
        if (status == 258) return false;
        throw new Win32Exception(Marshal.GetLastWin32Error());
    }

    public bool WaitForExit(uint milliseconds) => Wait(handle, milliseconds);

    public void Kill()
    {
        if (TerminateProcess(handle, 1)) return;
        int error = Marshal.GetLastWin32Error();
        if (!WaitForExit(0)) throw new Win32Exception(error);
    }

    public void Dispose() => handle.Dispose();
}
'@

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

function Test-FixtureImage([string]$Image) {
    $runPrefix = Join-Path $data '.localswim-run-'
    $Image -eq $fixtureExe -or ($Image -and $Image.StartsWith($runPrefix, [StringComparison]::OrdinalIgnoreCase) -and
        [IO.Path]::GetFileName($Image) -eq 'localswim.exe' -and
        [IO.Path]::GetDirectoryName([IO.Path]::GetDirectoryName($Image)) -eq $data)
}

function Get-OwnedServiceProcesses {
    foreach ($entry in Get-Process -Name 'm93-launcher', 'localswim' -ErrorAction SilentlyContinue) {
        try {
            $hint = $entry.Path
            if ($hint -and -not (Test-FixtureImage $hint)) { continue }
            try { $process = [M93FixtureProcess]::Capture($entry.Id) }
            catch {
                if ($hint -or $_.Exception.InnerException -isnot [ComponentModel.Win32Exception] -or
                    $_.Exception.InnerException.NativeErrorCode -ne 5) { throw }
                # An inaccessible process with no matching image hint may belong to another user.
                continue
            }
            if ($null -eq $process) { continue }
            if (Test-FixtureImage $process.Image) { $process }
            else { $process.Dispose() }
        }
        finally { $entry.Dispose() }
    }
}

function Clear-FixtureProcesses {
    $timer = [Diagnostics.Stopwatch]::StartNew()
    $emptyScans = 0
    $owned = [Collections.Generic.Dictionary[int, M93FixtureProcess]]::new()
    try {
        do {
            $scanComplete = $true
            try {
                Get-OwnedServiceProcesses | ForEach-Object {
                    if (-not $owned.TryAdd($_.Id, $_)) { $_.Dispose() }
                }
            }
            catch { $scanComplete = $false }

            if ($scanComplete -and $owned.Count -eq 0) {
                $emptyScans++
                if ($emptyScans -eq 2) { return }
            }
            else { $emptyScans = 0 }

            foreach ($process in $owned.Values) {
                try { $process.Kill() }
                catch {
                    # Keep the captured handle for observation and another attempt.
                    continue
                }
            }
            foreach ($process in @($owned.Values)) {
                $remaining = [Math]::Max(0, 5000 - $timer.ElapsedMilliseconds)
                try { $exited = $process.WaitForExit([uint32][Math]::Min(500, $remaining)) }
                catch { continue }
                if ($exited) {
                    [void]$owned.Remove($process.Id)
                    $process.Dispose()
                }
            }

            $remaining = 5000 - $timer.ElapsedMilliseconds
            if ($remaining -gt 0) { Start-Sleep -Milliseconds ([Math]::Min(100, $remaining)) }
        } while ($timer.ElapsedMilliseconds -lt 5000)
        throw 'Fixture process cleanup did not quiesce before its deadline.'
    }
    finally { $owned.Values | ForEach-Object { $_.Dispose() } }
}

function Start-FixtureService([string]$Human, [string]$Bot) {
    $reply = Invoke-FixtureExecutable @('serve', '--background', $data, "$port") @{
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
    try {
        $result = Invoke-RestMethod -Uri "$origin/api/$ApiVersion/browser-session" -Method Post `
            -Headers @{ Origin = $origin; 'Sec-Fetch-Site' = 'same-origin' } `
            -NoProxy -MaximumRedirection 0 -TimeoutSec 3
    }
    catch { throw 'Fixture browser bootstrap request failed.' }
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
    $saved = Invoke-FixtureCli @('card', 'get', $Expected.board_id, $Expected.id) $Token
    if ($saved.id -cne $Expected.id -or $saved.board_id -cne $Expected.board_id -or
        $saved.ticket -ne $Expected.ticket -or $saved.title -cne $Expected.title -or $saved.revision -ne $Expected.revision) {
        throw 'Fixture card identity or durable revision changed.'
    }
}

function Assert-CardWrite($Reply, [string]$BoardId, [string]$Title, $Previous = $null) {
    $idPattern = '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    if ($Reply.board_id -cne $BoardId -or $Reply.title -cne $Title -or
        $Reply.id -cnotmatch $idPattern -or $Reply.ticket -lt 1 -or $Reply.revision -lt 1 -or
        ($Previous -and ($Reply.id -cne $Previous.id -or $Reply.ticket -ne $Previous.ticket -or
            $Reply.revision -le $Previous.revision))) {
        throw 'Fixture card write did not confirm the expected board, ticket, title and revision.'
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
        $board = Invoke-FixtureCli @('board', 'create', '--preset', 'terry', 'name', 'M93 disposable board') $human
        $lane = (Invoke-FixtureCli @('board', 'get', '--view', 'lanes', $board.id) $bot).lanes |
            Where-Object key -CEQ 'backlog'
        $card = Invoke-FixtureCli @('card', 'create', $board.id, $lane.id, 'Durable before crash') $bot
        Assert-CardWrite $card $board.id 'Durable before crash'
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
    $edit = Invoke-FixtureCli @('card', 'title', $board.id, $card.id, 'Durable before activation') $bot
    Assert-CardWrite $edit $board.id 'Durable before activation' $card

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
    $last = Invoke-FixtureCli @('card', 'title', $board.id, $card.id, 'Written after activation') $bot
    Assert-CardWrite $last $board.id 'Written after activation' $edit
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
    if ($offline.board_id -cne $board.id -or $offline.card.id -cne $last.id -or $offline.card.ticket -ne $last.ticket -or
        $offline.card.title -cne 'Written after activation' -or $offline.card.revision -ne $last.revision) {
        throw 'Final fixture edit did not persist on disk.'
    }
}
catch { throw "Windows reconnect qualification failed during $phase. $($_.Exception.Message)" }
finally {
    Clear-FixtureProcesses
    $resolvedScratch = (Resolve-Path -LiteralPath $scratch).Path
    if ($resolvedScratch -cne $scratch -or [IO.Path]::GetDirectoryName($resolvedScratch) -ne
        [IO.Path]::TrimEndingDirectorySeparator([IO.Path]::GetTempPath())) {
        throw 'Fixture cleanup did not resolve to the generated temporary directory.'
    }
    Remove-Item -LiteralPath $resolvedScratch -Recurse -Force
}

Write-Output 'Windows cold recovery, staged activation, credential refresh and durable CLI writes passed.'
