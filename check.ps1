[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Invoke-Check {
    param([string]$Executable, [string[]]$Arguments)
    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Executable failed with exit code $LASTEXITCODE."
    }
}

Push-Location -LiteralPath $PSScriptRoot
try {
    $sources = @(Get-ChildItem -LiteralPath 'cmd', 'internal' -Filter '*.go' -File -Recurse |
        Select-Object -ExpandProperty FullName)
    $unformatted = @(& gofmt -l @sources)
    if ($LASTEXITCODE -ne 0) {
        throw 'Go formatting check failed.'
    }
    if ($unformatted.Count -gt 0) {
        $unformatted | Write-Output
        throw 'Run gofmt on the listed files.'
    }

    Invoke-Check 'go' @('vet', './...')
    Invoke-Check 'go' @('test', './...')
    $npm = if ($env:OS -eq 'Windows_NT') { 'npm.cmd' } else { 'npm' }
    Invoke-Check $npm @('--prefix', 'web', 'run', 'check')
    Invoke-Check $npm @('--prefix', 'web', 'test')
    Invoke-Check 'git' @('diff', '--check')
    Invoke-Check 'git' @('diff', '--cached', '--check')
    Write-Output 'All quality checks passed.'
}
finally {
    Pop-Location
}
