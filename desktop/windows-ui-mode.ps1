param(
  [Parameter(Mandatory=$true)][long]$WindowHandle,
  [Parameter(Mandatory=$true)][int]$OwnerProcess,
  [Parameter(Mandatory=$true)][ValidateSet('coding','persistent')][string]$Action
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'Native Windows required' }
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes

function Owned-Elements {
  $condition = New-Object System.Windows.Automation.PropertyCondition -ArgumentList @(
    [System.Windows.Automation.AutomationElement]::ProcessIdProperty,
    $OwnerProcess
  )
  $items = [System.Windows.Automation.AutomationElement]::RootElement.FindAll(
    [System.Windows.Automation.TreeScope]::Descendants,
    $condition
  )
  if ($items.Count -gt 4096) { throw 'Owned UI tree exceeds bound' }
  return $items
}

function Find-OwnedExact([object]$Type, [string]$Name) {
  foreach ($element in (Owned-Elements)) {
    $current = $element.Current
    if ($current.ControlType -eq $Type -and $current.Name -ceq $Name -and $current.IsEnabled) {
      return $element
    }
  }
  return $null
}

function Find-DescendantExact([object]$Root, [object]$Type, [string]$Name) {
  $items = $Root.FindAll(
    [System.Windows.Automation.TreeScope]::Descendants,
    [System.Windows.Automation.Condition]::TrueCondition
  )
  if ($items.Count -gt 2048) { throw 'Window UI tree exceeds bound' }
  foreach ($element in $items) {
    $current = $element.Current
    if ($current.ControlType -eq $Type -and $current.Name -ceq $Name -and $current.IsEnabled) {
      return $element
    }
  }
  return $null
}

function Find-DescendantPresent([object]$Root, [object]$Type, [string]$Name) {
  $items = $Root.FindAll(
    [System.Windows.Automation.TreeScope]::Descendants,
    [System.Windows.Automation.Condition]::TrueCondition
  )
  if ($items.Count -gt 2048) { throw 'Window UI tree exceeds bound' }
  foreach ($element in $items) {
    $current = $element.Current
    if ($current.ControlType -eq $Type -and $current.Name -ceq $Name) {
      return $element
    }
  }
  return $null
}

function Coding-SectionAnchorsVisible([object]$Element) {
  $checkbox = Find-DescendantPresent $Element ([System.Windows.Automation.ControlType]::CheckBox) '启用账户模型与受控 Docker（仅创建配置，不批准或执行）'
  $refresh = Find-DescendantPresent $Element ([System.Windows.Automation.ControlType]::Button) '刷新编码能力'
  return (
    $null -ne $checkbox -and
    $null -ne $refresh -and
    -not $checkbox.Current.IsOffscreen -and
    -not $refresh.Current.IsOffscreen
  )
}

function Has-DescendantText([object]$Root, [string]$Fragment) {
  $items = $Root.FindAll(
    [System.Windows.Automation.TreeScope]::Descendants,
    [System.Windows.Automation.Condition]::TrueCondition
  )
  if ($items.Count -gt 256) { throw 'Dialog UI tree exceeds bound' }
  foreach ($element in $items) {
    $current = $element.Current
    if ($current.ControlType -eq [System.Windows.Automation.ControlType]::Text -and $current.Name.Contains($Fragment)) {
      return $true
    }
  }
  return $false
}

function Invoke-Exact([object]$Element) {
  if ($null -eq $Element) { throw 'Required UI action unavailable' }
  $pattern = $null
  if (-not $Element.TryGetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern, [ref]$pattern)) {
    throw 'Required UI action is not invokable'
  }
  ([System.Windows.Automation.InvokePattern]$pattern).Invoke()
}

function Expand-Exact([object]$Element) {
  if ($null -eq $Element) { throw 'Required menu unavailable' }
  $pattern = $null
  if ($Element.TryGetCurrentPattern([System.Windows.Automation.ExpandCollapsePattern]::Pattern, [ref]$pattern)) {
    ([System.Windows.Automation.ExpandCollapsePattern]$pattern).Expand()
    return
  }
  Invoke-Exact $Element
}

function Scroll-IntoViewExact([object]$Element) {
  if ($null -eq $Element) { throw 'Required coding section unavailable' }
  if (Coding-SectionAnchorsVisible $Element) { return }
  $itemPattern = $null
  if ($Element.TryGetCurrentPattern([System.Windows.Automation.ScrollItemPattern]::Pattern, [ref]$itemPattern)) {
    ([System.Windows.Automation.ScrollItemPattern]$itemPattern).ScrollIntoView()
    Start-Sleep -Milliseconds 100
  }
  if (Coding-SectionAnchorsVisible $Element) { return }
  $walker = [System.Windows.Automation.TreeWalker]::ControlViewWalker
  $ancestor = $walker.GetParent($Element)
  $scrollPattern = $null
  while ($null -ne $ancestor -and $null -eq $scrollPattern) {
    $candidate = $null
    if ($ancestor.TryGetCurrentPattern([System.Windows.Automation.ScrollPattern]::Pattern, [ref]$candidate)) {
      $typed = [System.Windows.Automation.ScrollPattern]$candidate
      if ($typed.Current.VerticallyScrollable) { $scrollPattern = $typed }
    }
    $ancestor = $walker.GetParent($ancestor)
  }
  if ($null -eq $scrollPattern) { throw 'Required coding section is not scrollable' }
  for ($i = 0; $i -lt 16 -and -not (Coding-SectionAnchorsVisible $Element); $i++) {
    $scrollPattern.Scroll(
      [System.Windows.Automation.ScrollAmount]::NoAmount,
      [System.Windows.Automation.ScrollAmount]::SmallIncrement
    )
    Start-Sleep -Milliseconds 100
  }
  if (-not (Coding-SectionAnchorsVisible $Element)) { throw 'Required coding controls remained offscreen' }
}

