$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'Native Windows required' }

Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
. (Join-Path $PSScriptRoot 'windows-taskdialog-action.ps1')

function Assert-True([bool]$Value, [string]$Message) {
  if (-not $Value) { throw $Message }
}

function Wait-FixtureDialog([int]$ProcessId, [int]$Seconds) {
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
    'Open Controlled Coding Workbench?'
  )
  $conditionArray = [System.Windows.Automation.Condition[]]@(
    $processCondition,
    $typeCondition,
    $nameCondition
  )
  $conditions = New-Object 'System.Windows.Automation.AndCondition' -ArgumentList (,$conditionArray)
  $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
  while ([DateTime]::UtcNow -lt $deadline) {
    $dialog = [System.Windows.Automation.AutomationElement]::RootElement.FindFirst(
      [System.Windows.Automation.TreeScope]::Descendants,
      $conditions
    )
    if ($null -ne $dialog -and $dialog.Current.IsEnabled -and -not $dialog.Current.IsOffscreen) {
      return $dialog
    }
    Start-Sleep -Milliseconds 50
  }
  throw 'Pinned Electron TaskDialog fixture unavailable'
}

function Assert-RejectedFact([object]$Facts, [string]$Property, [object]$Value) {
  $original = $Facts.$Property
  try {
    $Facts.$Property = $Value
    $caught = $null
    try { [FoundryTaskDialogNative]::ValidateFacts($Facts) } catch { $caught = $_ }
    Assert-True ($null -ne $caught) "Invalid TaskDialog fact was accepted: $Property"
  } finally {
    $Facts.$Property = $original
  }
}

function Find-FixtureAction([object]$Dialog, [int]$ProcessId, [string]$Name) {
  $items = $Dialog.FindAll(
    [System.Windows.Automation.TreeScope]::Descendants,
    [System.Windows.Automation.Condition]::TrueCondition
  )
  if ($items.Count -gt 256) { throw 'Fixture UI tree exceeds bound' }
  $match = $null
  foreach ($element in $items) {
    $current = $element.Current
    $allowedType =
      $current.ControlType -eq [System.Windows.Automation.ControlType]::Button -or
      $current.ControlType -eq [System.Windows.Automation.ControlType]::Pane
    if (
      $current.ProcessId -eq $ProcessId -and
      $allowedType -and
      $current.Name -ceq $Name -and
      $current.IsEnabled -and
      -not $current.IsOffscreen -and
      $current.NativeWindowHandle -ne 0
    ) {
      if ($null -ne $match) { throw 'Fixture native action is ambiguous' }
      $match = $element
    }
  }
  if ($null -eq $match) { throw 'Fixture native action unavailable' }
  return $match
}

