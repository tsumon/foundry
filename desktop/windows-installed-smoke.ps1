param([Parameter(Mandatory=$true)][string]$Output)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'Native Windows required' }
$node = (Get-Command node.exe).Source
$installer = Join-Path $Output 'Foundry-0.1.0-alpha.1-x64.exe'
$testRoot = Join-Path $env:RUNNER_TEMP ('Foundry 验收 ' + [Guid]::NewGuid().ToString('N'))
$installed = Join-Path $testRoot '用户安装 Foundry'
New-Item -ItemType Directory -Path $testRoot | Out-Null
if (Test-Path $installed) { throw 'Install target must be fresh' }
$install = Start-Process -FilePath $installer -ArgumentList @('/S', "/D=$installed") -Wait -PassThru
if ($install.ExitCode -ne 0) { throw "Installer failed: $($install.ExitCode)" }
$app = Join-Path $installed 'Foundry.exe'
$resources = Join-Path $installed 'resources'
if (-not (Test-Path $app)) { throw 'Installed native app missing' }
$shipping = & $node (Join-Path $PSScriptRoot 'windows-provenance.cjs') (Join-Path $Output 'runtime-sources') (Join-Path $resources 'legal')
if ($LASTEXITCODE -ne 0) { throw 'Installed legal/source sidecar integrity failed' }
$shipping | Set-Content (Join-Path $Output 'shipping-provenance.json') -Encoding utf8
$oldPath = $env:PATH
$env:PATH = Join-Path $env:SystemRoot 'System32'
$env:FOUNDRY_INSTALLED_RESOURCES = $resources
$env:FOUNDRY_SMOKE_DATA = Join-Path $testRoot '保留 evidence'
try {
  # Test driver uses a pinned Node path; children have no node/go/gcc on PATH.
  $runtimeResult = & $node (Join-Path $PSScriptRoot 'installed-smoke.cjs')
  if ($LASTEXITCODE -ne 0) { throw 'Installed bundled backend smoke failed' }
  $runtime = ($runtimeResult -join "`n") | ConvertFrom-Json
  if ($runtime.result -ne 'PASS') { throw 'Missing installed smoke result' }
  $evidenceDB = Join-Path $runtime.simulationEvidence 'foundry.db'
  $evidenceHash = (Get-FileHash $evidenceDB -Algorithm SHA256).Hash
  $actualData = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Foundry'
  if (Test-Path $actualData) { throw 'Native UI acceptance requires a fresh user profile' }
  Add-Type -AssemblyName System.Drawing
  Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class FoundryWindow {
 [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr handle, out Rect rect);
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr handle);
}
'@
  function Wait-Window($Process) {
    $deadline = [DateTime]::UtcNow.AddSeconds(40)
    while ([DateTime]::UtcNow -lt $deadline) {
      $Process.Refresh()
      if ($Process.HasExited) { throw 'Native app exited before visible UI' }
      if ($Process.MainWindowHandle -ne [IntPtr]::Zero -and $Process.MainWindowTitle -like '*Persistent Workbench*' -and $Process.Responding) { return }
      Start-Sleep -Milliseconds 200
    }
    throw 'Native Electron window did not load'
  }
  $ui = Start-Process -FilePath $app -PassThru
  try {
    Wait-Window $ui
    [FoundryWindow]::SetForegroundWindow($ui.MainWindowHandle) | Out-Null
    $second = Start-Process -FilePath $app -PassThru
    if (-not $second.WaitForExit(15000)) { throw 'Second native instance did not yield ownership' }
    $rect = New-Object FoundryWindow+Rect
    if (-not [FoundryWindow]::GetWindowRect($ui.MainWindowHandle, [ref]$rect)) { throw 'Cannot capture native window' }
    $bitmap = New-Object System.Drawing.Bitmap(($rect.Right-$rect.Left), ($rect.Bottom-$rect.Top))
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    try {
      $graphics.CopyFromScreen($rect.Left,$rect.Top,0,0,$bitmap.Size)
      $bitmap.Save((Join-Path $Output 'windows-ui.png'), [System.Drawing.Imaging.ImageFormat]::Png)
    } finally { $graphics.Dispose(); $bitmap.Dispose() }
    if (-not $ui.CloseMainWindow() -or -not $ui.WaitForExit(15000)) { throw 'Native clean quit failed' }
    $profileDB = Join-Path $actualData 'workbench\foundry.db'
    if (-not (Test-Path $profileDB)) { throw 'LocalAppData persistent database missing' }
    $ui = Start-Process -FilePath $app -PassThru
    Wait-Window $ui
    if (-not $ui.CloseMainWindow() -or -not $ui.WaitForExit(15000)) { throw 'Native restart quit failed' }
  } finally {
    if ($ui -and -not $ui.HasExited) { $ui.Kill(); $ui.WaitForExit(); throw 'UI acceptance required forced cleanup; gate failed' }
  }
  $uninstaller = Join-Path $installed 'Uninstall Foundry.exe'
  if (-not (Test-Path $uninstaller)) { throw 'Per-user uninstaller missing' }
  $uninstall = Start-Process -FilePath $uninstaller -ArgumentList '/S' -Wait -PassThru
  if ($uninstall.ExitCode -ne 0) { throw 'Uninstaller failed' }
  $deadline = [DateTime]::UtcNow.AddSeconds(30)
  while ((Test-Path $app) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 200 }
  if (Test-Path $app) { throw 'Uninstall left installed executable' }
  if (-not (Test-Path $profileDB) -or (Get-FileHash $evidenceDB -Algorithm SHA256).Hash -ne $evidenceHash) { throw 'Uninstall changed persistent evidence' }
  @{ result='PASS'; platform='native Windows x64'; installed=$installed; unsigned=$true; runtime=$runtime; nativeUI='actual installed Electron visible window, captured; no manual interactive acceptance claimed'; secondInstance='PASS'; restart='PASS'; uninstall='PASS; data and simulation evidence retained'; powerLossCertified=$false } | ConvertTo-Json -Depth 8 | Set-Content (Join-Path $Output 'native-smoke.json') -Encoding utf8
} finally { $env:PATH=$oldPath }
