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
function Install-Exact([string]$File, [string[]]$Arguments) {
 $process = Start-Process -FilePath $File -ArgumentList $Arguments -PassThru
 if (-not $process.WaitForExit(120000)) { $process.Kill(); $process.WaitForExit(); throw 'Stable supplement installer deadline exceeded' }
 if ($process.ExitCode -ne 0) { throw 'Stable supplement installer failed' }
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
Install-Exact (Join-Path $installed 'Uninstall Foundry.exe') @('/S')
$deadline = [DateTime]::UtcNow.AddSeconds(30)
while ((Test-Path (Join-Path $installed 'Foundry.exe')) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 200 }
foreach ($relative in @('Foundry.exe','resources/sandbox','resources/sandbox-v2')) { if (Test-Path (Join-Path $installed $relative)) { throw 'Stable supplement uninstall left app/runtime bytes' } }
if ((Data-Inventory) -cne $beforeUninstall) { throw 'Stable uninstall changed synthetic task/runtime/evidence data' }
$report = @{ result='PASS'; source_version='v0.1.0'; source_commit='ab89b5a88f73755e91aaefa5b73b1dfd5c73fdb0'; prior_installer_sha256=$priorHash; candidate_installer_sha256=(Get-FileHash -LiteralPath $Installer -Algorithm SHA256).Hash.ToLowerInvariant(); scope='native stable over-install, strict controller recovery/rollback, uninstall; independent synthetic data'; requested_model_calls=0; requested_vm_calls=0; uninstall='PASS; complete synthetic data inventory unchanged'; candidate_manifest_sha256=$upgrade.current_manifest_sha256 }
$report | ConvertTo-Json -Depth 4 | Set-Content (Join-Path $Output 'upgrade-stable-uninstall.json') -Encoding utf8
$report | ConvertTo-Json -Depth 4 -Compress
