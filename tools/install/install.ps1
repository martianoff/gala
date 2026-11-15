<#
.SYNOPSIS
Installs the GALA compiler on Windows from a GitHub release.

.DESCRIPTION
    irm https://raw.githubusercontent.com/martianoff/gala/master/tools/install/install.ps1 | iex

With options:

    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/martianoff/gala/master/tools/install/install.ps1))) -Version 0.101.0

Downloads gala-windows-amd64.exe and the release's checksums-sha256.txt,
refuses to install a binary whose SHA-256 does not match, installs it as
gala.exe, adds the install directory to the user PATH, and runs
`gala version`. Sends nothing anywhere except the downloads themselves.
GALA compiles through Go, so it then checks for Go 1.25+ and says where to
get it; it does not install Go.

Options (each also settable through the environment variable after it):
    -Version VERSION    release to install, default: the latest      GALA_VERSION
    -InstallDir DIR     install directory,                           GALA_INSTALL_DIR
                        default: %LOCALAPPDATA%\Programs\gala\bin
    -NoModifyPath       leave the user PATH alone                    GALA_NO_MODIFY_PATH=1

Other environment variables:
    GITHUB_TOKEN        sent to the GitHub API when resolving the latest release
    GALA_DOWNLOAD_BASE  base URL of the release downloads, for a mirror;
                        assets are fetched from $GALA_DOWNLOAD_BASE/VERSION/ASSET
#>

# Everything runs in a child scope, and the options are parsed from $args
# rather than a param block: under `irm | iex` the script shares the caller's
# scope, and neither its variables nor the preferences set below may leak
# into the user's session.
& {
# Errors are thrown, never `exit`: under `irm | iex` an exit would close the
# user's PowerShell window. Run as a file, an uncaught throw exits with 1.
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
# Invoke-WebRequest renders its progress bar so slowly in Windows PowerShell
# 5.1 that it dominates the download time.
$ProgressPreference = 'SilentlyContinue'
# Windows PowerShell 5.1 may default to TLS 1.0, which GitHub refuses.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$Version = $env:GALA_VERSION
$InstallDir = $env:GALA_INSTALL_DIR
$NoModifyPath = $env:GALA_NO_MODIFY_PATH -eq '1'
for ($i = 0; $i -lt $args.Count; $i++) {
    $option = "$($args[$i])"
    switch ($option) {
        { $_ -in '-Version', '-InstallDir' } {
            if ($i + 1 -ge $args.Count) { throw "gala install: error: $option needs a value" }
            $i++
            if ($option -eq '-Version') { $Version = "$($args[$i])" } else { $InstallDir = "$($args[$i])" }
        }
        '-NoModifyPath' { $NoModifyPath = $true }
        default { throw "gala install: error: unknown option '$option' (supported: -Version VERSION, -InstallDir DIR, -NoModifyPath)" }
    }
}

$repo = 'martianoff/gala'
$goMin = [version]'1.25'
$asset = 'gala-windows-amd64.exe'
$downloadBase = if ($env:GALA_DOWNLOAD_BASE) { $env:GALA_DOWNLOAD_BASE.TrimEnd('/') } else { "https://github.com/$repo/releases/download" }
if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\gala\bin' }
# An absolute path, as the user PATH needs: resolves ~, relative paths and a
# trailing separator.
$InstallDir = [IO.Path]::GetFullPath($ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($InstallDir)).TrimEnd('\')

# --- platform ---------------------------------------------------------------

# PROCESSOR_ARCHITEW6432 is set when a 32-bit PowerShell runs on 64-bit Windows.
$cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
switch ($cpu) {
    'AMD64' { }
    'ARM64' { Write-Host 'Windows on ARM: installing the amd64 build, which runs under emulation.' }
    default { throw "gala install: error: unsupported CPU '$cpu'; GALA ships a 64-bit Windows build only (build from source: https://github.com/$repo#quick-start)" }
}

# --- version ----------------------------------------------------------------

if (-not $Version) {
    try {
        $headers = @{ 'User-Agent' = 'gala-installer' }
        if ($env:GITHUB_TOKEN) { $headers['Authorization'] = "Bearer $env:GITHUB_TOKEN" }
        $Version = (Invoke-RestMethod -UseBasicParsing -Headers $headers "https://api.github.com/repos/$repo/releases/latest").tag_name
    } catch {
        # The API is rate-limited without a token; the release page redirect is not.
        try {
            $request = [Net.WebRequest]::Create("https://github.com/$repo/releases/latest")
            $request.Method = 'HEAD'
            $response = $request.GetResponse()
            try {
                if ($response.ResponseUri.AbsolutePath -match '/releases/tag/([^/]+)$') { $Version = $Matches[1] }
            } finally {
                $response.Close()
            }
        } catch { Write-Verbose "release redirect lookup failed: $_" }
    }
    if (-not $Version) {
        throw "gala install: error: could not determine the latest GALA release from GitHub; check your network, set GITHUB_TOKEN if you are rate-limited, or pass -Version (releases: https://github.com/$repo/releases)"
    }
}
# Release tags are bare versions (0.101.0); accept a v-prefixed one too.
$Version = $Version.Trim() -replace '^v', ''
if ($Version -notmatch '^[0-9A-Za-z.+-]+$') {
    throw "gala install: error: '$Version' is not a release version (expected something like 0.101.0)"
}

# --- download and verify ----------------------------------------------------

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("gala-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "Installing GALA $Version ($asset)"

    $checksumsFile = Join-Path $tmp 'checksums-sha256.txt'
    $binaryFile = Join-Path $tmp $asset
    try {
        Invoke-WebRequest -UseBasicParsing -OutFile $checksumsFile "$downloadBase/$Version/checksums-sha256.txt"
    } catch {
        throw "gala install: error: could not download checksums-sha256.txt for GALA $Version from $downloadBase/$Version/ ($($_.Exception.Message)); check that the release exists: https://github.com/$repo/releases"
    }
    try {
        Invoke-WebRequest -UseBasicParsing -OutFile $binaryFile "$downloadBase/$Version/$asset"
    } catch {
        throw "gala install: error: could not download $asset for GALA $Version from $downloadBase/$Version/ ($($_.Exception.Message)); check that the release exists and has this asset: https://github.com/$repo/releases/tag/$Version"
    }

    $expected = $null
    foreach ($line in Get-Content $checksumsFile) {
        $fields = -split $line
        if ($fields.Count -ge 2 -and ($fields[1] -eq $asset -or $fields[1] -eq "*$asset")) {
            $expected = $fields[0].ToLowerInvariant()
            break
        }
    }
    if (-not $expected) {
        throw "gala install: error: checksums-sha256.txt of GALA $Version has no entry for $asset; refusing to install an unverified binary"
    }
    $actual = (Get-FileHash -Algorithm SHA256 $binaryFile).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        throw "gala install: error: SHA-256 mismatch for ${asset}: expected $expected, got $actual. The download is corrupt or was tampered with; nothing was installed. Try again, and report it at https://github.com/$repo/issues if it persists"
    }
    Write-Host "Verified SHA-256 $actual"

    # --- install ------------------------------------------------------------

    try {
        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    } catch {
        throw "gala install: error: cannot create $InstallDir; pick a writable directory with -InstallDir"
    }
    $target = Join-Path $InstallDir 'gala.exe'
    try {
        Move-Item -Force -Path $binaryFile -Destination $target
    } catch {
        throw "gala install: error: cannot write $target ($($_.Exception.Message)). If gala.exe is running (an editor's language server, for example), close it and run this again"
    }
} finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
}

