$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'Native Windows required' }
function Assert-True([bool]$Value, [string]$Message) { if (-not $Value) { throw $Message } }
function Assert-Rejected([scriptblock]$Action, [string]$Expected) {
 $message = ''
 try { & $Action | Out-Null } catch { $message = $_.Exception.Message }
 Assert-True ($message.Contains($Expected)) ('Expected rejection: ' + $Expected + '; actual: ' + $message)
}

# Load only the actual functions. The production script's installation and
# upgrade actions never run; Start-Process below is a local recording fake.
$tokens = $null; $errors = $null
$source = Join-Path $PSScriptRoot 'windows-upgrade-smoke.ps1'
$ast = [System.Management.Automation.Language.Parser]::ParseFile($source, [ref]$tokens, [ref]$errors)
Assert-True ($errors.Count -eq 0) 'Stable supplement PowerShell syntax invalid'
foreach ($name in @('Install-Exact','Wait-StableUninstall')) {
 $functions = @($ast.FindAll({ param($a) $a -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $a.Name -ceq $name }, $true))
 Assert-True ($functions.Count -eq 1) 'Actual stable supplement function missing/ambiguous'
 Invoke-Expression $functions[0].Extent.Text
 if ($name -ceq 'Wait-StableUninstall') {
  $actions = @($functions[0].FindAll({ param($a) $a -is [System.Management.Automation.Language.CommandAst] -and $a.GetCommandName() -in @('Start-Process','Remove-Item','Stop-Process','Install-Exact') }, $true))
  Assert-True ($actions.Count -eq 0) 'Observation helper performs an action'
 }
}
$script:launches = 0
$script:fakeProcess = [pscustomobject]@{ ExitCode=0 }
$script:fakeProcess | Add-Member ScriptMethod WaitForExit { param($Milliseconds) return $true }
function Start-Process([string]$FilePath, [string[]]$ArgumentList, [switch]$PassThru) { $script:launches++; return $script:fakeProcess }
$launch = @{}
Install-Exact 'inert-test-only' @('/S') $launch
Assert-True ($script:launches -eq 1 -and $launch.launcher_started -and $launch.launcher_returned -and $launch.launcher_exit_code -eq 0) 'Single successful launcher receipt invalid'
$script:fakeProcess.ExitCode = 7
$badLaunch = @{}
Assert-Rejected { Install-Exact 'inert-test-only' @('/S') $badLaunch } 'installer failed'
Assert-True ($script:launches -eq 2 -and $badLaunch.launcher_exit_code -eq 7) 'Nonzero launcher was retried or hidden'
$script:fakeProcess.ExitCode = 0

