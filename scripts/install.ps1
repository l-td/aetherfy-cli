# Aetherfy CLI Installer -- Windows
#
# Usage:
#   irm https://aetherfy.com/install.ps1 | iex
#
# THIS FILE IS SERVED AT THAT URL. aetherfy.com/install.ps1 is a temporary (307)
# redirect to this file's raw copy on main, configured in the OTHER repository:
#   aetherfy-dashboard:landing/next.config.js  (redirects(), source '/install.ps1')
# There is no second copy -- that redirect points here, so editing this file
# changes what users run, on the next push to main and with no deploy.
#
# Windows only, and it exists BECAUSE scripts/install.sh refuses Windows: Git
# Bash has no sudo and no /usr/local/bin, and it would strip the .exe. This is
# the Windows half of the same contract -- same asset names, same
# /releases/latest/download URLs, same fail-closed checksum rule.
#
# No releases are tagged yet, so the download below has nothing to fetch and
# fails loudly with the URL it tried. That is deliberate and expires with the
# first tag.
#
# Environment variables:
#   AETHERFY_INSTALL_DIR - Installation directory
#                          (default: %LOCALAPPDATA%\Programs\afy)
#   AETHERFY_VERSION     - Version to install (default: latest).
#                          Accepts "0.1.0" or "v0.1.0"; both resolve to the
#                          same release.

$ErrorActionPreference = 'Stop'

