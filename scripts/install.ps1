# Install zellij-reaper on Windows from a GitHub release and start its task.
# Each release carries this script as its windows-installer.ps1, with the
# release written in here and below, and installs that release:
#
#   irm https://github.com/kanggu12/zellij-reaper/releases/download/vX.Y.Z/zellij-reaper-X.Y.Z-windows-installer.ps1 | iex
#
# Settings, all optional, as environment variables:
#   ZELLIJ_REAPER_VERSION   release to install, v0.16.1 or later (default: the release this
#                           script came with; this copy in the repository installs
#                           the latest)
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

  # The release this script installs. scripts/installers.sh writes it into the
  # copy each release carries; empty here, so a run from the repository
  # installs the latest.
  $release = ''

  $repo = 'kanggu12/zellij-reaper'
  $base = if ($env:ZELLIJ_REAPER_BASE_URL) { $env:ZELLIJ_REAPER_BASE_URL } else { "https://github.com/$repo/releases/download" }
  $binDir = if ($env:ZELLIJ_REAPER_BIN_DIR) { $env:ZELLIJ_REAPER_BIN_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\zellij-reaper' }

  # zellij has a Windows build for x64 only, and so does zellij-reaper.
  if ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64' -and $env:PROCESSOR_ARCHITEW6432 -ne 'AMD64') {
    throw "zellij-reaper install: no release for $env:PROCESSOR_ARCHITECTURE"
  }

  # The release asked for, else the one this script came with, else the
  # latest: where github.com/<repo>/releases/latest redirects.
  $version = $env:ZELLIJ_REAPER_VERSION
  if (-not $version) { $version = $release }
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
  # Releases before 0.16.1 named their files otherwise, and each installs with
  # the script it carries: one before 0.15.0 with its install.ps1, told which
  # release, since left to itself it looks for the latest, whose files it
  # cannot name; one from 0.15.0 to 0.16.0 with the script named with its v,
  # which installs its own release. The backticks keep $env in the message as
  # words to type. Windows builds start with 0.6.0.
  if ($version -match '^v0\.[0-5]\.') {
    throw "zellij-reaper install: $version has no Windows build; the first release with one is v0.6.0"
  }
  if ($version -match '^v0\.([0-9]|1[0-4])\.') {
    throw "zellij-reaper install: $version names its files otherwise; install it with its own script: " +
      "`$env:ZELLIJ_REAPER_VERSION = '$version'; irm https://github.com/$repo/releases/download/$version/install.ps1 | iex"
  }
  if ($version -match '^v0\.(15\.|16\.0$)') {
    throw "zellij-reaper install: $version names its files otherwise; install it with its own script: " +
      "irm https://github.com/$repo/releases/download/$version/zellij-reaper-$version-install.ps1 | iex"
  }

  # Every file of a release starts with the name and the version, without its
  # v, and the archive holds a folder of the same name: zellij-reaper-0.16.1-windows-x64.
  $plain = $version.Substring(1)
  $name = "zellij-reaper-$plain-windows-x64"
  $archive = "$name.zip"
  $sums = "zellij-reaper-$plain-checksums.txt"
  $tmp = Join-Path ([IO.Path]::GetTempPath()) ('zellij-reaper-' + [Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    Write-Host "downloading $archive"
    $wc = New-Object Net.WebClient
    try {
      $wc.DownloadFile("$base/$version/$archive", (Join-Path $tmp $archive))
      $wc.DownloadFile("$base/$version/$sums", (Join-Path $tmp $sums))
    } catch {
      throw "zellij-reaper install: could not download $archive"
    }
    # The release's checksums. Get-FileHash is missing from some PowerShell
    # setups, so the hash is computed directly.
    $line = Get-Content (Join-Path $tmp $sums) | Where-Object { $_ -match (' ' + [regex]::Escape($archive) + '$') } | Select-Object -First 1
    if (-not $line) { throw "zellij-reaper install: $archive is not in $sums" }
    $want = ($line -split ' ')[0]
    $sha = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead((Join-Path $tmp $archive))
    try { $got = -join ($sha.ComputeHash($stream) | ForEach-Object { $_.ToString('x2') }) } finally { $stream.Close() }
    if ($got -ne $want) { throw "zellij-reaper install: checksum mismatch for $archive" }

    Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'x') -Force
    # The binary, out of the archive's folder.
    $new = Join-Path $tmp "x\$name\zellij-reaper.exe"
    if (-not (Test-Path $new)) { throw "zellij-reaper install: $archive holds no $name\zellij-reaper.exe" }
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
      try {
        Copy-Item $new $exe -Force
      } catch {
        # The new copy did not land: the old one goes back, so the task and
        # the PATH still find a program.
        Move-Item $aside $exe -Force
        throw
      }
      Remove-Item $aside -Force -ErrorAction SilentlyContinue
    } else {
      Copy-Item $new $exe -Force
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
