param(
  [string]$ServiceName = "LingmaIpcProxy",
  [string]$BinaryPath = "",
  [string]$Arguments = "--host 127.0.0.1 --port 8095 --session-mode auto",
  [string]$WorkingDirectory = "",
  [string]$WinSWExePath = "",
  [string]$TemplatePath = ""
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot

if ([string]::IsNullOrWhiteSpace($BinaryPath)) {
  $BinaryPath = Join-Path $repoRoot "dist\lingma-ipc-proxy.exe"
}
if ([string]::IsNullOrWhiteSpace($WorkingDirectory)) {
  $WorkingDirectory = $repoRoot
}
if ([string]::IsNullOrWhiteSpace($WinSWExePath)) {
  $WinSWExePath = Join-Path $repoRoot "dist\WinSW-x64.exe"
}
if ([string]::IsNullOrWhiteSpace($TemplatePath)) {
  $TemplatePath = Join-Path $PSScriptRoot "lingma-ipc-proxy.xml.template"
}

if (!(Test-Path $BinaryPath)) {
  throw "Binary not found: $BinaryPath"
}
if (!(Test-Path $WinSWExePath)) {
  throw "WinSW executable not found: $WinSWExePath"
}
if (!(Test-Path $TemplatePath)) {
  throw "WinSW template not found: $TemplatePath"
}

$serviceExePath = Join-Path $repoRoot "$ServiceName.exe"
$serviceXmlPath = Join-Path $repoRoot "$ServiceName.xml"

$xml = Get-Content -Raw $TemplatePath
$xml = $xml.Replace("__SERVICE_ID__", $ServiceName)
$xml = $xml.Replace("__SERVICE_NAME__", $ServiceName)
$xml = $xml.Replace("__SERVICE_DESCRIPTION__", "Lingma IPC proxy service")
$xml = $xml.Replace("__EXECUTABLE__", $BinaryPath)
$xml = $xml.Replace("__ARGUMENTS__", $Arguments)
$xml = $xml.Replace("__WORKDIR__", $WorkingDirectory)
$xml = $xml.Replace("__LOGDIR__", (Join-Path $repoRoot "logs"))

Copy-Item -Force $WinSWExePath $serviceExePath
Set-Content -Path $serviceXmlPath -Value $xml

Write-Host "Prepared WinSW service wrapper:"
Write-Host "  $serviceExePath"
Write-Host "  $serviceXmlPath"
Write-Host ""
Write-Host "Install with:"
Write-Host "  & `"$serviceExePath`" install"
Write-Host "Start with:"
Write-Host "  & `"$serviceExePath`" start"
