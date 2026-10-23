param([Parameter(Mandatory=$true)][long]$WindowHandle, [Parameter(Mandatory=$true)][int]$OwnerProcess)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
# The fixed OS Windows PowerShell host loads its inbox .NET UI Automation
# assemblies. Querying the accessibility provider enables Chromium's tree;
# no renderer/debug/activation switch is allowed or needed.
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
$root = [System.Windows.Automation.AutomationElement]::FromHandle([IntPtr]$WindowHandle)
if ($null -eq $root -or $root.Current.ProcessId -ne $OwnerProcess) { throw 'Window ownership mismatch' }
$walker = [System.Windows.Automation.TreeWalker]::ControlViewWalker
$nodes = New-Object 'System.Collections.Generic.List[object]'
$queue = New-Object 'System.Collections.Generic.Queue[object]'
$queue.Enqueue(@{ element=$root; parent=-1; depth=0 })
while ($queue.Count -gt 0) {
  if ($nodes.Count + $queue.Count -gt 2048) { throw 'UI tree exceeds bound' }
  $item = $queue.Dequeue()
  if ($item.depth -ge 32) { throw 'UI tree depth exceeds bound' }
  $element = $item.element
  $current = $element.Current
  $name = $current.Name
  if ($name.Length -gt 512) { throw 'UI name exceeds bound' }
  $index = $nodes.Count
  $record = @{ parent=$item.parent; type=($current.ControlType.ProgrammaticName -replace '^ControlType\.', ''); name=$name; visible=(-not $current.IsOffscreen -and $current.BoundingRectangle.Width -gt 0 -and $current.BoundingRectangle.Height -gt 0); enabled=$current.IsEnabled }
  if ($current.ControlType -eq [System.Windows.Automation.ControlType]::CheckBox) {
    $toggle = $null
    $record.toggleState = if ($element.TryGetCurrentPattern([System.Windows.Automation.TogglePattern]::Pattern, [ref]$toggle)) { $toggle.Current.ToggleState.ToString() } else { 'Unavailable' }
  }
  $nodes.Add($record)
  $child = $walker.GetFirstChild($element)
  while ($null -ne $child) {
    if ($nodes.Count + $queue.Count -ge 2048) { throw 'UI tree exceeds bound' }
    $queue.Enqueue(@{ element=$child; parent=$index; depth=($item.depth+1) })
    $child = $walker.GetNextSibling($child)
  }
}
@{ window=$WindowHandle.ToString(); processId=$OwnerProcess; nodes=$nodes.ToArray() } | ConvertTo-Json -Depth 5 -Compress
