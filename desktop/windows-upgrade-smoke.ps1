param(
 [Parameter(Mandatory=$true)][string]$Output,
 [Parameter(Mandatory=$true)][string]$Node,
 [Parameter(Mandatory=$true)][string]$Installer,
 [Parameter(Mandatory=$true)][string]$OldInstaller,
 [Parameter(Mandatory=$true)][string]$UpgradeFixture,
 [Parameter(Mandatory=$true)][string]$TestRoot
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'Native Windows required' }
# Stable supplement deliberately reuses the strict controller upgrade path.
# The complete native/UI matrix has already run once on this same candidate.
$priorHash = '5a770a31021d3914ad1b30a5597e0ec17e256c50f7e65c7565cfd21603f6ac7d'
if ((Get-FileHash -LiteralPath $OldInstaller -Algorithm SHA256).Hash.ToLowerInvariant() -cne $priorHash) { throw 'Pinned stable installer digest mismatch' }
$installed = Join-Path $TestRoot 'stable-overinstall'
if (Test-Path -LiteralPath $installed) { throw 'Stable install target must be fresh' }
function Install-Exact([string]$File, [string[]]$Arguments, [hashtable]$Receipt=$null) {
 $watch = [System.Diagnostics.Stopwatch]::StartNew()
 if ($null -ne $Receipt) { $Receipt.launcher_started=$false; $Receipt.launcher_returned=$false; $Receipt.launcher_exit_code=$null }
 try {
  $process = Start-Process -FilePath $File -ArgumentList $Arguments -PassThru
  if ($null -ne $Receipt) { $Receipt.launcher_started=$true }
  if (-not $process.WaitForExit(120000)) { $process.Kill(); $process.WaitForExit(); throw 'Stable supplement installer deadline exceeded' }
  if ($null -ne $Receipt) { $Receipt.launcher_returned=$true; $Receipt.launcher_exit_code=[int]$process.ExitCode }
  if ($process.ExitCode -ne 0) { throw 'Stable supplement installer failed' }
 } finally {
  if ($null -ne $Receipt) {
   $Receipt.launcher_elapsed_ms=[double]$watch.Elapsed.TotalMilliseconds
   $diagnostic = [ordered]@{ launcher_started=[bool]$Receipt.launcher_started; launcher_returned=[bool]$Receipt.launcher_returned; launcher_exit_code=$Receipt.launcher_exit_code; launcher_elapsed_ms=[double]$Receipt.launcher_elapsed_ms }
   [Console]::Error.WriteLine('stable-uninstall-launch: ' + ($diagnostic | ConvertTo-Json -Compress))
  }
 }
}
function Wait-StableUninstall([string]$Installed, [hashtable]$Launch, [scriptblock]$Exists, [scriptblock]$Clock, [scriptblock]$Pause) {
 # Launcher exit is not a claim about an NSIS child. Observe every required
 # owned path, without relaunching uninstall or removing residual files.
 $paths = [ordered]@{ app='Foundry.exe'; sandbox='resources/sandbox'; sandbox_v2='resources/sandbox-v2' }
 $record = [ordered]@{ launcher_exit_code=$null; launcher_elapsed_ms=0.0; elapsed_ms=0.0; polls=0; remaining=@('app','sandbox','sandbox_v2'); outcome='pending' }
 $time = @{ start=$null; previous=$null }
 $readElapsed = {
  $now = & $Clock
  if ($now -isnot [double] -or [double]::IsNaN($now) -or [double]::IsInfinity($now) -or $now -lt 0 -or ($null -ne $time.previous -and $now -lt $time.previous)) {
   $record.outcome='invalid_clock'; throw 'Stable uninstall invalid observation clock'
  }
  if ($null -eq $time.start) { $time.start=$now }
  $time.previous=$now
  return [double]($now-$time.start)
 }
 try {
  if ($Launch.launcher_exit_code -is [int]) { $record.launcher_exit_code=[int]$Launch.launcher_exit_code }
  $record.launcher_elapsed_ms=[double]$Launch.launcher_elapsed_ms
  if (-not $Launch.launcher_started -or -not $Launch.launcher_returned -or $Launch.launcher_exit_code -ne 0) { $record.outcome='launcher_failed'; throw 'Stable uninstall launcher not successful' }
  $record.elapsed_ms = & $readElapsed
  for ($poll=0; $poll -lt 151; $poll++) {
   $record.elapsed_ms = & $readElapsed
   if ($record.elapsed_ms -ge 30000) { $record.outcome='residue'; throw 'Stable supplement uninstall left app/runtime bytes before observation deadline' }
   $record.polls++
   $remaining = @()
   try {
    foreach ($category in $paths.Keys) {
     $present = & $Exists (Join-Path $Installed $paths[$category])
     if ($present -isnot [bool]) { throw 'Invalid path observation' }
     if ($present) { $remaining += [string]$category }
    }
   } catch { $record.outcome='read_failed'; throw 'Stable uninstall observation read failed' }
   $record.remaining=$remaining
   $record.elapsed_ms = & $readElapsed
   if ($record.elapsed_ms -ge 30000) { $record.outcome='deadline'; throw 'Stable uninstall observation deadline exceeded' }
   if ($remaining.Count -eq 0) { $record.outcome='absent'; return $record }
   & $Pause ([Math]::Min(200,30000-$record.elapsed_ms))
  }
  $record.outcome='poll_bound'; throw 'Stable uninstall observation poll bound exceeded'
 } finally {
  [Console]::Error.WriteLine('stable-uninstall-observation: ' + ($record | ConvertTo-Json -Depth 3 -Compress))
 }
}
Install-Exact $OldInstaller @('/S', "/D=$installed")
$resources = Join-Path $installed 'resources'
$oldHash = (Get-FileHash (Join-Path $resources 'app.asar') -Algorithm SHA256).Hash
$probe = & $Node (Join-Path $PSScriptRoot 'upgrade-smoke.cjs') --probe-old $resources $UpgradeFixture (Join-Path $Output 'upgrade-stable-old-installed.json') 'v0.1.0'
if ($LASTEXITCODE -ne 0 -or (($probe -join "`n") | ConvertFrom-Json).source_commit -cne 'ab89b5a88f73755e91aaefa5b73b1dfd5c73fdb0') { throw 'Installed stable controller seed probe failed' }
$oldResources = Join-Path $TestRoot 'stable-installed-resources'
if (Test-Path -LiteralPath $oldResources) { throw 'Stable resource copy must be fresh' }
Copy-Item -LiteralPath $resources -Destination $oldResources -Recurse
if ((Get-FileHash (Join-Path $oldResources 'app.asar') -Algorithm SHA256).Hash -cne $oldHash) { throw 'Stable resource copy changed' }
Install-Exact $Installer @('/S', "/D=$installed")
if ((Get-FileHash (Join-Path $resources 'app.asar') -Algorithm SHA256).Hash -ceq $oldHash) { throw 'Stable over-install did not replace app bytes' }
& $Node (Join-Path $PSScriptRoot 'distribution.cjs') --verify $Output $resources windows/amd64 | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Stable over-install candidate distribution mismatch' }
$upgradeRaw = & $Node (Join-Path $PSScriptRoot 'upgrade-smoke.cjs') $oldResources $resources $UpgradeFixture (Join-Path $Output 'upgrade-stable-acceptance.json') 'v0.1.0'
if ($LASTEXITCODE -ne 0) { throw 'Stable upgrade/recovery/rollback failed' }
$upgrade = ($upgradeRaw -join "`n") | ConvertFrom-Json
if ($upgrade.result -cne 'PASS' -or $upgrade.source_version -cne 'v0.1.0' -or $upgrade.requested_provider_calls -ne 0 -or $upgrade.requested_vm_calls -ne 0 -or $upgrade.requested_model_calls -ne 0) { throw 'Invalid stable upgrade receipt' }
$fixture = Get-Content -LiteralPath $UpgradeFixture -Raw | ConvertFrom-Json
function Data-Inventory {
 $rows = @(Get-ChildItem -LiteralPath $fixture.data_root -Recurse -Force | Sort-Object FullName | ForEach-Object {
  if ($_.LinkType) { throw 'Linked synthetic upgrade data' }
  [ordered]@{ path=$_.FullName.Substring($fixture.data_root.Length); directory=$_.PSIsContainer; sha256=$(if ($_.PSIsContainer) { '' } else { (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash }) }
 })
 return ConvertTo-Json -InputObject $rows -Depth 4 -Compress
}
$beforeUninstall = Data-Inventory
$uninstallLaunch = @{}
Install-Exact (Join-Path $installed 'Uninstall Foundry.exe') @('/S') $uninstallLaunch
$uninstallWatch = [System.Diagnostics.Stopwatch]::StartNew()
$uninstallObservation = Wait-StableUninstall $installed $uninstallLaunch { param($Path) Test-Path -LiteralPath $Path -ErrorAction Stop } { $uninstallWatch.Elapsed.TotalMilliseconds } { param($Milliseconds) Start-Sleep -Milliseconds $Milliseconds }
if ((Data-Inventory) -cne $beforeUninstall) { throw 'Stable uninstall changed synthetic task/runtime/evidence data' }
$report = @{ result='PASS'; source_version='v0.1.0'; source_commit='ab89b5a88f73755e91aaefa5b73b1dfd5c73fdb0'; prior_installer_sha256=$priorHash; candidate_installer_sha256=(Get-FileHash -LiteralPath $Installer -Algorithm SHA256).Hash.ToLowerInvariant(); scope='native stable over-install, strict controller recovery/rollback, uninstall; independent synthetic data'; requested_model_calls=0; requested_vm_calls=0; uninstall='PASS; complete synthetic data inventory unchanged'; uninstall_observation=$uninstallObservation; candidate_manifest_sha256=$upgrade.current_manifest_sha256 }
$report | ConvertTo-Json -Depth 4 | Set-Content (Join-Path $Output 'upgrade-stable-uninstall.json') -Encoding utf8
$report | ConvertTo-Json -Depth 4 -Compress
