# Install zellij-reaper on Windows from a GitHub release and start its tasks.
#
#   irm https://github.com/kanggu12/zellij-reaper/releases/latest/download/install.ps1 | iex
#
# Settings, all optional, as environment variables:
#   ZELLIJ_REAPER_VERSION   release to install, like v0.6.0 (default: the latest)
#   ZELLIJ_REAPER_BIN_DIR   where the binary goes (default: %LOCALAPPDATA%\Programs\zellij-reaper)
#   ZELLIJ_REAPER_NO_TIMER  set to 1 to install the binary only
#   ZELLIJ_REAPER_BASE_URL  where releases are downloaded from (mirrors, tests)
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 speaks only older TLS unless told otherwise.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$repo = 'kanggu12/zellij-reaper'
$base = if ($env:ZELLIJ_REAPER_BASE_URL) { $env:ZELLIJ_REAPER_BASE_URL } else { "https://github.com/$repo/releases/download" }
$binDir = if ($env:ZELLIJ_REAPER_BIN_DIR) { $env:ZELLIJ_REAPER_BIN_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\zellij-reaper' }

function Stop-Install([string]$message) {
  Write-Host "zellij-reaper install: $message"
  exit 1
}

# zellij has a Windows build for x64 only, and so does zellij-reaper.
if ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64' -and $env:PROCESSOR_ARCHITEW6432 -ne 'AMD64') {
  Stop-Install "no release for $env:PROCESSOR_ARCHITECTURE"
}

# The latest release is where github.com/<repo>/releases/latest redirects.
$version = $env:ZELLIJ_REAPER_VERSION
if (-not $version) {
  try {
    $req = [Net.WebRequest]::Create("https://github.com/$repo/releases/latest")
    $req.Method = 'HEAD'
    $resp = $req.GetResponse()
    $url = $resp.ResponseUri.AbsoluteUri
    $resp.Close()
  } catch {
    Stop-Install 'could not find the latest release; set ZELLIJ_REAPER_BASE_URL and ZELLIJ_REAPER_VERSION on a network that cannot reach GitHub'
  }
  if ($url -notmatch '/releases/tag/(.+)$') { Stop-Install 'no release published yet; set ZELLIJ_REAPER_VERSION' }
  $version = $Matches[1]
}
if (-not $version.StartsWith('v')) { $version = "v$version" }

$archive = "zellij-reaper_$($version.Substring(1))_windows_amd64.zip"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("zellij-reaper-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "downloading zellij-reaper $version for windows/amd64"
  $wc = New-Object Net.WebClient
  try {
    $wc.DownloadFile("$base/$version/$archive", (Join-Path $tmp $archive))
    $wc.DownloadFile("$base/$version/checksums.txt", (Join-Path $tmp 'checksums.txt'))
  } catch {
    Stop-Install "could not download $archive"
  }
  # The release's checksums: Get-FileHash is missing from some PowerShell
  # setups, so the hash is computed directly.
  $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match (' ' + [regex]::Escape($archive) + '$') } | Select-Object -First 1
  if (-not $line) { Stop-Install "$archive is not in checksums.txt" }
  $want = ($line -split ' ')[0]
  $sha = [Security.Cryptography.SHA256]::Create()
  $stream = [IO.File]::OpenRead((Join-Path $tmp $archive))
  try { $got = -join ($sha.ComputeHash($stream) | ForEach-Object { $_.ToString('x2') }) } finally { $stream.Close() }
  if ($got -ne $want) { Stop-Install "checksum mismatch for $archive" }

  Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'x') -Force
  New-Item -ItemType Directory -Path $binDir -Force | Out-Null
  Copy-Item (Join-Path $tmp 'x\zellij-reaper.exe') (Join-Path $binDir 'zellij-reaper.exe') -Force
  Write-Host "installed $(Join-Path $binDir 'zellij-reaper.exe')"
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

# The user's PATH gets the directory once, for shells opened from now on.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $binDir)) {
  [Environment]::SetEnvironmentVariable('Path', ((@($userPath, $binDir) | Where-Object { $_ }) -join ';'), 'User')
  Write-Host "added $binDir to your PATH; open a new terminal to run zellij-reaper by name"
}

if ($env:ZELLIJ_REAPER_NO_TIMER -eq '1') {
  Write-Host "run 'zellij-reaper install' to start the tasks"
} else {
  & (Join-Path $binDir 'zellij-reaper.exe') install
}
