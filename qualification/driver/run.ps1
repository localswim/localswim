param(
    [Parameter(Mandatory = $true)][string]$RuntimeRoot,
    [Parameter(Mandatory = $true)][string]$ModuleCache,
    [string]$GoExecutable = 'C:\Program Files\Go\bin\go.exe',
    [ValidateRange(1, 10)][int]$Repeats = 3
)

$ErrorActionPreference = 'Stop'
$repository = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$privateRoot = [IO.Path]::GetFullPath($RuntimeRoot)
if ($privateRoot.StartsWith($repository + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or $privateRoot -eq $repository) {
    throw 'RuntimeRoot must be outside the public repository.'
}
$cache = (Resolve-Path $ModuleCache).Path
New-Item -ItemType Directory -Force -Path $privateRoot | Out-Null
# A failed rerun must never leave the previous PASS as the current evidence.
[IO.File]::WriteAllText((Join-Path $privateRoot 'evidence.json'), "{`"status`":`"RUNNING_NOT_QUALIFIED`"}`n", [Text.UTF8Encoding]::new($false))
$settings = @('GOTOOLCHAIN', 'CGO_ENABLED', 'GOOS', 'GOARCH', 'GOFLAGS', 'GOMODCACHE', 'GOCACHE', 'GOTMPDIR')
$prior = @{}
foreach ($key in $settings) { $prior[$key] = [Environment]::GetEnvironmentVariable($key, 'Process') }
$oldLocation = Get-Location
try {
    Set-Location $repository
    $env:GOTOOLCHAIN = 'local'
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:GOFLAGS = '-mod=readonly'
    $env:GOMODCACHE = $cache
    $env:GOCACHE = Join-Path $privateRoot 'gocache'
    $env:GOTMPDIR = Join-Path $privateRoot 'gotmp'
    New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:GOTMPDIR | Out-Null
    $version = (& $GoExecutable version | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $version -ne 'go version go1.27.3 windows/amd64') { throw 'Exact native Go candidate required.' }
    $lockedBefore = @{}
    foreach ($file in @('go.mod', 'go.sum')) { $lockedBefore[$file] = (Get-FileHash $file -Algorithm SHA256).Hash.ToLowerInvariant() }
    $candidate = Get-Content qualification/environment.json -Raw | ConvertFrom-Json
    $acceptedMod = '5dfd8f468bc0fa34eaa1c6a64ca7ee3c59b12c943a991e1e78bec900e9d12ce1'
    $completeMod = 'e4884af04b03465b486bccd0f2e8c031d575b5d3ddbf20a6db64ab7fb84f3629'
    $acceptedSum = '73d8f9e5776a31a8d5eb60d1cec809f56ba60ffdc13933ae5db6b12fd5c9e8e8'
    $addendum = 'docs/decisions/go-module-completeness-2026-10-25.txt'
    $addendumText = Get-Content $addendum -Raw
    if ($lockedBefore['go.mod'] -ne $completeMod -or $lockedBefore['go.sum'] -ne $acceptedSum -or !$addendumText.Contains($acceptedMod) -or !$addendumText.Contains($completeMod) -or !$addendumText.Contains($acceptedSum)) { throw 'Manifest differs from documented exact additive repair.' }
    $selected = @(& $GoExecutable list -m '-f={{.Path}} {{.Version}}' all)
    if ($LASTEXITCODE -ne 0) { throw 'Readonly module graph inventory failed.' }
    $selected = @($selected | Where-Object { $_ -match ' v' } | Sort-Object)
    $expected = @($candidate.go_modules | ForEach-Object { "$($_.module) $($_.version)" } | Sort-Object)
    if (@(Compare-Object $expected $selected).Count -ne 0) { throw 'Selected module versions differ from Q01 inventory.' }
    & $GoExecutable mod verify *> (Join-Path $privateRoot 'module-verify.log')
    if ($LASTEXITCODE -ne 0) { throw 'Module verification failed; inspect private log.' }
    & $GoExecutable vet ./qualification/driver *> (Join-Path $privateRoot 'vet.log')
    if ($LASTEXITCODE -ne 0) { throw 'Vet failed; inspect private log.' }
    $binary = Join-Path $privateRoot 'q02-driver.exe'
    & $GoExecutable build -trimpath -buildvcs=false -o $binary ./qualification/driver *> (Join-Path $privateRoot 'build.log')
    if ($LASTEXITCODE -ne 0) { throw 'Build failed; inspect private log.' }
    $invalidFixture = Join-Path $privateRoot ('invalid-never-created-' + [guid]::NewGuid().ToString('N'))
    & $binary -runtime-root $invalidFixture -repeats 0 1> (Join-Path $privateRoot 'negative-arguments.stdout') 2> (Join-Path $privateRoot 'negative-arguments.stderr')
    if ($LASTEXITCODE -ne 1 -or (Test-Path -LiteralPath $invalidFixture)) { throw 'Invalid repeat bound did not fail before fixture creation.' }
    $rawResult = Join-Path $privateRoot 'worker-results.json'
    & $binary -runtime-root (Join-Path $privateRoot 'fixtures') -repeats $Repeats 1> $rawResult 2> (Join-Path $privateRoot 'worker-errors.log')
    if ($LASTEXITCODE -ne 0) { throw 'Harness failed; inspect private stderr.' }
    $observed = Get-Content $rawResult -Raw | ConvertFrom-Json
    if ($observed.repetitions -ne $Repeats -or @($observed.results).Count -ne $Repeats -or @($observed.results | Where-Object status -ne 'PASS_ISOLATED_SCOPE').Count -ne 0) { throw 'Incomplete evidence.' }
    $expectedOptions = @($candidate.sqlite.source_compile_options.options | Sort-Object)
    foreach ($result in $observed.results) {
        if (@(Compare-Object $expectedOptions @($result.compile_options | Sort-Object)).Count -ne 0) { throw 'Runtime compile options differ from frozen source inventory.' }
    }
    $hashes = [ordered]@{}
    foreach ($file in @('go.mod', 'go.sum', 'qualification/environment.json', 'docs/decisions/go-module-completeness-2026-10-25.txt', 'qualification/driver/main_windows.go', 'qualification/driver/run.ps1', 'qualification/driver/README.md')) {
        $hashes[$file] = (Get-FileHash $file -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    foreach ($file in @('go.mod', 'go.sum')) { if ($lockedBefore[$file] -ne $hashes[$file]) { throw 'Dependency lock changed.' } }
    $bindingHashes = [ordered]@{}
    foreach ($file in @('doc.go', 'conn.go', 'lib/mutex.go')) {
        $bindingHashes["modernc.org/sqlite@v1.61.0/$file"] = (Get-FileHash (Join-Path $cache "modernc.org/sqlite@v1.61.0/$file") -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    $evidence = [ordered]@{
        schema_version = 1
        task = 'Q02'
        recorded_date = (Get-Date -Format 'yyyy-MM-dd')
        status = 'PASS_ISOLATED_SCOPE_PENDING_INDEPENDENT_REVIEW'
        source_base_revision = (& git rev-parse HEAD | Out-String).Trim()
        source_state = 'Harness source identified by SHA256 below; base revision is not claimed to include uncommitted harness files.'
        source_sha256 = $hashes
        manifest_completeness_repair = [ordered]@{ accepted_candidate_go_mod_sha256 = $acceptedMod; complete_go_mod_sha256 = $completeMod; unchanged_go_sum_sha256 = $acceptedSum; selected_versions_match_q01_inventory = $true; reference = $addendum }
        inspected_binding_source_sha256 = $bindingHashes
        go_executable_sha256 = (Get-FileHash $GoExecutable -Algorithm SHA256).Hash.ToLowerInvariant()
        harness_executable_sha256 = (Get-FileHash $binary -Algorithm SHA256).Hash.ToLowerInvariant()
        environment = [ordered]@{ GOTOOLCHAIN = 'local'; CGO_ENABLED = '0'; GOOS = 'windows'; GOARCH = 'amd64'; GOFLAGS = '-mod=readonly'; GOMODCACHE = '<verified dependency cache>'; GOCACHE = '<private Q02 cache>'; GOTMPDIR = '<private Q02 temporary directory>' }
        commands = @('go version', 'go list -m -f={{.Path}} {{.Version}} all', 'go mod verify', 'go vet ./qualification/driver', 'go build -trimpath -buildvcs=false -o <private>/q02-driver.exe ./qualification/driver', '<private>/q02-driver.exe -runtime-root <private>/invalid-never-created -repeats 0 (expected exit 1, no fixture)', "<private>/q02-driver.exe -runtime-root <private>/fixtures -repeats $Repeats")
        verification = [ordered]@{ module_verify = 'PASS'; vet = 'PASS'; cgo_free_native_build = 'PASS'; invalid_repeat_bounds_fail_before_fixture_creation = 'PASS'; locks_unchanged = $true; full_runtime_options_match_frozen_source_inventory = $true }
        observed = $observed
        limitations = @('Synthetic single-host local NTFS behavior only.', 'Explicit SQL BEGIN IMMEDIATE/COMMIT/ROLLBACK on individually leased sql.Conn; no sql.Tx failure semantics or concurrent Raw usage tested.', 'DELETE mode pinned reader blocks commit until released; no simultaneous committed writes during export.', 'No race-detector claim: frozen CGO_ENABLED=0 was retained.', 'No product schema, admission, export archive, crash recovery, write/flush fault, power-loss, filesystem durability or performance qualification.', 'No cross-process lock or OS reboot qualification; isolation children are deadline watchdogs only.', 'Independent non-author review and owner acceptance remain separate gates.')
    }
    $json = $evidence | ConvertTo-Json -Depth 20
    [IO.File]::WriteAllText((Join-Path $privateRoot 'evidence.json'), $json.Replace("`r`n", "`n") + "`n", [Text.UTF8Encoding]::new($false))
    Write-Output 'PASS_ISOLATED_SCOPE; sanitized evidence.json written to private RuntimeRoot.'
}
finally {
    Set-Location $oldLocation
    foreach ($key in $settings) { [Environment]::SetEnvironmentVariable($key, $prior[$key], 'Process') }
}