Write-Host "Installed $target"
& $target version
if ($LASTEXITCODE -ne 0) {
    throw "gala install: error: $target was installed but 'gala version' failed; please report it at https://github.com/$repo/issues"
}

# --- PATH -------------------------------------------------------------------

function Test-PathEntry([string]$PathValue, [string]$Dir) {
    $want = $Dir.TrimEnd('\')
    foreach ($entry in ($PathValue -split ';')) {
        if ($entry -and [Environment]::ExpandEnvironmentVariables($entry).TrimEnd('\') -ieq $want) { return $true }
    }
    return $false
}

if ($NoModifyPath) {
    if (-not (Test-PathEntry $env:Path $InstallDir)) {
        Write-Host ""
        Write-Host "$InstallDir is not on your PATH (left unchanged: -NoModifyPath)."
    }
} else {
    # Edit the registry value directly: [Environment]::SetEnvironmentVariable
    # would rewrite a REG_EXPAND_SZ Path as REG_SZ and freeze every %VAR% in it.
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    try {
        $userPath = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        if (Test-PathEntry $userPath $InstallDir) {
            Write-Host "$InstallDir is already on your user PATH."
        } else {
            $kind = if ($userPath) { $key.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
            $newPath = if ($userPath) { $userPath.TrimEnd(';') + ';' + $InstallDir } else { $InstallDir }
            $key.SetValue('Path', $newPath, $kind)
            # Setting any user variable through .NET broadcasts WM_SETTINGCHANGE,
            # so terminals opened from now on see the new PATH.
            [Environment]::SetEnvironmentVariable('GALA_INSTALLER_REFRESH', '1', 'User')
            [Environment]::SetEnvironmentVariable('GALA_INSTALLER_REFRESH', $null, 'User')
            Write-Host "Added $InstallDir to your user PATH; open a new terminal to use it."
        }
    } finally {
        $key.Close()
    }
    if (-not (Test-PathEntry $env:Path $InstallDir)) { $env:Path = "$env:Path;$InstallDir" }
}

# --- Go ---------------------------------------------------------------------

Write-Host ""
$goHint = "GALA compiles programs through Go, so it needs Go $goMin+ on your PATH: https://go.dev/dl/ (or: winget install GoLang.Go / scoop install go)"
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "Go was not found. $goHint"
} else {
    # 2>$null under ErrorActionPreference Stop turns any stderr line into a throw.
    $goVersionOutput = try { & go version 2>$null } catch { "" }
    if ("$goVersionOutput" -match 'go(\d+)\.(\d+)') {
        $goVersion = [version]"$($Matches[1]).$($Matches[2])"
        if ($goVersion -lt $goMin) {
            Write-Host "Found Go $goVersion, which is too old. $goHint"
        } else {
            Write-Host "Found Go $goVersion"
        }
    } else {
        Write-Host "Could not read the version of the installed Go ('go version' failed). $goHint"
    }
}

Write-Host ""
Write-Host "Done. Get started: gala new hello; cd hello; gala run main.gala"
} @args