$electron = Join-Path $PSScriptRoot 'node_modules/electron/dist/electron.exe'
$fixtureScript = Join-Path $PSScriptRoot 'test/windows-taskdialog-fixture.cjs'
if (-not (Test-Path -LiteralPath $electron -PathType Leaf)) { throw 'Pinned Electron runtime missing' }
if (-not (Test-Path -LiteralPath $fixtureScript -PathType Leaf)) { throw 'TaskDialog fixture script missing' }
$fixtureRoot = Join-Path $env:TEMP ('Foundry TaskDialog ' + [Guid]::NewGuid().ToString('N'))
$resultPath = Join-Path $fixtureRoot 'result.json'
$profilePath = Join-Path $fixtureRoot 'profile'
New-Item -ItemType Directory -Path $fixtureRoot | Out-Null
$oldResult = $env:FOUNDRY_TASKDIALOG_FIXTURE_RESULT
$process = $null
$dialog = $null
$OwnerProcess = 0
$primaryFailure = $null
$cleanupFailure = $null
try {
  $env:FOUNDRY_TASKDIALOG_FIXTURE_RESULT = $resultPath
  $process = Start-Process -FilePath $electron -ArgumentList @(
    "--user-data-dir=`"$profilePath`"",
    "`"$fixtureScript`""
  ) -PassThru
  $OwnerProcess = $process.Id
  $dialog = Wait-FixtureDialog $OwnerProcess 10
  $action = Wait-CodingTaskDialogAction $dialog $OwnerProcess 5
  $facts = [FoundryTaskDialogNative]::CaptureFacts(
    (Get-AutomationNativeHandle $dialog),
    (Get-AutomationNativeHandle $action),
    $OwnerProcess
  )
  [FoundryTaskDialogNative]::ValidateFacts($facts)
  Assert-RejectedFact $facts 'DialogOwned' $false
  Assert-RejectedFact $facts 'DialogVisible' $false
  Assert-RejectedFact $facts 'DialogEnabled' $false
  Assert-RejectedFact $facts 'ActionOwned' $false
  Assert-RejectedFact $facts 'ActionIsChild' $false
  Assert-RejectedFact $facts 'DialogClass' 'OtherDialog'
  Assert-RejectedFact $facts 'DialogTitle' 'Other title'
  Assert-RejectedFact $facts 'ActionClass' 'Pane'
  Assert-RejectedFact $facts 'ActionTitle' 'Open Another Workbench'
  Assert-RejectedFact $facts 'ActionStyleType' 2
  Assert-RejectedFact $facts 'ActionVisible' $false
  Assert-RejectedFact $facts 'ActionEnabled' $false

  $wrongOwner = $OwnerProcess + 1
  $caught = $null
  try { Invoke-CodingTaskDialogAction $dialog $action $wrongOwner } catch { $caught = $_ }
  Assert-True ($null -ne $caught) 'Wrong TaskDialog owner did not fail closed'

  $cancel = Find-FixtureAction $dialog $OwnerProcess 'Cancel'
  $caught = $null
  try { Invoke-CodingTaskDialogAction $dialog $cancel $OwnerProcess } catch { $caught = $_ }
  Assert-True ($null -ne $caught) 'Wrong native TaskDialog action did not fail closed'

  Invoke-CodingTaskDialogAction $dialog $action $OwnerProcess
  if (-not $process.WaitForExit(10000)) { throw 'Pinned Electron TaskDialog fixture did not exit' }
  if ($process.ExitCode -ne 0) { throw 'Pinned Electron TaskDialog fixture failed' }
  if (-not (Test-Path -LiteralPath $resultPath -PathType Leaf)) { throw 'TaskDialog fixture receipt missing' }
  $receipt = Get-Content -Raw -LiteralPath $resultPath | ConvertFrom-Json
  $launches = @($receipt.launches)
  Assert-True ($launches.Count -eq 1) 'TaskDialog fixture launch count mismatch'
  Assert-True ($launches[0] -ceq 'coding') 'TaskDialog fixture mode mismatch'
} catch {
  $primaryFailure = $_
  try {
    if ($null -ne $dialog -and $dialog.Current.ProcessId -eq $OwnerProcess) {
      try { Write-CodingTaskDialogDiagnostic $dialog $OwnerProcess } catch {
        [Console]::Error.WriteLine('coding-confirmation-button: fixture diagnostic unavailable')
      }
    }
  } catch [System.Windows.Automation.ElementNotAvailableException] {}
} finally {
  try {
    $env:FOUNDRY_TASKDIALOG_FIXTURE_RESULT = $oldResult
  } catch {
    $cleanupFailure = $_
  }
  try {
    if ($null -ne $process) {
      $process.Refresh()
      if (-not $process.HasExited) {
        Stop-Process -Id $process.Id -Force
        $process.WaitForExit()
      }
    }
  } catch {
    if ($null -eq $cleanupFailure) { $cleanupFailure = $_ }
  }
  try {
    if (Test-Path -LiteralPath $fixtureRoot) {
      Remove-Item -LiteralPath $fixtureRoot -Recurse -Force
    }
  } catch {
    if ($null -eq $cleanupFailure) { $cleanupFailure = $_ }
  }
}
if ($null -ne $primaryFailure) { throw $primaryFailure }
if ($null -ne $cleanupFailure) { throw $cleanupFailure }

Write-Output 'PASS: pinned Electron native TaskDialog action contract'
