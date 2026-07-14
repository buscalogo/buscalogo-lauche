# BuscaLogo Agent — build MSI (Windows)
#
# Prerequisites:
#   - WiX Toolset v4 or v5:  winget install WiXToolset.WiX
#   - Staging folder: run `make msi-stage` (Linux or Windows) first,
#     or this script will expect ..\..\dist\msi-stage
#
# Usage (from repo root on Windows PowerShell):
#   .\packaging\windows\build.ps1
#   .\packaging\windows\build.ps1 -Version 0.1.14

param(
    [string]$Version = "",
    [string]$StageDir = "",
    [string]$OutDir = ""
)

$ErrorActionPreference = "Stop"
$Root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
if (-not $Version) {
    $verFile = Join-Path $Root "VERSION"
    if (Test-Path $verFile) {
        $Version = (Get-Content $verFile -Raw).Trim()
    } else {
        $Version = "0.1.0"
    }
}
# WiX requires up to 4 numeric parts
if ($Version -notmatch '^\d+\.\d+\.\d+') {
    throw "VERSION must look like x.y.z (got '$Version')"
}

if (-not $StageDir) { $StageDir = Join-Path $Root "dist\msi-stage" }
if (-not $OutDir) { $OutDir = Join-Path $Root "dist" }

$Required = @(
    "buscalogo-agentd.exe",
    "buscalogo-agent.exe",
    "resources.neu",
    "trayIcon.png",
    "wintun.dll"
)
foreach ($f in $Required) {
    $p = Join-Path $StageDir $f
    if (-not (Test-Path $p)) {
        throw "Missing staging file: $p`nRun: make msi-stage"
    }
}

# Copy stage next to wxs so Source paths msi-stage\... resolve
$PackDir = $PSScriptRoot
$LocalStage = Join-Path $PackDir "msi-stage"
if (Test-Path $LocalStage) { Remove-Item -Recurse -Force $LocalStage }
New-Item -ItemType Directory -Path $LocalStage | Out-Null
Copy-Item (Join-Path $StageDir "*") $LocalStage -Force

$wix = Get-Command wix -ErrorAction SilentlyContinue
if (-not $wix) {
    throw "WiX CLI 'wix' not found. Install WiX Toolset v4+: winget install WiXToolset.WiX"
}

$OutMsi = Join-Path $OutDir "BuscaLogoAgent-$Version-amd64.msi"
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

Push-Location $PackDir
try {
    & wix build `
        .\BuscaLogo.wxs `
        -d Version=$Version `
        -o $OutMsi
    if ($LASTEXITCODE -ne 0) { throw "wix build failed ($LASTEXITCODE)" }
} finally {
    Pop-Location
}

Write-Host ">> MSI: $OutMsi"
Write-Host ">> Install as Admin. Service: BuscaLogoAgent. Data: %ProgramData%\BuscaLogo"
Write-Host ">> Optional: signtool sign /fd SHA256 /a `"$OutMsi`""
