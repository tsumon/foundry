$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'Native Windows required' }
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes

function Assert-True([bool]$Value, [string]$Message) { if (-not $Value) { throw $Message } }
function Assert-Rejected([scriptblock]$Action, [string]$Expected) {
  $errorText = ''
  try { & $Action | Out-Null } catch { $errorText = $_.Exception.Message }
  Assert-True ($errorText.Contains($Expected)) ('Expected rejection: ' + $Expected + '; actual: ' + $errorText)
}

# Execute the actual decision function, not a second implementation. Parsing
# only this AST also proves the pure tests cannot launch the native adapter.
$tokens = $null
$errors = $null
$source = Join-Path $PSScriptRoot 'windows-ui-scroll.ps1'
$ast = [System.Management.Automation.Language.Parser]::ParseFile($source, [ref]$tokens, [ref]$errors)
Assert-True ($errors.Count -eq 0) 'Scroll driver PowerShell syntax invalid'
foreach ($name in @('Test-CodingScrollReady', 'Get-CodingScrollDecision')) {
  $functions = @($ast.FindAll({ param($a) $a -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $a.Name -ceq $name }, $true))
  Assert-True ($functions.Count -eq 1) 'Actual scroll decision function missing/ambiguous'
  Invoke-Expression $functions[0].Extent.Text
}

function New-Facts {
  return @{
    upper=@{ matches=1; owned=$true; enabled=$true; visible=$false; itemAvailable=$true; top=2800.0; bottom=2820.0 }
    checkbox=@{ matches=1; owned=$true; enabled=$true; unchecked=$true; visible=$false; itemAvailable=$true; top=3000.0; bottom=3020.0 }
    refresh=@{ matches=1; owned=$true; enabled=$true; visible=$false; itemAvailable=$true; top=3040.0; bottom=3070.0 }
    scroll=@{ available=$true; owned=$true; percent=0.0; viewSize=15.0; top=0.0; bottom=600.0 }
    fingerprint='initial'
  }
}
Assert-True (-not (Test-CodingScrollReady $null)) 'Missing loading anchors were considered ready'
$loading=New-Facts; $loading.checkbox.enabled=$false; $loading.refresh.enabled=$false
Assert-True (-not (Test-CodingScrollReady $loading)) 'Disabled loading anchors were considered ready'
$loading.checkbox.enabled=$true; $loading.refresh.enabled=$true
Assert-True (Test-CodingScrollReady $loading) 'Loading-to-ready transition not observed'
$loading.checkbox.unchecked=$false
Assert-Rejected { Test-CodingScrollReady $loading } 'opt-in state changed'
$facts = New-Facts
$decision = Get-CodingScrollDecision $facts 0 $null @{}
Assert-True ($decision.kind -ceq 'item' -and $decision.target -ceq 'refresh') 'Tall section did not target actual bottom control'
# A provider may align refresh at the top: upper metadata must still be seen.
$facts.refresh.visible=$true
$facts.checkbox.visible=$true
$decision = Get-CodingScrollDecision $facts 1 $null @{ refresh=$true }
Assert-True ($decision.kind -ceq 'item' -and $decision.target -ceq 'upper') 'Clipped provider/approval region was accepted'
$facts.upper.visible=$true
Assert-True ((Get-CodingScrollDecision $facts 16 $null @{}).kind -ceq 'done') 'Already-visible final state rejected'
foreach ($anchor in @('upper','checkbox','refresh')) {
  foreach ($property in @('owned','enabled')) {
    $bad=New-Facts; $bad.$anchor.$property=$false
    Assert-Rejected { Get-CodingScrollDecision $bad 0 $null @{} } 'identity/state'
  }
  foreach ($count in @(0,2)) {
    $bad=New-Facts; $bad.$anchor.matches=$count
    Assert-Rejected { Get-CodingScrollDecision $bad 0 $null @{} } 'identity/state'
  }
}
$bad=New-Facts; $bad.checkbox.unchecked=$false
Assert-Rejected { Get-CodingScrollDecision $bad 0 $null @{} } 'opt-in state changed'
$bad=New-Facts
Assert-Rejected { Get-CodingScrollDecision $bad 1 $bad @{} } 'no progress'
Assert-Rejected { Get-CodingScrollDecision $bad 16 $null @{} } 'bound exceeded'
$facts=New-Facts; $facts.refresh.itemAvailable=$false
$decision=Get-CodingScrollDecision $facts 0 $null @{}
Assert-True ($decision.kind -ceq 'percent' -and $decision.direction -ceq 'down' -and $decision.percent -gt 0 -and $decision.percent -le 100) 'Downward fallback invalid'
$facts.scroll.percent=100; $facts.refresh.top=-1000; $facts.refresh.bottom=-970
$decision=Get-CodingScrollDecision $facts 0 $null @{}
Assert-True ($decision.direction -ceq 'up' -and $decision.percent -lt 100) 'Above-target fallback did not reverse direction'
$facts.scroll.available=$false
Assert-Rejected { Get-CodingScrollDecision $facts 0 $null @{} } 'ancestor unavailable'
$facts=New-Facts; $facts.refresh.itemAvailable=$false; $facts.scroll.owned=$false
Assert-Rejected { Get-CodingScrollDecision $facts 0 $null @{} } 'ancestor unavailable'
$facts.scroll.owned=$true; $facts.scroll.percent=100
Assert-Rejected { Get-CodingScrollDecision $facts 0 $null @{} } 'boundary reached'
$facts.scroll.percent=0; $facts.refresh.top=[double]::NaN
Assert-Rejected { Get-CodingScrollDecision $facts 0 $null @{} } 'nonfinite'
$facts=New-Facts; $facts.refresh.itemAvailable=$false; $facts.scroll.viewSize=100
Assert-Rejected { Get-CodingScrollDecision $facts 0 $null @{} } 'invalid geometry'
$facts=New-Facts; $facts.refresh.itemAvailable=$false; $facts.refresh.top=100; $facts.refresh.bottom=130
Assert-Rejected { Get-CodingScrollDecision $facts 0 $null @{} } 'without vertical displacement'
Write-Output 'PASS: actual bounded scroll policy, identity/state and progress regressions'

