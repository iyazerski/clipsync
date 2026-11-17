# Install the latest clipsync release into %LOCALAPPDATA%\Programs\clipsync.
# Set $env:CLIPSYNC_VERSION = "vX.Y.Z" to pin a release.
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$repo = "iyazerski/clipsync"
$asset = "clipsync-x86_64-pc-windows-msvc.zip"
$binaries = "clipsync.exe", "clipsyncw.exe"
$dir = Join-Path $env:LOCALAPPDATA "Programs\clipsync"
$runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"

if ($env:CLIPSYNC_VERSION) {
  $downloadUrl = "https://github.com/$repo/releases/download/$env:CLIPSYNC_VERSION/$asset"
} else {
  $downloadUrl = "https://github.com/$repo/releases/latest/download/$asset"
}
$installTmp = Join-Path ([IO.Path]::GetTempPath()) "clipsync-$([guid]::NewGuid())"
New-Item -ItemType Directory $installTmp | Out-Null

try {
  Write-Host "Downloading $asset..."
  Invoke-WebRequest $downloadUrl -OutFile "$installTmp\$asset" -UseBasicParsing
  Invoke-WebRequest "$downloadUrl.sha256" -OutFile "$installTmp\$asset.sha256" -UseBasicParsing
  $expected = (Get-Content "$installTmp\$asset.sha256" -Raw).Trim().Split()[0]
  if ((Get-FileHash "$installTmp\$asset" -Algorithm SHA256).Hash -ne $expected) {
    throw "the download doesn't match its checksum; try again"
  }
  Expand-Archive "$installTmp\$asset" -DestinationPath "$installTmp\unpacked"

  # A running service keeps its binary locked: stop it first, then start it
  # again on the new one.
  $service = Get-ItemProperty $runKey -Name clipsync -ErrorAction SilentlyContinue
  if ($service) {
    $session = (Get-Process -Id $PID).SessionId
    Get-Process clipsyncw -ErrorAction SilentlyContinue |
      Where-Object SessionId -eq $session |
      Stop-Process -Force -PassThru |
      Wait-Process
  }

  New-Item -ItemType Directory -Force $dir | Out-Null
  foreach ($name in $binaries) {
    Copy-Item "$installTmp\unpacked\$name" (Join-Path $dir $name) -Force
  }
} finally {
  Remove-Item -Recurse -Force $installTmp
}

try {
  $version = & "$dir\clipsync.exe" --version
} catch {
  # Defender's ransomware rule blocks unsigned binaries without a reputation yet.
  throw "Windows didn't let clipsync run ($_). If Microsoft Defender blocked it, see https://github.com/$repo#if-defender-blocks-clipsync"
}
Write-Host "$version is installed to $dir"

if ($service) {
  Start-Process "$dir\clipsyncw.exe" -ArgumentList service
  Write-Host "restarted the background service"
}

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($userPath -split ";") -notcontains $dir) {
  [Environment]::SetEnvironmentVariable("Path", (@($userPath, $dir) | Where-Object { $_ }) -join ";", "User")
  Write-Host "Added $dir to your PATH. Open a new terminal to use clipsync."
}