function Wait-OwnedExact([object]$Type, [string]$Name, [int]$Seconds) {
  $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
  while ([DateTime]::UtcNow -lt $deadline) {
    $element = Find-OwnedExact $Type $Name
    if ($null -ne $element) { return $element }
    Start-Sleep -Milliseconds 100
  }
  throw 'Required owned UI element unavailable'
}

function Find-FoundryMenuButton([object]$Root) {
  $items = $Root.FindAll(
    [System.Windows.Automation.TreeScope]::Descendants,
    [System.Windows.Automation.Condition]::TrueCondition
  )
  if ($items.Count -gt 2048) { throw 'Window UI tree exceeds bound' }
  $match = $null
  foreach ($menuBar in $items) {
    $bar = $menuBar.Current
    if (
      $bar.ProcessId -ne $OwnerProcess -or
      $bar.ControlType -ne [System.Windows.Automation.ControlType]::MenuBar -or
      -not $bar.IsEnabled -or
      $bar.IsOffscreen
    ) { continue }
    $children = $menuBar.FindAll(
      [System.Windows.Automation.TreeScope]::Descendants,
      [System.Windows.Automation.Condition]::TrueCondition
    )
    if ($children.Count -gt 256) { throw 'Menu bar UI tree exceeds bound' }
    foreach ($button in $children) {
      $current = $button.Current
      if (
        $current.ProcessId -eq $OwnerProcess -and
        $current.ControlType -eq [System.Windows.Automation.ControlType]::Button -and
        $current.Name -ceq 'Foundry' -and
        $current.IsEnabled -and
        -not $current.IsOffscreen
      ) {
        if ($null -ne $match) { throw 'Foundry menu button is ambiguous' }
        $match = $button
      }
    }
  }
  return $match
}

function Wait-FoundryMenuButton([object]$Root, [int]$Seconds) {
  $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
  while ([DateTime]::UtcNow -lt $deadline) {
    $button = Find-FoundryMenuButton $Root
    if ($null -ne $button) { return $button }
    Start-Sleep -Milliseconds 100
  }
  Write-OwnedMenuDiagnostic
  throw 'Required Foundry popup button unavailable'
}

function Write-OwnedMenuDiagnostic {
  $candidates = New-Object 'System.Collections.Generic.List[string]'
  $total = 0
  $menuItems = 0
  $menuBars = 0
  $menus = 0
  $menuBarButtons = 0
  $fixedFoundryName = 0
  $enabledFoundryButton = 0
  $disabledFoundryButton = 0
  $walker = [System.Windows.Automation.TreeWalker]::ControlViewWalker
  foreach ($element in (Owned-Elements)) {
    try {
      $current = $element.Current
      $isMenuBarButton = $false
      if ($current.ControlType -eq [System.Windows.Automation.ControlType]::Button) {
        $ancestor = $walker.GetParent($element)
        for ($depth = 0; $null -ne $ancestor -and $depth -lt 8; $depth++) {
          if ($ancestor.Current.ControlType -eq [System.Windows.Automation.ControlType]::MenuBar) {
            $isMenuBarButton = $true
            break
          }
          $ancestor = $walker.GetParent($ancestor)
        }
      }
      if (-not $isMenuBarButton -and
        $current.ControlType -ne [System.Windows.Automation.ControlType]::MenuItem -and
        $current.ControlType -ne [System.Windows.Automation.ControlType]::MenuBar -and
        $current.ControlType -ne [System.Windows.Automation.ControlType]::Menu
      ) { continue }
      $total++
      if ($current.ControlType -eq [System.Windows.Automation.ControlType]::MenuItem) { $menuItems++ }
      if ($current.ControlType -eq [System.Windows.Automation.ControlType]::MenuBar) { $menuBars++ }
      if ($current.ControlType -eq [System.Windows.Automation.ControlType]::Menu) { $menus++ }
      if ($isMenuBarButton) { $menuBarButtons++ }
      $rawName = [string]$current.Name
      if ($rawName -ceq 'Foundry') {
        $fixedFoundryName++
        if ($isMenuBarButton) {
          if ($current.IsEnabled) { $enabledFoundryButton++ } else { $disabledFoundryButton++ }
        }
      }
      if ($candidates.Count -lt 32) {
        $name = $rawName
        if ($name.Length -gt 80) { $name = $name.Substring(0, 80) }
        $name = [Regex]::Replace($name, '[^\p{L}\p{Nd} ._():/+&…—-]', '?')
        $state = if ($current.IsEnabled) { 'enabled' } else { 'disabled' }
        $candidates.Add($current.ControlType.ProgrammaticName + ':' + $state + ':' + $name) | Out-Null
      }
    } catch [System.Windows.Automation.ElementNotAvailableException] {
      continue
    }
  }
  $summary = @{
    total=$total
    menuItems=$menuItems
    menuBars=$menuBars
    menus=$menus
    menuBarButtons=$menuBarButtons
    fixedFoundryName=$fixedFoundryName
    enabledFoundryButton=$enabledFoundryButton
    disabledFoundryButton=$disabledFoundryButton
    emitted=$candidates.Count
    truncated=($total -gt $candidates.Count)
    candidates=$candidates.ToArray()
  } | ConvertTo-Json -Depth 3 -Compress
  [Console]::Error.WriteLine('Owned menu candidates: ' + $summary)
}

