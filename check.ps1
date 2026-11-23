#Requires -Version 7.4
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
    # The launcher's disposable checks run against a fresh scratch build, never the repo-root executable.
    $launcherExe = Join-Path ([IO.Path]::GetTempPath()) "localswim-check-$PID.exe"
    Invoke-Check 'go' @('build', '-o', $launcherExe, './cmd/localswim')
    try {
        Invoke-Check 'pwsh' @('-NoProfile', '-NonInteractive', '-File', 'scripts/Test-StartLocalswim.ps1', '-Executable', $launcherExe)
    }
    finally {
        Remove-Item -LiteralPath $launcherExe -ErrorAction SilentlyContinue
    }
    $npm = if ($env:OS -eq 'Windows_NT') { 'npm.cmd' } else { 'npm' }
    Invoke-Check $npm @('--prefix', 'web', 'run', 'check')
    Invoke-Check $npm @('--prefix', 'web', 'test')
    # Each interface schema MUST accept its example and refuse every invalid one.
    $schemas = @(Get-ChildItem -LiteralPath 'docs/schemas' -Filter '*.schema.json' -File)
    if ($schemas.Count -eq 0) {
        throw 'No interface schemas found in docs/schemas.'
    }
    foreach ($name in $schemas.Name -replace '\.schema\.json$', '') {
        $schema = "docs/schemas/$name.schema.json"
        if (-not (Test-Json -LiteralPath "docs/schemas/examples/$name.json" -SchemaFile $schema)) {
            throw "docs/schemas/examples/$name.json does not match $schema."
        }
        $invalid = @(Get-ChildItem -LiteralPath 'docs/schemas/examples/invalid' -Filter "$name-*.json" -File)
        if ($invalid.Count -eq 0) {
            throw "No invalid examples exercise $schema."
        }
        foreach ($file in $invalid) {
            # An invalid example MUST be valid JSON, so the schema is what refuses it.
            if (-not (Test-Json -LiteralPath $file.FullName -ErrorAction SilentlyContinue)) {
                throw "$($file.Name) is not valid JSON."
            }
            if (Test-Json -LiteralPath $file.FullName -SchemaFile $schema -ErrorAction SilentlyContinue) {
                throw "$($file.Name) matches $schema but MUST NOT."
            }
        }
    }
    Invoke-Check 'git' @('diff', '--check')
    Invoke-Check 'git' @('diff', '--cached', '--check')
    Write-Output 'All quality checks passed.'
}
finally {
    Pop-Location
}
