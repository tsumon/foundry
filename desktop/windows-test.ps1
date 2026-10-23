$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
function Checked([string]$Command, [string[]]$Arguments) {
  & $Command @Arguments
  if ($LASTEXITCODE -ne 0) { throw "Native gate failed: $Command $Arguments" }
}
if (-not $IsWindows) { throw 'Native Windows required' }
if ($env:CGO_ENABLED -ne '1') { throw 'Real SQLite CGO required' }
if ($env:CC -ne 'C:\mingw64\bin\gcc.exe') { throw 'Fixed native GCC required' }
Checked 'go' @('build', './cmd/...')
# Compile every test package natively; execute the Windows-supported boundary
# and integration suites below. The existing macOS full/race/vet/security gates
# remain independent requirements; Unix-only credential/FIFO tests stay there.
Checked 'go' @('test', '-run', '^$', './...')
Checked 'go' @('test', '-count=1', './internal/platformfs', './internal/gitinspect', './internal/tools', './internal/workspace', './internal/evidence', './internal/repositorytask', './internal/desktophost', './internal/keychaincheck')
Checked 'go' @('test', '-count=1', './internal/store', '-run', '^TestWindows')
Checked 'go' @('test', '-count=1', './internal/sandbox', '-run', '^TestWindows')
Checked 'go' @('test', '-count=1', './internal/model', '-run', '^Test(CredentialManager|WindowsSecret)')
Checked 'go' @('test', '-count=1', './cmd/foundry', '-run', 'Test(Desktop|DataDirectoryLock|LockProcessCrash|Windows)')
# Actual SQLite/controller source import, multi-file repair, export, explicit
# approval-before-action, protected drift, and recovery without replay.
Checked 'go' @('test', '-count=1', './cmd/foundry-acceptance', '-run', 'Test(RepositoryTask|DesktopRepository)')
Checked 'node' @('--test', 'desktop/test/security.test.cjs', 'desktop/test/downloads.test.cjs', 'desktop/test/build.test.cjs', 'desktop/test/windows.test.cjs', 'desktop/test/native-notices.test.cjs', 'desktop/test/nsis-template.test.cjs', 'desktop/test/coding-mode.test.cjs', 'desktop/test/coding-smoke.test.cjs', 'desktop/test/installed-smoke-module.test.cjs', 'desktop/test/windows-coding-ui.test.cjs')
# Real native Electron session/cookie/attachment events, in disposable profiles.
$electron = Join-Path $PWD 'desktop/node_modules/electron/dist/electron.exe'
$contract = Join-Path $PWD 'desktop/test/download-contract.cjs'
foreach ($kind in @('patch', 'report')) {
  $contractProcess = Start-Process -FilePath $electron -ArgumentList @("`"$contract`"", $kind) -Wait -PassThru
  if ($contractProcess.ExitCode -ne 0) { throw "Native Electron $kind download contract failed" }
}
Checked 'npm.cmd' @('test', '--prefix', 'web', '--', '--run')
Checked 'npm.cmd' @('audit', '--prefix', 'web', '--audit-level=low')
Checked 'npm.cmd' @('audit', '--prefix', 'desktop', '--audit-level=low')
Checked 'node' @('--test', 'desktop/test/windows-provenance.test.cjs')
