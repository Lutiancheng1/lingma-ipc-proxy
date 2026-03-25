param(
  [string]$OutputDir = "dist",
  [string]$BinaryName = "lingma-ipc-proxy.exe",
  [switch]$Clean
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$distDir = Join-Path $repoRoot $OutputDir
$binaryPath = Join-Path $distDir $BinaryName

if ($Clean -and (Test-Path $distDir)) {
  Remove-Item -Recurse -Force $distDir
}

New-Item -ItemType Directory -Force $distDir | Out-Null

Push-Location $repoRoot
try {
  $env:CGO_ENABLED = "0"
  $env:GOOS = "windows"
  $env:GOARCH = "amd64"

  Write-Host "Building $binaryPath"
  go build -trimpath -ldflags "-s -w" -o $binaryPath .\cmd\lingma-ipc-proxy
  Write-Host "Build completed: $binaryPath"
}
finally {
  Pop-Location
}
