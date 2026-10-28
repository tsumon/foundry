param(
  [Parameter(Mandatory=$true)][string]$Output,
  [Parameter(Mandatory=$true)][string]$OldInstaller,
  [Parameter(Mandatory=$true)][string]$UpgradeFixture,
  [Parameter(Mandatory=$true)][string]$StableInstaller,
  [Parameter(Mandatory=$true)][string]$StableUpgradeFixture
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'Native Windows required' }
$node = (Get-Command node.exe).Source
$packageVersion = (Get-Content -LiteralPath (Join-Path $PSScriptRoot 'package.json') -Raw | ConvertFrom-Json).version
if ($packageVersion -cnotmatch '^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?\z') { throw 'Invalid desktop package version' }
$installer = Join-Path $Output "Foundry-$packageVersion-x64.exe"
$testRoot = Join-Path $env:RUNNER_TEMP ('Foundry 验收 ' + [Guid]::NewGuid().ToString('N'))
$installed = Join-Path $testRoot '用户安装 Foundry'
New-Item -ItemType Directory -Path $testRoot | Out-Null
if (Test-Path $installed) { throw 'Install target must be fresh' }
if (-not (Test-Path -LiteralPath $OldInstaller) -or (Get-FileHash -LiteralPath $OldInstaller -Algorithm SHA256).Hash.ToLowerInvariant() -cne '023bf7e0c95b3ec90e76e429b145e9fbbe0b1d52103ec82e5ac4eb3968188327') { throw 'Pinned alpha.3 installer digest mismatch' }
$oldInstall = Start-Process -FilePath $OldInstaller -ArgumentList @('/S', "/D=$installed") -Wait -PassThru
if ($oldInstall.ExitCode -ne 0) { throw "Old installer failed: $($oldInstall.ExitCode)" }
$app = Join-Path $installed 'Foundry.exe'
$resources = Join-Path $installed 'resources'
if (-not (Test-Path $app) -or -not (Test-Path (Join-Path $resources 'app.asar'))) { throw 'Old installed native app missing' }
$oldAsarHash = (Get-FileHash (Join-Path $resources 'app.asar') -Algorithm SHA256).Hash
$oldProbeRaw = & $node (Join-Path $PSScriptRoot 'upgrade-smoke.cjs') --probe-old $resources $UpgradeFixture (Join-Path $Output 'upgrade-old-installed.json')
if ($LASTEXITCODE -ne 0) { throw 'Installed alpha.3 controller seed probe failed' }
$oldProbe = ($oldProbeRaw -join "`n") | ConvertFrom-Json
if ($oldProbe.result -cne 'PASS' -or $oldProbe.source_version -cne 'v0.1.0-alpha.3') { throw 'Invalid old installed controller receipt' }
$oldResources = Join-Path $testRoot 'alpha3-installed-resources'
Copy-Item -LiteralPath $resources -Destination $oldResources -Recurse
if ((Get-FileHash (Join-Path $oldResources 'app.asar') -Algorithm SHA256).Hash -cne $oldAsarHash) { throw 'Old installed resource copy changed' }
$install = Start-Process -FilePath $installer -ArgumentList @('/S', "/D=$installed") -Wait -PassThru
if ($install.ExitCode -ne 0) { throw "Candidate over-install failed: $($install.ExitCode)" }
if (-not (Test-Path $app) -or -not (Test-Path (Join-Path $resources 'app.asar'))) { throw 'Candidate installed native app missing' }
$candidateAsarHash = (Get-FileHash (Join-Path $resources 'app.asar') -Algorithm SHA256).Hash
& $node (Join-Path $PSScriptRoot 'distribution.cjs') --verify $Output $resources windows/amd64
if ($LASTEXITCODE -ne 0) { throw 'Dual runtime installed distribution/source linkage failed' }
if ($candidateAsarHash -ceq $oldAsarHash) { throw 'Candidate did not replace old installed app bytes' }
$shipping = & $node (Join-Path $PSScriptRoot 'windows-provenance.cjs') (Join-Path $Output 'runtime-sources') (Join-Path $resources 'legal')
if ($LASTEXITCODE -ne 0) { throw 'Installed legal/source sidecar integrity failed' }
$shipping | Set-Content (Join-Path $Output 'shipping-provenance.json') -Encoding utf8
$oldPath = $env:PATH
$env:PATH = Join-Path $env:SystemRoot 'System32'
$env:FOUNDRY_INSTALLED_RESOURCES = $resources
$env:FOUNDRY_SMOKE_DATA = Join-Path $testRoot '保留 evidence'
try {
  # Test driver uses a pinned Node path; children have no node/go/gcc on PATH.
  # Mandatory real WHPX guest gate. No feature installation, reboot, TCG,
  # unsupported-host skip, account credential, or real-provider request.
  $env:FOUNDRY_VM_ACCEPTANCE_DATA = Join-Path $testRoot 'native-vm-evidence'
  $env:FOUNDRY_VM_ACCEPTANCE_REPORT = Join-Path $Output 'native-vm-acceptance.json'
  $vmResult = & $node (Join-Path $PSScriptRoot 'native-vm-smoke.cjs')
  if ($LASTEXITCODE -ne 0) { throw 'Mandatory installed WHPX guest acceptance failed; see bounded native-vm-acceptance.json when available' }
  $nativeVM = ($vmResult -join "`n") | ConvertFrom-Json
  if ($nativeVM.passed -ne $true -or $nativeVM.acceleration -cne 'whpx') { throw 'Missing native WHPX guest receipt' }
  $env:FOUNDRY_VM_ACCEPTANCE_DATA = Join-Path $testRoot 'native-v2-evidence'
  $env:FOUNDRY_VM_ACCEPTANCE_REPORT = Join-Path $Output 'native-vm-v2-matrix.json'
  $v2Result = & $node (Join-Path $PSScriptRoot 'native-vm-smoke.cjs') --v2-matrix
  if ($LASTEXITCODE -ne 0) { throw 'Mandatory installed v2 WHPX matrix failed; distinct bounded attempt receipts retained' }
  $nativeV2 = ($v2Result -join "`n") | ConvertFrom-Json
  if ($nativeV2.passed -ne $true -or $nativeV2.kind -cne 'foundry-native-vm-v2-matrix' -or $nativeV2.cases.Count -ne 12) { throw 'Missing installed v2 native matrix' }
  $runtimeResult = & $node (Join-Path $PSScriptRoot 'installed-smoke.cjs')
  if ($LASTEXITCODE -ne 0) { throw 'Installed bundled backend smoke failed' }
  $runtime = ($runtimeResult -join "`n") | ConvertFrom-Json
  if ($runtime.result -ne 'PASS') { throw 'Missing installed smoke result' }
  $evidenceDB = Join-Path $runtime.simulationEvidence 'foundry.db'
  $evidenceHash = (Get-FileHash $evidenceDB -Algorithm SHA256).Hash
  $runtimeCodingDB = Join-Path $runtime.codingEvidence 'foundry.db'
  $runtimeCodingHash = (Get-FileHash $runtimeCodingDB -Algorithm SHA256).Hash
  $actualData = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Foundry'
  $sameVolume = [string]::Equals([IO.Path]::GetPathRoot($resources), [IO.Path]::GetPathRoot($actualData), [StringComparison]::OrdinalIgnoreCase)
  [Console]::WriteLine('Native UI install/data placement: ' + (@{ same_drive=$sameVolume } | ConvertTo-Json -Compress))
  if (Test-Path $actualData) { throw 'Native UI acceptance requires a fresh user profile' }
  Add-Type -AssemblyName System.Drawing
  Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class FoundryWindow {
 [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
 [StructLayout(LayoutKind.Sequential)] public struct Point { public int X, Y; }
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr handle, out Rect rect);
 [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr handle, out Rect rect);
 [DllImport("user32.dll")] public static extern bool ClientToScreen(IntPtr handle, ref Point point);
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr handle);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("dwmapi.dll")] public static extern int DwmFlush();
}
'@
  function Wait-Window($Process, [string]$Title, [string]$Mode, [string]$Filename) {
    $deadline = [DateTime]::UtcNow.AddSeconds(40)
    $last = @{ ready=$false }
    $paint = @{ clientPaint=$false }
    while ([DateTime]::UtcNow -lt $deadline) {
      $Process.Refresh()
      if ($Process.HasExited) { throw 'Native app exited before visible UI' }
      if ($Process.MainWindowHandle -ne [IntPtr]::Zero -and $Process.MainWindowTitle -ceq $Title -and $Process.Responding) {
        $raw = & $node (Join-Path $PSScriptRoot 'windows-ui-ready.cjs') $Process.MainWindowHandle.ToInt64().ToString() $Process.Id.ToString() $Mode
        if ($LASTEXITCODE -ne 0) { throw 'Native UI inspection driver failed' }
        $last = ($raw -join "`n") | ConvertFrom-Json
        if ($last.ready) {
          $paint = Capture-Workbench $Process $Filename
          if ($paint.clientPaint) { return @{ semantics=$last; paint=$paint } }
        }
      }
      Start-Sleep -Milliseconds 200
    }
    $diagnostic = 'Native workbench content/data/paint readiness deadline exceeded: ' + (@{ semantics=$last; paint=$paint } | ConvertTo-Json -Compress -Depth 10)
    [Console]::Error.WriteLine($diagnostic)
    throw 'Native workbench readiness deadline exceeded'
  }
  function Capture-Workbench($Process, [string]$Filename) {
    [FoundryWindow]::SetForegroundWindow($Process.MainWindowHandle) | Out-Null
    if ([FoundryWindow]::GetForegroundWindow() -ne $Process.MainWindowHandle) { return @{ clientPaint=$false; foreground=$false } }
    if ([FoundryWindow]::DwmFlush() -ne 0) { throw 'Desktop presentation synchronization failed' }
    $rect = New-Object FoundryWindow+Rect
    $client = New-Object FoundryWindow+Rect
    $origin = New-Object FoundryWindow+Point
    if (-not [FoundryWindow]::GetWindowRect($Process.MainWindowHandle, [ref]$rect) -or -not [FoundryWindow]::GetClientRect($Process.MainWindowHandle, [ref]$client) -or -not [FoundryWindow]::ClientToScreen($Process.MainWindowHandle, [ref]$origin)) { throw 'Cannot capture native client' }
    $bitmap = New-Object System.Drawing.Bitmap(($rect.Right-$rect.Left), ($rect.Bottom-$rect.Top))
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    try {
      $graphics.CopyFromScreen($rect.Left,$rect.Top,0,0,$bitmap.Size)
      if ([FoundryWindow]::GetForegroundWindow() -ne $Process.MainWindowHandle) { return @{ clientPaint=$false; foreground=$false } }
      # Supplement semantic readiness with actual client paint, excluding
      # native title/menu/borders. Pixels alone can never admit the window.
      $colors = New-Object 'System.Collections.Generic.HashSet[int]'
      for ($y=8; $y -lt ($client.Bottom-8); $y+=4) {
        for ($x=8; $x -lt ($client.Right-8); $x+=4) {
          $pixel = $bitmap.GetPixel($origin.X-$rect.Left+$x, $origin.Y-$rect.Top+$y)
          $colors.Add((($pixel.R -shr 4) -shl 8) -bor (($pixel.G -shr 4) -shl 4) -bor ($pixel.B -shr 4)) | Out-Null
        }
      }
      $bitmap.Save((Join-Path $Output $Filename), [System.Drawing.Imaging.ImageFormat]::Png)
      return @{ clientPaint=($colors.Count -ge 8); foreground=$true; colorBuckets=$colors.Count }
    } finally { $graphics.Dispose(); $bitmap.Dispose() }
  }
  function Assert-WorkbenchForeground($Process, [string]$Title) {
    $Process.Refresh()
    if ($Process.HasExited -or $Process.MainWindowHandle -eq [IntPtr]::Zero -or $Process.MainWindowTitle -cne $Title -or -not $Process.Responding) {
      throw 'Native mode source window mismatch'
    }
    $handle = $Process.MainWindowHandle
    [FoundryWindow]::SetForegroundWindow($handle) | Out-Null
    if ([FoundryWindow]::GetForegroundWindow() -ne $handle) { throw 'Native mode source is not foreground' }
    return $handle
  }
  $ui = Start-Process -FilePath $app -PassThru
  $uiFailure = $null
  try {
    $firstReady = Wait-Window $ui 'Foundry — Persistent Workbench' 'persistent' 'windows-ui.png'
    [FoundryWindow]::SetForegroundWindow($ui.MainWindowHandle) | Out-Null
    $second = Start-Process -FilePath $app -PassThru
    if (-not $second.WaitForExit(15000)) { throw 'Second native instance did not yield ownership' }
    $firstReady = Wait-Window $ui 'Foundry — Persistent Workbench' 'persistent' 'windows-ui.png'
    $codingSourceHandle = Assert-WorkbenchForeground $ui 'Foundry — Persistent Workbench'
    $codingActionRaw = & $node (Join-Path $PSScriptRoot 'windows-ui-mode.cjs') $codingSourceHandle.ToInt64().ToString() $ui.Id.ToString() 'coding'
    if ($LASTEXITCODE -ne 0) { throw 'Native coding menu action failed' }
    $codingAction = ($codingActionRaw -join "`n") | ConvertFrom-Json
    if (-not $codingAction.ok -or $codingAction.action -ne 'coding') { throw 'Native coding menu receipt invalid' }
    $codingReady = Wait-Window $ui 'Foundry — Controlled Coding Workbench' 'coding-vm-configurable' 'windows-ui-coding.png'
    $codingDB = Join-Path $actualData 'coding\foundry.db'
    if (-not (Test-Path $codingDB)) { throw 'Native coding database missing' }
    $persistentSourceHandle = Assert-WorkbenchForeground $ui 'Foundry — Controlled Coding Workbench'
    $persistentActionRaw = & $node (Join-Path $PSScriptRoot 'windows-ui-mode.cjs') $persistentSourceHandle.ToInt64().ToString() $ui.Id.ToString() 'persistent'
    if ($LASTEXITCODE -ne 0) { throw 'Native persistent menu action failed' }
    $persistentAction = ($persistentActionRaw -join "`n") | ConvertFrom-Json
    if (-not $persistentAction.ok -or $persistentAction.action -ne 'persistent') { throw 'Native persistent menu receipt invalid' }
    $switchedReady = Wait-Window $ui 'Foundry — Persistent Workbench' 'persistent' 'windows-ui-return.png'
    $codingHash = (Get-FileHash $codingDB -Algorithm SHA256).Hash
    if (-not $ui.CloseMainWindow() -or -not $ui.WaitForExit(15000)) { throw 'Native clean quit failed' }
    $profileDB = Join-Path $actualData 'workbench\foundry.db'
    if (-not (Test-Path $profileDB)) { throw 'LocalAppData persistent database missing' }
    $ui = Start-Process -FilePath $app -PassThru
    $restartReady = Wait-Window $ui 'Foundry — Persistent Workbench' 'persistent' 'windows-ui-restart.png'
    if (-not (Test-Path $codingDB) -or (Get-FileHash $codingDB -Algorithm SHA256).Hash -ne $codingHash) { throw 'Native restart changed retained coding data' }
    if (-not $ui.CloseMainWindow() -or -not $ui.WaitForExit(15000)) { throw 'Native restart quit failed' }
  } catch {
    $uiFailure = $_
    throw
  } finally {
    if ($ui -and -not $ui.HasExited) {
      try { $ui.Kill(); $ui.WaitForExit() } catch { if ($null -eq $uiFailure) { throw } }
      if ($null -eq $uiFailure) { throw 'UI acceptance required forced cleanup; gate failed' }
    }
  }
  $upgradeRaw = & $node (Join-Path $PSScriptRoot 'upgrade-smoke.cjs') $oldResources $resources $UpgradeFixture (Join-Path $Output 'upgrade-acceptance.json')
  if ($LASTEXITCODE -ne 0) { throw 'Installed cross-version upgrade acceptance failed' }
  $upgrade = ($upgradeRaw -join "`n") | ConvertFrom-Json
  if ($upgrade.result -cne 'PASS' -or $upgrade.source_version -cne 'v0.1.0-alpha.3' -or $upgrade.host -cne 'windows/amd64') { throw 'Invalid cross-version upgrade receipt' }
  if ($upgrade.old_manifest_sha256 -cne $oldProbe.old_manifest_sha256 -or $upgrade.frozen_history_sha256 -cne $oldProbe.frozen_history_sha256) { throw 'Installed old probe and upgrade history disagree' }
  $uninstaller = Join-Path $installed 'Uninstall Foundry.exe'
  if (-not (Test-Path $uninstaller)) { throw 'Per-user uninstaller missing' }
  $uninstall = Start-Process -FilePath $uninstaller -ArgumentList '/S' -Wait -PassThru
  if ($uninstall.ExitCode -ne 0) { throw 'Uninstaller failed' }
  $deadline = [DateTime]::UtcNow.AddSeconds(30)
  while ((Test-Path $app) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 200 }
  if (Test-Path $app) { throw 'Uninstall left installed executable' }
  foreach ($runtimePath in @('sandbox','sandbox-v2')) { if (Test-Path (Join-Path $resources $runtimePath)) { throw 'Uninstall left installed runtime resources' } }
  if (-not (Test-Path $profileDB) -or -not (Test-Path $codingDB) -or (Get-FileHash $codingDB -Algorithm SHA256).Hash -ne $codingHash -or -not (Test-Path $runtimeCodingDB) -or (Get-FileHash $runtimeCodingDB -Algorithm SHA256).Hash -ne $runtimeCodingHash -or (Get-FileHash $evidenceDB -Algorithm SHA256).Hash -ne $evidenceHash) { throw 'Uninstall changed persistent evidence' }
  $freshInstalled = Join-Path $testRoot 'fresh-candidate-install'
  if (Test-Path $freshInstalled) { throw 'Fresh candidate install target occupied' }
  $freshInstall = Start-Process -FilePath $installer -ArgumentList @('/S', "/D=$freshInstalled") -Wait -PassThru
  if ($freshInstall.ExitCode -ne 0) { throw 'Fresh candidate installer failed' }
  $freshApp = Join-Path $freshInstalled 'Foundry.exe'
  $freshResources = Join-Path $freshInstalled 'resources'
  if (-not (Test-Path $freshApp) -or (Get-FileHash (Join-Path $freshResources 'app.asar') -Algorithm SHA256).Hash -cne $candidateAsarHash) { throw 'Fresh candidate installed bytes differ from upgraded candidate' }
  $freshRaw = & $node (Join-Path $PSScriptRoot 'upgrade-smoke.cjs') --verify-fresh $freshResources (Join-Path $Output 'fresh-candidate-integrity.json')
  if ($LASTEXITCODE -ne 0) { throw 'Fresh candidate resource integrity failed' }
  $fresh = ($freshRaw -join "`n") | ConvertFrom-Json
  if ($fresh.result -cne 'PASS' -or $fresh.manifest_sha256 -cne $upgrade.current_manifest_sha256) { throw 'Fresh candidate manifest differs from upgraded candidate' }
  $freshUninstall = Start-Process -FilePath (Join-Path $freshInstalled 'Uninstall Foundry.exe') -ArgumentList '/S' -Wait -PassThru
  if ($freshUninstall.ExitCode -ne 0) { throw 'Fresh candidate uninstaller failed' }
  $freshDeadline = [DateTime]::UtcNow.AddSeconds(30)
  while ((Test-Path $freshApp) -and [DateTime]::UtcNow -lt $freshDeadline) { Start-Sleep -Milliseconds 200 }
  if (Test-Path $freshApp) { throw 'Fresh candidate uninstall left executable' }
  foreach ($runtimePath in @('sandbox','sandbox-v2')) { if (Test-Path (Join-Path $freshResources $runtimePath)) { throw 'Fresh uninstall left installed runtime resources' } }
  if (-not (Test-Path $profileDB) -or -not (Test-Path $codingDB) -or (Get-FileHash $codingDB -Algorithm SHA256).Hash -ne $codingHash -or -not (Test-Path $runtimeCodingDB) -or (Get-FileHash $runtimeCodingDB -Algorithm SHA256).Hash -ne $runtimeCodingHash -or (Get-FileHash $evidenceDB -Algorithm SHA256).Hash -ne $evidenceHash) { throw 'Fresh install or uninstall changed retained evidence' }
  $stableRaw = & (Join-Path $PSScriptRoot 'windows-upgrade-smoke.ps1') -Output $Output -Node $node -Installer $installer -OldInstaller $StableInstaller -UpgradeFixture $StableUpgradeFixture -TestRoot $testRoot
  $stableUpgrade = ($stableRaw -join "`n") | ConvertFrom-Json
  if ($stableUpgrade.result -cne 'PASS') { throw 'Stable upgrade supplement did not pass' }
  @{ result='PASS'; nativeV2=$nativeV2; stableUpgrade=$stableUpgrade; platform='native Windows x64'; installed=$installed; unsigned=$true; runtime=$runtime; upgrade=$upgrade; freshInstall=$fresh; nativeUI='OS UI Automation opened the fixed native coding menu and confirmation, observed configurable VM metadata with opt-in unchecked and independent approvals, returned to persistent mode, and captured painted clients; no manual interactive acceptance claimed'; firstUI=$firstReady; codingAction=$codingAction; codingUI=$codingReady; persistentAction=$persistentAction; switchedUI=$switchedReady; restartUI=$restartReady; secondInstance='PASS'; restart='PASS; defaulted to persistent mode and retained coding data'; uninstall='PASS; persistent, coding, and simulation evidence retained'; powerLossCertified=$false } | ConvertTo-Json -Depth 8 | Set-Content (Join-Path $Output 'native-smoke.json') -Encoding utf8
} finally { $env:PATH=$oldPath }
