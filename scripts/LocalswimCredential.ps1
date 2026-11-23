#Requires -Version 5.1
# The launch credential interface in docs/windows-operations.md, "The service receipt and
# credential files": writes and reads human.json and bot.json. Dot-source this file; it only
# defines functions. It works in PowerShell 7 and Windows PowerShell 5.1.

# Creates a new credential folder under $Root with fresh, distinct Human and Bot tokens, and
# returns its path. Each token is 32 random bytes in unpadded base64url, saved with Windows DPAPI
# in user scope, so only this Windows user on this PC can read it.
function New-LocalswimCredentialFolder([Parameter(Mandatory)][string]$Root) {
    $ErrorActionPreference = 'Stop'
    $folder = Join-Path $Root ([guid]::NewGuid().ToString())
    New-Item -ItemType Directory -Path $folder -Force | Out-Null
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        foreach ($role in 'human', 'bot') {
            $bytes = [byte[]]::new(32)
            $rng.GetBytes($bytes)
            $token = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
            $secret = ConvertTo-SecureString -String $token -AsPlainText -Force
            $credential = [ordered]@{
                format          = 'localswim-credential'
                format_version  = 1
                role            = $role
                token_dpapi_hex = ConvertFrom-SecureString -SecureString $secret
            }
            [IO.File]::WriteAllText((Join-Path $folder "$role.json"),
                ($credential | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
        }
    }
    finally {
        $rng.Dispose()
        Remove-Variable token, secret, credential -ErrorAction SilentlyContinue
    }
    $folder
}

# Returns a role's token from a credential folder, refusing any file the interface forbids. It
# never includes the token in an error.
function Read-LocalswimToken([Parameter(Mandatory)][string]$CredentialDirectory,
                             [Parameter(Mandatory)][ValidateSet('human', 'bot')][string]$Role) {
    $ErrorActionPreference = 'Stop'
    $file = Join-Path $CredentialDirectory "$Role.json"
    $text = [IO.File]::ReadAllText($file)
    $credential = $text | ConvertFrom-Json
    $names = @($credential.PSObject.Properties.Name)
    $expected = 'format', 'format_version', 'role', 'token_dpapi_hex'
    $version = $credential.format_version
    if ($text -notmatch '^\s*\{' -or $names.Count -ne 4 -or
        @($names | Where-Object { $_ -cnotin $expected }).Count -ne 0 -or
        $credential.format -cne 'localswim-credential' -or
        -not ($version -is [int] -or $version -is [long]) -or $version -ne 1 -or
        $credential.role -cne $Role) {
        throw "Unsupported credential format in $file."
    }
    $token = [Net.NetworkCredential]::new('', (ConvertTo-SecureString -String $credential.token_dpapi_hex)).Password
    if ($token -cnotmatch '^[A-Za-z0-9_-]{43}\z') {
        throw "The token in $file is not 43 base64url characters."
    }
    $token
}
