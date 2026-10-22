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
 [StructLayout(LayoutKind.Sequential)] public struct Point { public int X, Y; }
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr handle, out Rect rect);
 [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr handle, out Rect rect);
 [DllImport("user32.dll")] public static extern bool ClientToScreen(IntPtr handle, ref Point point);
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr handle);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("dwmapi.dll")] public static extern int DwmFlush();
}
'@
  function Wait-Window($Process, [string]$Filename) {
    $deadline = [DateTime]::UtcNow.AddSeconds(40)
    $last = @{ ready=$false }
    $paint = @{ clientPaint=$false }
    while ([DateTime]::UtcNow -lt $deadline) {
      $Process.Refresh()
      if ($Process.HasExited) { throw 'Native app exited before visible UI' }
      if ($Process.MainWindowHandle -ne [IntPtr]::Zero -and $Process.MainWindowTitle -like '*Persistent Workbench*' -and $Process.Responding) {
        $raw = & $node (Join-Path $PSScriptRoot 'windows-ui-ready.cjs') $Process.MainWindowHandle.ToInt64().ToString() $Process.Id.ToString()
        if ($LASTEXITCODE -ne 0) { throw 'Native UI inspection driver failed' }
        $last = ($raw -join "`n") | ConvertFrom-Json
        if ($last.ready) {
          $paint = Capture-Workbench $Process $Filename
          if ($paint.clientPaint) { return @{ semantics=$last; paint=$paint } }
        }
      }
      Start-Sleep -Milliseconds 200
    }
    $diagnostic = 'Native workbench content/data/paint readiness deadline exceeded: ' + (@{ semantics=$last; paint=$paint } | ConvertTo-Json -Compress)
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
  $ui = Start-Process -FilePath $app -PassThru
  try {
    $firstReady = Wait-Window $ui 'windows-ui.png'
    [FoundryWindow]::SetForegroundWindow($ui.MainWindowHandle) | Out-Null
    $second = Start-Process -FilePath $app -PassThru
    if (-not $second.WaitForExit(15000)) { throw 'Second native instance did not yield ownership' }
    $firstReady = Wait-Window $ui 'windows-ui.png'
    if (-not $ui.CloseMainWindow() -or -not $ui.WaitForExit(15000)) { throw 'Native clean quit failed' }
    $profileDB = Join-Path $actualData 'workbench\foundry.db'
    if (-not (Test-Path $profileDB)) { throw 'LocalAppData persistent database missing' }
    $ui = Start-Process -FilePath $app -PassThru
    $restartReady = Wait-Window $ui 'windows-ui-restart.png'
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
  @{ result='PASS'; platform='native Windows x64'; installed=$installed; unsigned=$true; runtime=$runtime; nativeUI='OS UI Automation workbench controls and received data, plus painted client screenshot; no manual interactive acceptance claimed'; firstUI=$firstReady; restartUI=$restartReady; secondInstance='PASS'; restart='PASS'; uninstall='PASS; data and simulation evidence retained'; powerLossCertified=$false } | ConvertTo-Json -Depth 8 | Set-Content (Join-Path $Output 'native-smoke.json') -Encoding utf8
} finally { $env:PATH=$oldPath }
