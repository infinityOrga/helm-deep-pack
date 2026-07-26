$ErrorActionPreference = "Stop"

$Repo = "infinityOrga/helm-deep-pack"
$Binary = "helm-deep-pack.exe"
$InstallDir = if ($env:HELM_DEEP_PACK_INSTALL) { $env:HELM_DEEP_PACK_INSTALL } else { Join-Path $env:LOCALAPPDATA "Programs\helm-deep-pack\bin" }
$Version = if ($env:HELM_DEEP_PACK_VERSION) { $env:HELM_DEEP_PACK_VERSION } else { "latest" }

function Write-Info {
    param([string]$Message)
    Write-Host $Message
}

function Get-Arch {
    switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
        "X64" { "amd64" }
        "Arm64" { "arm64" }
        default { throw "Unsupported architecture: $([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture)" }
    }
}

function Get-ReleaseAssetUrl {
    param(
        [object]$Release,
        [string]$Name
    )
    ($Release.assets | Where-Object { $_.name -eq $Name } | Select-Object -First 1).browser_download_url
}

function Find-ReleaseAsset {
    param(
        [object]$Release,
        [string]$Pattern
    )
    $Release.assets | Where-Object { $_.name -match $Pattern } | Select-Object -First 1
}

function Get-ChecksumLine {
    param(
        [string]$ChecksumsPath,
        [string]$AssetName
    )
    Get-Content -Path $ChecksumsPath | Where-Object { $_ -match ("  " + [Regex]::Escape($AssetName) + "$") } | Select-Object -First 1
}

function Add-ToUserPathIfMissing {
    param([string]$PathToAdd)

    $current = [Environment]::GetEnvironmentVariable("Path", "User")
    if ([string]::IsNullOrWhiteSpace($current)) {
        [Environment]::SetEnvironmentVariable("Path", $PathToAdd, "User")
        return $true
    }

    $parts = $current.Split(";") | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne "" }
    if ($parts -contains $PathToAdd) {
        return $false
    }

    $newPath = ($parts + $PathToAdd) -join ";"
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    return $true
}

$Arch = Get-Arch
$ApiUrl = "https://api.github.com/repos/$Repo/releases/latest"
if ($Version -ne "latest") {
    $ApiUrl = "https://api.github.com/repos/$Repo/releases/tags/$Version"
}

$release = Invoke-RestMethod -Uri $ApiUrl
$tag = if ($release.tag_name) { $release.tag_name } else { $Version }
$archiveAsset = Find-ReleaseAsset -Release $release -Pattern ("^helm-deep-pack_.+_windows_{0}\.zip$" -f [Regex]::Escape($Arch))
if (-not $archiveAsset) {
    throw "No release archive found for windows/$Arch"
}
$archiveName = $archiveAsset.name
$archiveUrl = $archiveAsset.browser_download_url

$checksumsUrl = Get-ReleaseAssetUrl -Release $release -Name "checksums.txt"

$tmpDir = Join-Path ([IO.Path]::GetTempPath()) ("helm-deep-pack-install-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmpDir | Out-Null
try {
    $archivePath = Join-Path $tmpDir $archiveName
    $extractDir = Join-Path $tmpDir "extract"

    Write-Info "Installing helm-deep-pack ($tag) for windows/$Arch..."
    Invoke-WebRequest -Uri $archiveUrl -OutFile $archivePath

    if ($checksumsUrl) {
        $checksumsPath = Join-Path $tmpDir "checksums.txt"
        Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksumsPath

        $line = Get-ChecksumLine -ChecksumsPath $checksumsPath -AssetName $archiveName
        if (-not $line) {
            throw "Checksum entry not found for $archiveName"
        }

        $expected = ($line -split "\s+")[0].ToLowerInvariant()
        $actual = (Get-FileHash -Algorithm SHA256 -Path $archivePath).Hash.ToLowerInvariant()
        if ($expected -ne $actual) {
            throw "Checksum verification failed"
        }
    }

    Expand-Archive -Path $archivePath -DestinationPath $extractDir -Force

    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Copy-Item -Path (Join-Path $extractDir $Binary) -Destination (Join-Path $InstallDir $Binary) -Force

    Write-Info "Installed to $(Join-Path $InstallDir $Binary)"
    if (Add-ToUserPathIfMissing -PathToAdd $InstallDir) {
        Write-Info "Added $InstallDir to your user PATH."
        Write-Info "Restart your terminal, then run: helm-deep-pack --help"
    } else {
        Write-Info "Run: helm-deep-pack --help"
    }
}
finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