function Wait-Destination([string]$Title, [int]$Seconds) {
  $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
  while ([DateTime]::UtcNow -lt $deadline) {
    $process = Get-Process -Id $OwnerProcess -ErrorAction Stop
    $process.Refresh()
    if ($process.HasExited) { throw 'Native application exited during mode switch' }
    if ($process.MainWindowHandle -ne [IntPtr]::Zero -and $process.MainWindowTitle -ceq $Title -and $process.Responding) {
      return $process.MainWindowHandle
    }
    Start-Sleep -Milliseconds 100
  }
  throw 'Native mode destination unavailable'
}

$root = [System.Windows.Automation.AutomationElement]::FromHandle([IntPtr]$WindowHandle)
if ($null -eq $root -or $root.Current.ProcessId -ne $OwnerProcess) { throw 'Window ownership mismatch' }
$expectedSource = if ($Action -eq 'coding') { 'Foundry — Persistent Workbench' } else { 'Foundry — Controlled Coding Workbench' }
if ($root.Current.Name -cne $expectedSource) { throw 'Unexpected native mode source' }
$foundry = Wait-FoundryMenuButton $root 5
Expand-Exact $foundry
$targetName = if ($Action -eq 'coding') { 'Controlled Coding Workbench' } else { 'Persistent Workbench' }
$target = Wait-OwnedExact ([System.Windows.Automation.ControlType]::MenuItem) $targetName 5
Invoke-Exact $target

$confirmation = $false
$fixedExplanation = $false
$createForm = $false
$codingSectionVisible = $false
if ($Action -eq 'coding') {
  $dialog = Wait-OwnedExact ([System.Windows.Automation.ControlType]::Window) 'Open Controlled Coding Workbench?' 5
  $button = Find-DescendantExact $dialog ([System.Windows.Automation.ControlType]::Button) 'Open Coding Workbench'
  $fixedExplanation =
    (Has-DescendantText $dialog 'Open live coding configuration?') -and
    (Has-DescendantText $dialog 'This enables configuration only.') -and
    (Has-DescendantText $dialog 'no model calls') -and
    (Has-DescendantText $dialog 'no Docker commands') -and
    (Has-DescendantText $dialog 'separate approvals')
  if (-not $fixedExplanation) { throw 'Native coding explanation mismatch' }
  Invoke-Exact $button
  $confirmation = $true
  $destinationHandle = Wait-Destination 'Foundry — Controlled Coding Workbench' 30
  $destinationRoot = [System.Windows.Automation.AutomationElement]::FromHandle([IntPtr]$destinationHandle)
  $create = $null
  $deadline = [DateTime]::UtcNow.AddSeconds(30)
  while ([DateTime]::UtcNow -lt $deadline -and $null -eq $create) {
    $create = Find-DescendantExact $destinationRoot ([System.Windows.Automation.ControlType]::Button) '创建交付任务'
    if ($null -eq $create) { Start-Sleep -Milliseconds 100 }
  }
  Invoke-Exact $create
  $createForm = $true
  $codingSection = $null
  $deadline = [DateTime]::UtcNow.AddSeconds(30)
  while ([DateTime]::UtcNow -lt $deadline -and $null -eq $codingSection) {
    $codingSection = Find-DescendantPresent $destinationRoot ([System.Windows.Automation.ControlType]::Group) '账户模型编码设置'
    if ($null -eq $codingSection) { Start-Sleep -Milliseconds 100 }
  }
  Scroll-IntoViewExact $codingSection
  $codingSectionVisible = $true
} else {
  $destinationHandle = Wait-Destination 'Foundry — Persistent Workbench' 30
}

@{
  action=$Action
  processId=$OwnerProcess
  window=$destinationHandle.ToInt64().ToString()
  menu=$true
  confirmation=$confirmation
  fixedExplanation=$fixedExplanation
  destination=$true
  createForm=$createForm
  codingSection=$codingSectionVisible
} | ConvertTo-Json -Compress