. $source
$electron = Join-Path $PSScriptRoot 'node_modules/electron/dist/electron.exe'
$fixture = Join-Path $PSScriptRoot 'test/windows-scroll-fixture.cjs'
$node = (Get-Command node.exe).Source
$fixtureRoot = Join-Path $env:TEMP ('Foundry Scroll ' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixtureRoot | Out-Null
$process = $null
$primaryFailure = $null
$cleanupFailure = $null
try {
  $process = Start-Process -FilePath $electron -ArgumentList @("--user-data-dir=`"$fixtureRoot`"", "`"$fixture`"") -PassThru
  $section = $null
  $deadline = [DateTime]::UtcNow.AddSeconds(15)
  while ([DateTime]::UtcNow -lt $deadline -and $null -eq $section) {
    $process.Refresh()
    if ($process.HasExited) { throw 'Native scroll fixture exited' }
    if ($process.MainWindowHandle -ne [IntPtr]::Zero) {
      $root = [System.Windows.Automation.AutomationElement]::FromHandle($process.MainWindowHandle)
      $items = $root.FindAll([System.Windows.Automation.TreeScope]::Descendants, [System.Windows.Automation.Condition]::TrueCondition)
      if ($items.Count -gt 2048) { throw 'Native fixture tree exceeds bound' }
      foreach ($item in $items) {
        if ($item.Current.ProcessId -eq $process.Id -and $item.Current.ControlType -eq [System.Windows.Automation.ControlType]::Group -and $item.Current.Name -ceq '账户模型编码设置') { $section=$item; break }
      }
    }
    if ($null -eq $section) { Start-Sleep -Milliseconds 100 }
  }
  Assert-True ($null -ne $section) 'Native long-section fixture unavailable'
  foreach ($start in @('top','bottom')) {
    $capture=Get-CodingScrollCapture $section $process.Id
    Assert-True ($null -ne $capture.scroll) 'Native fixture lacks scroll ancestor'
    $percent=if ($start -ceq 'top') { 0.0 } else { 100.0 }
    $capture.scroll.SetScrollPercent([System.Windows.Automation.ScrollPattern]::NoScroll,$percent)
    Start-Sleep -Milliseconds 100
    $capture=Get-CodingScrollCapture $section $process.Id
    Assert-True (-not $capture.facts.refresh.visible) 'Fixture did not establish an offscreen target'
    if ($start -ceq 'bottom') {
      # Force the real ancestor adapter path without a production test flag.
      $decision=Get-CodingScrollDecision $capture.facts 0 $null @{ upper=$true; checkbox=$true; refresh=$true }
      Assert-True ($decision.kind -ceq 'percent' -and $decision.direction -ceq 'up') 'Native above-target fallback not selected'
      Invoke-CodingScrollStep $capture $decision
      Start-Sleep -Milliseconds 100
    }
    Scroll-CodingControlsIntoView $section $process.Id
    $capture=Get-CodingScrollCapture $section $process.Id
    Assert-True ($capture.facts.upper.visible -and $capture.facts.checkbox.visible -and $capture.facts.refresh.visible -and $capture.facts.checkbox.unchecked) 'Native controls not visible and unchanged'
    $raw = & $node (Join-Path $PSScriptRoot 'windows-ui-ready.cjs') $process.MainWindowHandle.ToInt64().ToString() $process.Id.ToString() 'coding-vm-configurable'
    if ($LASTEXITCODE -ne 0) { throw 'Native same-viewport fixture readiness driver failed' }
    $ready=($raw -join "`n") | ConvertFrom-Json
    Assert-True ($ready.ready -eq $true) ('Native same-viewport provider/approval/controls not ready: ' + ($ready | ConvertTo-Json -Depth 8 -Compress))
  }
  Assert-Rejected { Get-CodingScrollCapture $section ($process.Id+1) } 'ownership changed'
  if (-not $process.CloseMainWindow() -or -not $process.WaitForExit(10000) -or $process.ExitCode -ne 0) { throw 'Native scroll fixture close failed' }
} catch { $primaryFailure=$_ } finally {
  try {
    if ($null -ne $process) {
      $process.Refresh()
      if (-not $process.HasExited) { Stop-Process -Id $process.Id -Force; $process.WaitForExit() }
    }
    if (Test-Path -LiteralPath $fixtureRoot) { Remove-Item -LiteralPath $fixtureRoot -Recurse -Force }
  } catch { $cleanupFailure=$_ }
}
if ($null -ne $primaryFailure) { throw $primaryFailure }
if ($null -ne $cleanupFailure) { throw $cleanupFailure }
Write-Output 'PASS: pinned Electron long-section UIA item/ancestor scrolling and strict same-viewport readiness'
