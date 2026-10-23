$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'Native Windows required' }

Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes

function Assert-True([bool]$Value, [string]$Message) {
  if (-not $Value) { throw $Message }
}

function Get-ModeFunctionDefinition([string]$Name) {
  $tokens = $null
  $errors = $null
  $path = Join-Path $PSScriptRoot 'windows-ui-mode.ps1'
  $ast = [System.Management.Automation.Language.Parser]::ParseFile(
    $path,
    [ref]$tokens,
    [ref]$errors
  )
  if ($errors.Count -ne 0) { throw 'Native mode driver failed to parse' }
  $definitions = @($ast.FindAll({
    param($node)
    $node -is [System.Management.Automation.Language.FunctionDefinitionAst]
  }, $true))
  $matches = @($definitions | Where-Object { $_.Name -ceq $Name })
  if ($matches.Count -ne 1) { throw "Expected one native mode function: $Name" }
  return $matches[0].Extent.Text
}

foreach ($name in @(
  'Find-DescendantExact',
  'Write-OwnedDialogDiagnostic',
  'Wait-DescendantExact',
  'Invoke-Exact'
)) { . ([ScriptBlock]::Create((Get-ModeFunctionDefinition $name))) }

function Start-NativeFixture([string]$Mode) {
  $title = 'Foundry UI wait fixture ' + [Guid]::NewGuid().ToString('N')
  $eventName = 'Local\FoundryUiWait' + [Guid]::NewGuid().ToString('N')
  $release = [Threading.EventWaitHandle]::new(
    $false,
    [Threading.EventResetMode]::ManualReset,
    $eventName
  )
  $oldTitle = $env:FOUNDRY_UI_FIXTURE_TITLE
  $oldMode = $env:FOUNDRY_UI_FIXTURE_MODE
  $oldEvent = $env:FOUNDRY_UI_FIXTURE_EVENT
  try {
    $env:FOUNDRY_UI_FIXTURE_TITLE = $title
    $env:FOUNDRY_UI_FIXTURE_MODE = $Mode
    $env:FOUNDRY_UI_FIXTURE_EVENT = $eventName
    $source = @'
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$form = New-Object System.Windows.Forms.Form
$form.Text = $env:FOUNDRY_UI_FIXTURE_TITLE
$form.Width = 520
$form.Height = 220
$form.StartPosition = [System.Windows.Forms.FormStartPosition]::CenterScreen
$form.add_Shown({
  if ($env:FOUNDRY_UI_FIXTURE_MODE -ceq 'delayed') {
    $release = [Threading.EventWaitHandle]::OpenExisting($env:FOUNDRY_UI_FIXTURE_EVENT)
    $timer = New-Object System.Windows.Forms.Timer
    $timer.Interval = 50
    $form.Tag = @{ timer=$timer; release=$release }
    $timer.add_Tick({
      if (-not $form.Tag.release.WaitOne(0)) { return }
      $form.Tag.timer.Stop()
      $button = New-Object System.Windows.Forms.Button
      $button.Text = 'Open Coding Workbench'
      $button.Enabled = $true
      $button.Width = 190
      $button.Height = 32
      $button.Left = 20
      $button.Top = 30
      $form.Controls.Add($button)
    })
    $timer.Start()
  } else {
    $wrong = New-Object System.Windows.Forms.Button
    $wrong.Text = 'Open Another Workbench'
    $wrong.Enabled = $true
    $wrong.Width = 190
    $wrong.Height = 32
    $wrong.Left = 20
    $wrong.Top = 30
    $form.Controls.Add($wrong)
    $disabled = New-Object System.Windows.Forms.Button
    $disabled.Text = 'Open Coding Workbench'
    $disabled.Enabled = $false
    $disabled.Width = 190
    $disabled.Height = 32
    $disabled.Left = 230
    $disabled.Top = 30
    $form.Controls.Add($disabled)
  }
})
try { [System.Windows.Forms.Application]::Run($form) } finally {
  if ($null -ne $form.Tag -and $null -ne $form.Tag.release) { $form.Tag.release.Dispose() }
}
'@
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($source))
    $powerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $process = Start-Process -FilePath $powerShell -ArgumentList @(
      '-NoProfile',
      '-NonInteractive',
      '-STA',
      '-EncodedCommand',
      $encoded
    ) -PassThru
    return @{ process=$process; title=$title; release=$release }
  } catch {
    $release.Dispose()
    throw
  } finally {
    $env:FOUNDRY_UI_FIXTURE_TITLE = $oldTitle
    $env:FOUNDRY_UI_FIXTURE_MODE = $oldMode
    $env:FOUNDRY_UI_FIXTURE_EVENT = $oldEvent
  }
}

