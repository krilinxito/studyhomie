# StudyHomie installer for Windows:
#   irm https://studyhomie.maxi42.space/install.ps1 | iex
#
# Downloads the latest release (or $env:STUDYHOMIE_VERSION), checks its SHA-256 against the
# release's checksums.txt and puts studyhomie.exe in %LOCALAPPDATA%\Programs\StudyHomie
# (or $env:STUDYHOMIE_INSTALL_DIR), which it adds to your user PATH unless
# $env:STUDYHOMIE_NO_MODIFY_PATH is set. Running it again updates StudyHomie.

& {
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue'
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

  $repo = 'krilinxito/studyhomie'
  $headers = @{ 'User-Agent' = 'studyhomie-installer' }

  if (-not [Environment]::Is64BitOperatingSystem) { throw 'StudyHomie needs 64-bit Windows.' }

  $v = $env:STUDYHOMIE_VERSION
  if (-not $v) { $v = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest" -Headers $headers).tag_name }
  if (-not $v) { throw 'Could not find the latest StudyHomie version.' }
  if ($v -notmatch '^v') { $v = "v$v" }

  # only amd64 is published; Windows on ARM runs it under emulation
  $name = "studyhomie_${v}_windows_amd64"
  $base = "https://github.com/$repo/releases/download/$v"
  $dir = if ($env:STUDYHOMIE_INSTALL_DIR) { $env:STUDYHOMIE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\StudyHomie' }

  $tmp = Join-Path ([IO.Path]::GetTempPath()) ('studyhomie-' + [guid]::NewGuid())
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    Write-Host "Downloading StudyHomie $v..."
    $zip = Join-Path $tmp "$name.zip"
    Invoke-WebRequest "$base/$name.zip" -OutFile $zip -UseBasicParsing -Headers $headers
    $sumsFile = Join-Path $tmp 'checksums.txt'
    Invoke-WebRequest "$base/checksums.txt" -OutFile $sumsFile -UseBasicParsing -Headers $headers

    $want = $null
    foreach ($line in Get-Content $sumsFile) {
      $parts = $line.Trim() -split '\s+'
      if ($parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq "$name.zip") { $want = $parts[0].ToLower() }
    }
    if (-not $want) { throw "checksums.txt has no entry for $name.zip" }
    $got = (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower()
    if ($want -ne $got) { throw "Checksum mismatch for $name.zip (expected $want, got $got). Nothing was installed." }

    Expand-Archive $zip -DestinationPath $tmp -Force
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $exe = Join-Path $dir 'studyhomie.exe'
    # a running studyhomie.exe cannot be overwritten, but it can be renamed out of the way
    if (Test-Path $exe) { Move-Item -Force $exe "$exe.old" }
    Copy-Item (Join-Path $tmp "$name\studyhomie.exe") $exe -Force
    Remove-Item "$exe.old" -Force -ErrorAction SilentlyContinue

    if (-not $env:STUDYHOMIE_NO_MODIFY_PATH) {
      $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
      if (-not (($userPath -split ';') -contains $dir)) {
        $newPath = if ($userPath) { "$userPath;$dir" } else { $dir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
      }
      if (-not (($env:Path -split ';') -contains $dir)) { $env:Path += ";$dir" }
    }

    Write-Host ''
    Write-Host "StudyHomie $v is installed in $exe" -ForegroundColor Green
    Write-Host 'Next, in a folder for your studies:'
    Write-Host '  mkdir estudios; cd estudios'
    Write-Host '  studyhomie init'
    Write-Host '  studyhomie'
  } finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
  }
}
