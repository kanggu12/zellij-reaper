# Install zellij-reaper on Windows from a GitHub release and start its task.
#
#   irm https://github.com/kanggu12/zellij-reaper/releases/latest/download/install.ps1 | iex
#
# Settings, all optional, as environment variables:
#   ZELLIJ_REAPER_VERSION   release to install, like v0.6.0 (default: the latest)
#   ZELLIJ_REAPER_BIN_DIR   where the binary goes (default: %LOCALAPPDATA%\Programs\zellij-reaper)
#   ZELLIJ_REAPER_NO_TIMER  set to 1 to install the binary only
#   ZELLIJ_REAPER_NO_SETUP  set to 1 to start the task with the defaults, without the
#                           setup screen a first install in a console gets
#   ZELLIJ_REAPER_BASE_URL  where releases are downloaded from (mirrors, tests)
#
# Everything runs in a script block of its own: run through iex, the script
# shares the caller's session, and a failure must end the install, not the
# person's PowerShell window, nor leave settings like ErrorActionPreference
# behind.
& {
  $ErrorActionPreference = 'Stop'
  # Windows PowerShell 5.1 speaks only older TLS unless told otherwise.
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

  $repo = 'kanggu12/zellij-reaper'
  $base = if ($env:ZELLIJ_REAPER_BASE_URL) { $env:ZELLIJ_REAPER_BASE_URL } else { "https://github.com/$repo/releases/download" }
  $binDir = if ($env:ZELLIJ_REAPER_BIN_DIR) { $env:ZELLIJ_REAPER_BIN_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\zellij-reaper' }

  # zellij has a Windows build for x64 only, and so does zellij-reaper.
  if ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64' -and $env:PROCESSOR_ARCHITEW6432 -ne 'AMD64') {
    throw "zellij-reaper install: no release for $env:PROCESSOR_ARCHITECTURE"
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
      throw 'zellij-reaper install: could not find the latest release; on a network that cannot reach GitHub, set ZELLIJ_REAPER_BASE_URL and ZELLIJ_REAPER_VERSION'
    }
    if ($url -notmatch '/releases/tag/(.+)$') { throw 'zellij-reaper install: no release published yet; set ZELLIJ_REAPER_VERSION' }
    $version = $Matches[1]
  }
  if (-not $version.StartsWith('v')) { $version = "v$version" }

  $archive = "zellij-reaper_$($version.Substring(1))_windows_amd64.zip"
  $tmp = Join-Path ([IO.Path]::GetTempPath()) ('zellij-reaper-' + [Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    Write-Host "downloading zellij-reaper $version for windows/amd64"
    $wc = New-Object Net.WebClient
    try {
      $wc.DownloadFile("$base/$version/$archive", (Join-Path $tmp $archive))
      $wc.DownloadFile("$base/$version/checksums.txt", (Join-Path $tmp 'checksums.txt'))
    } catch {
      throw "zellij-reaper install: could not download $archive"
    }
    # The release's checksums. Get-FileHash is missing from some PowerShell
    # setups, so the hash is computed directly.
    $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match (' ' + [regex]::Escape($archive) + '$') } | Select-Object -First 1
    if (-not $line) { throw "zellij-reaper install: $archive is not in checksums.txt" }
    $want = ($line -split ' ')[0]
    $sha = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead((Join-Path $tmp $archive))
    try { $got = -join ($sha.ComputeHash($stream) | ForEach-Object { $_.ToString('x2') }) } finally { $stream.Close() }
    if ($got -ne $want) { throw "zellij-reaper install: checksum mismatch for $archive" }

    Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'x') -Force
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
    $exe = Join-Path $binDir 'zellij-reaper.exe'
    # Copies an earlier upgrade moved aside, once nothing runs them any more.
    Get-ChildItem -Path $binDir -Filter 'zellij-reaper.exe.*.old' -ErrorAction SilentlyContinue | Remove-Item -Force -ErrorAction SilentlyContinue
    # Windows writes no file over a program that is running (the dashboard,
    # or the task at that moment), but it moves one: the running copy goes on
    # under its new name, and the next install removes it.
    if (Test-Path $exe) {
      $aside = "$exe.$([DateTime]::Now.ToString('yyyyMMddHHmmss')).old"
      Move-Item $exe $aside -Force
      Copy-Item (Join-Path $tmp 'x\zellij-reaper.exe') $exe -Force
      Remove-Item $aside -Force -ErrorAction SilentlyContinue
    } else {
      Copy-Item (Join-Path $tmp 'x\zellij-reaper.exe') $exe -Force
    }
    Write-Host "installed $exe"
  } finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
  }

  # The user's PATH gets the folder once, for terminals opened from now on. It
  # is read and written as it is stored, so entries like %USERPROFILE%\bin
  # keep their variables. A PATH stored as a plain string stays one; any
  # other, or a new one, is one Windows expands.
  $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
  try {
    $userPath = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
    if ($null -ne $key.GetValue('Path') -and $key.GetValueKind('Path') -eq [Microsoft.Win32.RegistryValueKind]::String) {
      $kind = [Microsoft.Win32.RegistryValueKind]::String
    }
    $entries = @($userPath -split ';' | Where-Object { $_ })
    if (-not ($entries | Where-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') -eq $binDir.TrimEnd('\') })) {
      $key.SetValue('Path', (@($entries + $binDir) -join ';'), $kind)
      # Tell Windows the environment changed, so new terminals see it: setting
      # a variable through .NET sends that message.
      [Environment]::SetEnvironmentVariable('ZELLIJ_REAPER_INSTALLING', '1', 'User')
      [Environment]::SetEnvironmentVariable('ZELLIJ_REAPER_INSTALLING', $null, 'User')
      Write-Host "added $binDir to your PATH; open a new terminal to run zellij-reaper by name"
    }
  } finally {
    $key.Close()
  }

  $exe = Join-Path $binDir 'zellij-reaper.exe'
  if ($env:ZELLIJ_REAPER_NO_TIMER -eq '1') {
    Write-Host "run 'zellij-reaper install' to start the task"
    return
  }
  if ($env:ZELLIJ_REAPER_NO_SETUP -ne '1' -and -not [Console]::IsInputRedirected -and -not [Console]::IsOutputRedirected) {
    # In a console: on a first install the setup screen, which shows what the
    # first pass would reap; on an upgrade only the task is written again.
    & $exe setup --installer
  } else {
    & $exe install
  }
  # A program's failing does not stop a script, even with
  # ErrorActionPreference at Stop: its status is read. The task did not start
  # (no Task Scheduler answered, or ctrl+c on the setup screen), for the
  # reason the program gave above, and the install is not finished.
  if ($LASTEXITCODE -ne 0) {
    throw "zellij-reaper install: the binary is in place, the task is not: run '$exe install' to try again"
  }
}