function Wait-NativeFixture([int]$ProcessId, [string]$Title, [int]$Seconds) {
  $processCondition = New-Object System.Windows.Automation.PropertyCondition -ArgumentList @(
    [System.Windows.Automation.AutomationElement]::ProcessIdProperty,
    $ProcessId
  )
  $typeCondition = New-Object System.Windows.Automation.PropertyCondition -ArgumentList @(
    [System.Windows.Automation.AutomationElement]::ControlTypeProperty,
    [System.Windows.Automation.ControlType]::Window
  )
  $nameCondition = New-Object System.Windows.Automation.PropertyCondition -ArgumentList @(
    [System.Windows.Automation.AutomationElement]::NameProperty,
    $Title
  )
  $conditionArray = [System.Windows.Automation.Condition[]]@(
    $processCondition,
    $typeCondition,
    $nameCondition
  )
  $conditions = New-Object 'System.Windows.Automation.AndCondition' -ArgumentList (,$conditionArray)
  $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
  while ([DateTime]::UtcNow -lt $deadline) {
    $window = [System.Windows.Automation.AutomationElement]::RootElement.FindFirst(
      [System.Windows.Automation.TreeScope]::Descendants,
      $conditions
    )
    if ($null -ne $window -and $window.Current.IsEnabled -and -not $window.Current.IsOffscreen) {
      return $window
    }
    Start-Sleep -Milliseconds 50
  }
  throw 'Native UI fixture window unavailable'
}

function Stop-NativeFixture([object]$Fixture) {
  try {
    $process = $Fixture.process
    if ($null -eq $process) { return }
    $process.Refresh()
    if ($process.HasExited) { return }
    $process.CloseMainWindow() | Out-Null
    if (-not $process.WaitForExit(3000)) {
      Stop-Process -Id $process.Id -Force
      $process.WaitForExit()
    }
  } finally {
    if ($null -ne $Fixture.release) { $Fixture.release.Dispose() }
  }
}

$delayed = Start-NativeFixture 'delayed'
try {
  $OwnerProcess = $delayed.process.Id
  $window = Wait-NativeFixture $OwnerProcess $delayed.title 5
  $script:productionFindDescendantExact = ${function:Find-DescendantExact}
  $script:delayedFindCalls = 0
  $script:delayedRelease = $delayed.release
  Set-Item Function:\Find-DescendantExact -Value {
    param([object]$Root, [object]$Type, [string]$Name)
    $result = & $script:productionFindDescendantExact $Root $Type $Name
    $script:delayedFindCalls++
    if ($script:delayedFindCalls -eq 1) {
      Assert-True ($null -eq $result) 'Delayed exact button existed on the first wait lookup'
      $script:delayedRelease.Set() | Out-Null
      return $null
    }
    return $result
  }
  try {
    $button = Wait-DescendantExact $window ([System.Windows.Automation.ControlType]::Button) 'Open Coding Workbench' 5 'coding-confirmation-button'
  } finally {
    Set-Item Function:\Find-DescendantExact -Value $script:productionFindDescendantExact
  }
  Assert-True ($script:delayedFindCalls -ge 2) 'Waiter did not retry after the exact button was absent'
  Assert-True ($button.Current.ProcessId -eq $OwnerProcess) 'Delayed button owner mismatch'
  Assert-True ($button.Current.Name -ceq 'Open Coding Workbench') 'Delayed exact button mismatch'
  Assert-True ($button.Current.IsEnabled) 'Delayed exact button remained disabled'
  $expectedOwner = $OwnerProcess
  $OwnerProcess = $expectedOwner + 1
  $caught = $null
  try { Invoke-Exact $button 'coding-confirmation-button' } catch { $caught = $_ }
  Assert-True ($null -ne $caught) 'Other-owner action did not fail closed'
  Assert-True ($caught.Exception.Message -ceq 'coding-confirmation-button: required UI action state changed') 'Other-owner action phase mismatch'
  $OwnerProcess = $expectedOwner
} finally { Stop-NativeFixture $delayed }

$absent = Start-NativeFixture 'absent'
try {
  $OwnerProcess = $absent.process.Id
  $window = Wait-NativeFixture $OwnerProcess $absent.title 5
  $oldError = [Console]::Error
  $errorWriter = New-Object IO.StringWriter
  [Console]::SetError($errorWriter)
  try {
    $caught = $null
    try {
      Wait-DescendantExact $window ([System.Windows.Automation.ControlType]::Button) 'Open Coding Workbench' 1 'coding-confirmation-button' | Out-Null
    } catch { $caught = $_ }
  } finally { [Console]::SetError($oldError) }
  Assert-True ($null -ne $caught) 'Absent exact enabled button did not fail closed'
  Assert-True ($caught.Exception.Message -ceq 'coding-confirmation-button: required owned dialog element unavailable') 'Absent button phase mismatch'
  $diagnostic = $errorWriter.ToString()
  Assert-True ($diagnostic.Contains('Owned dialog candidates:')) 'Absent button diagnostic missing'
  Assert-True ($diagnostic.Contains('"phase":"coding-confirmation-button"')) 'Absent button diagnostic phase missing'
  Assert-True ($diagnostic.Contains('"exactNameDisabledButtons":1')) 'Disabled exact button was not diagnosed'

  $OwnerProcess = $absent.process.Id + 1
  $caught = $null
  try {
    Wait-DescendantExact $window ([System.Windows.Automation.ControlType]::Button) 'Open Coding Workbench' 1 'coding-confirmation-button' | Out-Null
  } catch { $caught = $_ }
  Assert-True ($null -ne $caught) 'Other-owner dialog did not fail closed'
  Assert-True ($caught.Exception.Message -ceq 'coding-confirmation-button: owned dialog identity mismatch') 'Other-owner phase mismatch'
} finally { Stop-NativeFixture $absent }

Write-Output 'PASS: native UI descendant readiness helper'
