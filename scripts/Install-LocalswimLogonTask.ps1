#Requires -Version 7.3
<#
.SYNOPSIS
Registers the scheduled task that starts localswim when this Windows user signs in.

.DESCRIPTION
The task runs scripts\Start-Localswim.ps1 at every logon of the current user, under
`conhost.exe --headless` so no window appears. It runs at standard rights as an interactive
principal, because the launch credentials are protected with this user's DPAPI key, and at
normal priority, which the service inherits. It has no time limit, ignores a second start while
one runs, and runs on battery. Running this again replaces the task.

-StartArguments passes arguments to Start-Localswim.ps1; tests use it with -Disposable and a
different -TaskName. -NoStart registers the task without running it now.

Uninstall with: Unregister-ScheduledTask -TaskName 'localswim service' -Confirm:$false

.EXAMPLE
pwsh -NoProfile -File scripts\Install-LocalswimLogonTask.ps1
#>
[CmdletBinding()]
param(
    [string]$TaskName = 'localswim service',
    [string[]]$StartArguments = @(),
    [switch]$NoStart
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'The logon task exists only on Windows.' }

$pwsh = (Get-Command pwsh).Source
$conhost = Join-Path $env:SystemRoot 'System32\conhost.exe'
$script = Join-Path $PSScriptRoot 'Start-Localswim.ps1'
# Quoting stays simple by refusing the values Windows argument rules treat specially.
foreach ($value in @($pwsh, $script) + $StartArguments) {
    if ($value -eq '' -or $value.Contains('"') -or $value.EndsWith('\')) {
        throw "Argument '$value' is empty, contains a quote, or ends in a backslash, which the task's command line cannot carry."
    }
}
$quoted = @($StartArguments | ForEach-Object { if ($_ -match '\s') { "`"$_`"" } else { $_ } })
$arguments = (@('--headless', "`"$pwsh`"", '-NoProfile', '-NonInteractive', '-File', "`"$script`"") + $quoted) -join ' '

$user = "$env:USERDOMAIN\$env:USERNAME"
$action = New-ScheduledTaskAction -Execute $conhost -Argument $arguments -WorkingDirectory (Split-Path $PSScriptRoot)
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $user
$principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
# Priority 4 is in Task Scheduler's normal band; the default, 7, would run the service below normal.
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew -StartWhenAvailable -Priority 4
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal `
    -Settings $settings -Description "Starts localswim at sign-in ($script)." -Force | Out-Null
Write-Output "Registered '$TaskName'."
if (-not $NoStart) {
    Start-ScheduledTask -TaskName $TaskName
    Write-Output "Started '$TaskName'. Its outcome is in the log Start-Localswim.ps1 writes, by default $(Join-Path $env:LOCALAPPDATA 'localswim-v2\logs\start.log')."
}