function New-UninstallScenario([object[]]$Frames) {
 $state = @{ milliseconds=[double]0; polls=0; reads=0 }
 $root = 'C:\inert-owned-fixture'
 $paths = @{}
 $paths[(Join-Path $root 'Foundry.exe')] = 'app'
 $paths[(Join-Path $root 'resources/sandbox')] = 'sandbox'
 $paths[(Join-Path $root 'resources/sandbox-v2')] = 'sandbox_v2'
 $exists = {
  param($Path)
  if (-not $paths.ContainsKey($Path)) { throw 'Observation escaped fixed owned paths' }
  $category = $paths[$Path]
  if ($category -ceq 'app') { $state.polls++ }
  $state.reads++
  $frame = $Frames[[Math]::Min($state.polls-1,$Frames.Count-1)]
  return [bool]($frame.remaining -ccontains $category)
 }.GetNewClosure()
 $clock = { return [double]$state.milliseconds }.GetNewClosure()
 $pause = { param($Milliseconds) $state.milliseconds += $Milliseconds }.GetNewClosure()
 return @{ state=$state; root=$root; exists=$exists; clock=$clock; pause=$pause }
}
$scenario = New-UninstallScenario @(@{remaining=@('sandbox','sandbox_v2')}, @{remaining=@('sandbox_v2')}, @{remaining=@()})
$receipt = Wait-StableUninstall $scenario.root $launch $scenario.exists $scenario.clock $scenario.pause
Assert-True ($receipt.outcome -ceq 'absent' -and $receipt.polls -eq 3 -and $receipt.elapsed_ms -eq 400 -and $receipt.remaining.Count -eq 0) 'Exe disappearance accepted before both runtimes disappeared'
Assert-True ($scenario.state.reads -eq 9 -and $script:launches -eq 2) 'Observation skipped a fixed path or relaunched uninstall'
function Test-UnclosedCallerClock {
 # Like the production Stopwatch callback, these blocks resolve caller-local
 # state dynamically rather than capturing it in a separate module/closure.
 $callerState = @{ milliseconds=[double]0 }
 $scenario = New-UninstallScenario @(@{remaining=@('sandbox')}, @{remaining=@()})
 $receipt = Wait-StableUninstall $scenario.root $launch $scenario.exists { return [double]$callerState.milliseconds } { param($Milliseconds) $callerState.milliseconds += $Milliseconds }
 Assert-True ($receipt.outcome -ceq 'absent' -and $receipt.polls -eq 2 -and $receipt.elapsed_ms -eq 200) 'Unclosed caller-local clock/pause did not resolve like production'
}
Test-UnclosedCallerClock
$scenario = New-UninstallScenario @(@{remaining=@()})
$receipt = Wait-StableUninstall $scenario.root $launch $scenario.exists $scenario.clock $scenario.pause
Assert-True ($receipt.polls -eq 1 -and $receipt.elapsed_ms -eq 0 -and $scenario.state.milliseconds -eq 0) 'Already absent files required another action/wait'
foreach ($category in @('app','sandbox','sandbox_v2')) {
 $scenario = New-UninstallScenario @(@{remaining=@($category)})
 Assert-Rejected { Wait-StableUninstall $scenario.root $launch $scenario.exists $scenario.clock $scenario.pause } 'left app/runtime bytes'
 Assert-True ($scenario.state.milliseconds -eq 30000 -and $scenario.state.polls -le 151 -and $script:launches -eq 2) 'Persistent residue escaped the original deadline or relaunched uninstall'
}
$scenario = New-UninstallScenario @(@{remaining=@()})
Assert-Rejected { Wait-StableUninstall $scenario.root $badLaunch $scenario.exists $scenario.clock $scenario.pause } 'launcher not successful'
Assert-True ($scenario.state.reads -eq 0) 'Nonzero launcher was accepted based on absent files'
foreach ($invalid in @([double]::NaN,[double]::PositiveInfinity,[double]-1,$null,'private-invalid-clock')) {
 $scenario = New-UninstallScenario @(@{remaining=@()})
 $badClock = { return $invalid }.GetNewClosure()
 Assert-Rejected { Wait-StableUninstall $scenario.root $launch $scenario.exists $badClock $scenario.pause } 'invalid observation clock'
}
$scenario = New-UninstallScenario @(@{remaining=@('app')})
$state = $scenario.state
$backwards = { param($Milliseconds) $state.milliseconds -= $Milliseconds }
Assert-Rejected { Wait-StableUninstall $scenario.root $launch $scenario.exists $scenario.clock $backwards } 'invalid observation clock'
$scenario = New-UninstallScenario @(@{remaining=@('app')})
Assert-Rejected { Wait-StableUninstall $scenario.root $launch $scenario.exists $scenario.clock { param($Milliseconds) } } 'observation poll bound'
Assert-True ($scenario.state.polls -eq 151) 'Frozen clock left an unbounded loop'
$scenario = New-UninstallScenario @(@{remaining=@()})
$state = $scenario.state
$lateAbsent = { param($Path) $state.milliseconds=[double]30000; return $false }
Assert-Rejected { Wait-StableUninstall $scenario.root $launch $lateAbsent $scenario.clock $scenario.pause } 'observation deadline'
$scenario = New-UninstallScenario @(@{remaining=@()})
Assert-Rejected { Wait-StableUninstall $scenario.root $launch { param($Path) throw 'private-path-must-not-leak' } $scenario.clock $scenario.pause } 'observation read failed'
$launch.private = 'private-launch-must-not-leak'
$receipt = Wait-StableUninstall $scenario.root $launch $scenario.exists $scenario.clock $scenario.pause
$json = $receipt | ConvertTo-Json -Depth 4 -Compress
Assert-True ($json.Length -lt 1024 -and $json -notmatch 'private|inert|C:') 'Closed uninstall receipt leaked data'
Assert-True ($script:launches -eq 2) 'Observation invoked another uninstaller'
Write-Output 'PASS: actual stable uninstall observation, delayed runtime absence, strict deadline, invalid clock, closed diagnostics and single launch'