# Everything lives in a function so that a failure `throw`s instead of calling
# `exit`. Under `irm ... | iex` the script shares the user's session, and `exit`
# there closes their PowerShell window. `powershell -File install.ps1` still
# returns a non-zero exit code on an unhandled throw, so CI is unaffected.
function Install-AetherfyCli {
    $BinaryName   = 'afy'
    $BinaryFile   = 'afy.exe'          # the name goreleaser puts INSIDE the zip
    $GitHubRepo   = 'l-td/aetherfy-cli'
    $ChecksumFile = 'checksums.txt'

    if ($PSVersionTable.PSVersion.Major -lt 5) {
        throw "PowerShell 5.1 or newer is required (found $($PSVersionTable.PSVersion))."
    }

    # Windows PowerShell 5.1 still negotiates TLS 1.0 by default on some hosts;
    # github.com refuses it.
    [Net.ServicePointManager]::SecurityProtocol = `
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    Write-Host ''
    Write-Host '  Aetherfy CLI Installer' -ForegroundColor Green
    Write-Host ''

    # ---- platform ----------------------------------------------------------
    # .goreleaser.yaml's build matrix `ignore`s windows/arm64, so no asset is
    # ever published for it. Say that, rather than building a URL that 404s
    # with nothing in the message to explain why.
    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }

    switch ($arch) {
        'AMD64' { $goarch = 'amd64' }
        default {
            throw ("Unsupported architecture: $arch. The Aetherfy CLI publishes " +
                   "windows/amd64 only. On ARM64 Windows, x64 emulation runs the " +
                   "amd64 build -- or build from source (see the README).")
        }
    }
    $platform = "windows-$goarch"
    Write-Host "Detected platform: $platform" -ForegroundColor Cyan

    # ---- release URL -------------------------------------------------------
    # Deliberately no api.github.com call: the /releases/latest/download
    # redirect serves the newest asset directly, which costs neither the
    # anonymous rate limit (60/hr/IP) nor a JSON parse that can only fail
    # silently. Same reasoning scripts/install.sh documents.
    $version = $env:AETHERFY_VERSION
    if (-not $version) { $version = 'latest' }
    $version = $version -replace '^v', ''      # tags are vX.Y.Z; accept either

    if ($version -eq 'latest') {
        $urlPrefix = "https://github.com/$GitHubRepo/releases/latest/download"
        Write-Host 'Installing version: latest' -ForegroundColor Cyan
    } else {
        $urlPrefix = "https://github.com/$GitHubRepo/releases/download/v$version"
        Write-Host "Installing version: v$version" -ForegroundColor Cyan
    }

    $asset       = "$BinaryName-$platform.zip"
    $assetUrl    = "$urlPrefix/$asset"
    $checksumUrl = "$urlPrefix/$ChecksumFile"

    $installDir = $env:AETHERFY_INSTALL_DIR
    if (-not $installDir) {
        $installDir = Join-Path $env:LOCALAPPDATA "Programs\$BinaryName"
    }

    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("afy-install-" + [System.Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null

    try {
        # ---- download ------------------------------------------------------
        # Name the URL on failure. A 404 here -- wrong tag, asset renamed, no
        # release published yet -- is the likeliest way this ever fails, and a
        # bare WebException says nothing about which file was missing.
        $archivePath  = Join-Path $tmp $asset
        $checksumPath = Join-Path $tmp $ChecksumFile

        Write-Host "Downloading from: $assetUrl" -ForegroundColor Cyan
        Get-Remote -Url $assetUrl    -OutFile $archivePath
        Get-Remote -Url $checksumUrl -OutFile $checksumPath

        # ---- verify BEFORE extracting, fail closed -------------------------
        Write-Host 'Verifying checksum...' -ForegroundColor Cyan

        $expected = $null
        foreach ($line in Get-Content -LiteralPath $checksumPath) {
            $parts = $line -split '\s+', 2
            if ($parts.Count -eq 2 -and $parts[1].Trim() -eq $asset) {
                $expected = $parts[0].Trim().ToLower()
                break
            }
        }
        # A missing line must never read as success -- that is the whole point of
        # verifying. Refuse rather than install something unlisted.
        if (-not $expected) {
            throw "$asset is not listed in $ChecksumFile. Refusing to install an unverified binary."
        }

        $actual = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLower()
        if ($actual -ne $expected) {
            throw ("Checksum verification failed for $asset.`n" +
                   "  expected: $expected`n" +
                   "  actual:   $actual")
        }

        # ---- extract -------------------------------------------------------
        $extractDir = Join-Path $tmp 'unpacked'
        Expand-Archive -LiteralPath $archivePath -DestinationPath $extractDir -Force

        $binarySrc = Join-Path $extractDir $BinaryFile
        if (-not (Test-Path -LiteralPath $binarySrc)) {
            throw "$BinaryFile not found at the root of $asset."
        }

        # ---- install -------------------------------------------------------
        if (-not (Test-Path -LiteralPath $installDir)) {
            New-Item -ItemType Directory -Path $installDir -Force | Out-Null
        }
        $binaryDst = Join-Path $installDir $BinaryFile

        Write-Host "Installing to $binaryDst..." -ForegroundColor Cyan
        try {
            Copy-Item -LiteralPath $binarySrc -Destination $binaryDst -Force
        } catch [System.IO.IOException] {
            throw ("Could not write $binaryDst -- the file is in use.`n" +
                   "Close any running afy (and any shell running it) and try again.")
        } catch [System.UnauthorizedAccessException] {
            throw ("Could not write to $installDir -- permission denied.`n" +
                   "Choose a writable location instead:`n" +
                   "  `$env:AETHERFY_INSTALL_DIR = `"`$env:LOCALAPPDATA\Programs\afy`"")
        }

        # ---- PATH ----------------------------------------------------------
        # User PATH only: no elevation, and it survives a reboot. Idempotent --
        # re-running the installer must not append a duplicate entry.
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        if (-not $userPath) { $userPath = '' }
        $onPath = $userPath.Split(';') | Where-Object { $_.TrimEnd('\') -ieq $installDir.TrimEnd('\') }

        if (-not $onPath) {
            $newPath = if ($userPath.TrimEnd(';')) { $userPath.TrimEnd(';') + ';' + $installDir } else { $installDir }
            [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
            $pathWasAdded = $true
        } else {
            $pathWasAdded = $false
        }
        # Make afy runnable in THIS session too, so the verify below and the
        # user's next command both work without reopening a terminal.
        if ($env:Path -notlike "*$installDir*") {
            $env:Path = "$env:Path;$installDir"
        }

        # ---- verify --------------------------------------------------------
        # Success is what the binary we just installed reports, by its full
        # path. Get-Command would answer about some other afy already on PATH.
        Write-Host ''
        & $binaryDst version
        if ($LASTEXITCODE -ne 0) {
            throw "$binaryDst did not run (exit $LASTEXITCODE)."
        }

        Write-Host ''
        Write-Host 'Aetherfy CLI installed successfully.' -ForegroundColor Green
        Write-Host ''
        Write-Host 'Get started:' -ForegroundColor Cyan
        Write-Host '  afy login              # Authenticate with your API key'
        Write-Host '  afy agents list        # List your agents'
        Write-Host '  afy deploy             # Deploy your agent'
        Write-Host ''
        if ($pathWasAdded) {
            Write-Host "Added $installDir to your user PATH." -ForegroundColor Yellow
            Write-Host 'Open a new terminal for it to take effect there.' -ForegroundColor Yellow
            Write-Host ''
        }
        Write-Host 'Documentation: https://docs.aetherfy.com' -ForegroundColor Cyan
    }
    finally {
        # Every exit path, including the throws above.
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Get-Remote {
    param(
        [Parameter(Mandatory = $true)][string] $Url,
        [Parameter(Mandatory = $true)][string] $OutFile
    )
    try {
        # -UseBasicParsing: Windows PowerShell 5.1 otherwise routes through the
        # Internet Explorer engine, which is absent on Server Core and on hosts
        # where IE has been removed.
        Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing
    } catch {
        throw "download failed: $Url`n  $($_.Exception.Message)"
    }
}

Install-AetherfyCli
