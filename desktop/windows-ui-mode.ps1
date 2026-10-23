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
Add-Type -AssemblyName Accessibility
. (Join-Path $PSScriptRoot 'windows-taskdialog-action.ps1')
$accessibilityAssembly = [Accessibility.IAccessible].Assembly.Location
Add-Type -ReferencedAssemblies @($accessibilityAssembly) -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using Accessibility;

public static class FoundryMsaaMenu {
  public const int MaxNodes = 2048;
  public const int MaxDepth = 16;
  private const uint OBJID_CLIENT = unchecked((uint)-4);
  private const int CHILDID_SELF = 0;
  private const int ROLE_SYSTEM_MENUBAR = 0x02;
  private const int ROLE_SYSTEM_DOCUMENT = 0x0F;
  private const int ROLE_SYSTEM_BUTTONMENU = 0x39;
  private const int STATE_SYSTEM_UNAVAILABLE = 0x00000001;
  private const int STATE_SYSTEM_INVISIBLE = 0x00008000;
  private const int STATE_SYSTEM_OFFSCREEN = 0x00010000;
  private static readonly Guid IID_IAccessible =
      new Guid("618736E0-3C3D-11CF-810C-00AA00389B71");

  [DllImport("oleacc.dll", ExactSpelling = true)]
  private static extern int AccessibleObjectFromWindow(
      IntPtr window, uint objectId, ref Guid interfaceId,
      [MarshalAs(UnmanagedType.Interface)] out IAccessible accessible);

  [DllImport("oleacc.dll", ExactSpelling = true)]
  private static extern int AccessibleChildren(
      IAccessible container, int childStart, int childCount,
      [Out, MarshalAs(UnmanagedType.LPArray, SizeParamIndex = 2)] object[] children,
      out int obtained);

  [DllImport("user32.dll", ExactSpelling = true)]
  private static extern uint GetWindowThreadProcessId(
      IntPtr window, out uint processId);

  private sealed class Candidate {
    internal readonly IAccessible Owner;
    internal readonly object ChildId;

    internal Candidate(IAccessible owner, object childId) {
      Owner = owner;
      ChildId = childId;
    }
  }

  private sealed class Search {
    internal readonly double Left;
    internal readonly double Top;
    internal readonly double Width;
    internal readonly double Height;
    internal readonly List<Candidate> Matches = new List<Candidate>();
    internal int Nodes;

    internal Search(double left, double top, double width, double height) {
      Left = left;
      Top = top;
      Width = width;
      Height = height;
    }
  }

  public static void Invoke(
      IntPtr window, int expectedProcessId,
      double left, double top, double width, double height) {
    uint actualProcessId;
    if (GetWindowThreadProcessId(window, out actualProcessId) == 0 ||
        actualProcessId != (uint)expectedProcessId) {
      throw new InvalidOperationException("MSAA window ownership mismatch");
    }
    if (!Finite(left) || !Finite(top) || !Finite(width) || !Finite(height) ||
        width <= 0 || height <= 0) {
      throw new InvalidOperationException("MSAA target bounds invalid");
    }

    IAccessible root;
    Guid interfaceId = IID_IAccessible;
    int result = AccessibleObjectFromWindow(
        window, OBJID_CLIENT, ref interfaceId, out root);
    if (result < 0 || root == null) {
      throw new InvalidOperationException("MSAA client root unavailable");
    }

    Search search = new Search(left, top, width, height);
    Walk(root, 0, -1, search);
    if (search.Matches.Count != 1) {
      throw new InvalidOperationException("MSAA Foundry menu target unavailable or ambiguous");
    }
    try {
      search.Matches[0].Owner.accDoDefaultAction(search.Matches[0].ChildId);
    } catch {
      throw new InvalidOperationException("MSAA Foundry menu action failed");
    }
  }

  private static void Walk(
      IAccessible accessible, int depth, int parentRole, Search search) {
    int role = Inspect(accessible, CHILDID_SELF, parentRole, search);
    // The application menu is a native Views sibling of the renderer document.
    // Do not traverse arbitrary web content while resolving that native menu.
    if (role == ROLE_SYSTEM_DOCUMENT) return;
    int childCount;
    try {
      childCount = accessible.accChildCount;
    } catch {
      return;
    }
    if (childCount < 0 || childCount > MaxNodes) {
      throw new InvalidOperationException("MSAA menu tree exceeds bound");
    }
    if (childCount == 0) return;
    if (depth >= MaxDepth) {
      throw new InvalidOperationException("MSAA menu tree exceeds depth bound");
    }

    object[] children = new object[childCount];
    int obtained;
    int result = AccessibleChildren(
        accessible, 0, childCount, children, out obtained);
    if (result < 0 || obtained < 0 || obtained > childCount) {
      throw new InvalidOperationException("MSAA menu children unavailable");
    }
    for (int index = 0; index < obtained; index++) {
      IAccessible child = children[index] as IAccessible;
      if (child != null) {
        Walk(child, depth + 1, role, search);
      } else if (children[index] is int) {
        Inspect(accessible, children[index], role, search);
      }
    }
  }

  private static int Inspect(
      IAccessible owner, object childId, int parentRole, Search search) {
    search.Nodes++;
    if (search.Nodes > MaxNodes) {
      throw new InvalidOperationException("MSAA menu tree exceeds bound");
    }
    try {
      int role = Convert.ToInt32(owner.get_accRole(childId));
      if (parentRole != ROLE_SYSTEM_MENUBAR ||
          role != ROLE_SYSTEM_BUTTONMENU ||
          !String.Equals(owner.get_accName(childId), "Foundry",
                         StringComparison.Ordinal)) {
        return role;
      }
      int state = Convert.ToInt32(owner.get_accState(childId));
      int blocked = STATE_SYSTEM_UNAVAILABLE |
                    STATE_SYSTEM_INVISIBLE |
                    STATE_SYSTEM_OFFSCREEN;
      if ((state & blocked) != 0) return role;
      string defaultAction = owner.get_accDefaultAction(childId);
      if (String.IsNullOrWhiteSpace(defaultAction)) return role;
      int actualLeft, actualTop, actualWidth, actualHeight;
      owner.accLocation(out actualLeft, out actualTop,
                        out actualWidth, out actualHeight, childId);
      if (Same(actualLeft, search.Left) &&
          Same(actualTop, search.Top) &&
          Same(actualWidth, search.Width) &&
          Same(actualHeight, search.Height)) {
        search.Matches.Add(new Candidate(owner, childId));
      }
      return role;
    } catch {
      return -1;
    }
  }

  private static bool Same(int actual, double expected) {
    return Math.Abs(actual - expected) < 0.5;
  }

  private static bool Finite(double value) {
    return !Double.IsNaN(value) && !Double.IsInfinity(value);
  }
}
'@

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
    if (
      $current.ProcessId -eq $OwnerProcess -and
      $current.ControlType -eq $Type -and
      $current.Name -ceq $Name -and
      $current.IsEnabled
    ) {
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

function Invoke-Exact(
  [object]$Element,
  [ValidateSet('coding-mode-menu-item','persistent-mode-menu-item','create-mission-button')]
  [string]$Phase
) {
  if ($null -eq $Element) { throw "${Phase}: required UI action unavailable" }
  try { $current = $Element.Current } catch {
    throw "${Phase}: required UI action state unavailable"
  }
  if ($current.ProcessId -ne $OwnerProcess -or -not $current.IsEnabled) {
    throw "${Phase}: required UI action state changed"
  }
  $pattern = $null
  try {
    $invokable = $Element.TryGetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern, [ref]$pattern)
  } catch {
    throw "${Phase}: required UI action pattern unavailable"
  }
  if (-not $invokable) {
    throw "${Phase}: required UI action is not invokable"
  }
  try { ([System.Windows.Automation.InvokePattern]$pattern).Invoke() } catch {
    throw "${Phase}: required UI action failed"
  }
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
      [System.Windows.Automation.TreeScope]::Children,
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

function Invoke-FoundryMenuExact([object]$Root, [object]$Element) {
  $fresh = Find-FoundryMenuButton $Root
  if ($null -eq $fresh -or -not [System.Windows.Automation.Automation]::Compare($Element, $fresh)) {
    throw 'Foundry menu identity changed before action'
  }
  $current = $fresh.Current
  if (
    $current.ProcessId -ne $OwnerProcess -or
    $current.ControlType -ne [System.Windows.Automation.ControlType]::Button -or
    $current.Name -cne 'Foundry' -or
    -not $current.IsEnabled -or
    $current.IsOffscreen
  ) { throw 'Foundry menu state changed before action' }
  $bounds = $current.BoundingRectangle
  [FoundryMsaaMenu]::Invoke(
    [IntPtr]$WindowHandle,
    $OwnerProcess,
    $bounds.Left,
    $bounds.Top,
    $bounds.Width,
    $bounds.Height
  )
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
Invoke-FoundryMenuExact $root $foundry
$targetName = if ($Action -eq 'coding') { 'Controlled Coding Workbench' } else { 'Persistent Workbench' }
$target = Wait-OwnedExact ([System.Windows.Automation.ControlType]::MenuItem) $targetName 5
$targetPhase = if ($Action -eq 'coding') { 'coding-mode-menu-item' } else { 'persistent-mode-menu-item' }
Invoke-Exact $target $targetPhase

$confirmation = $false
$fixedExplanation = $false
$createForm = $false
$codingSectionVisible = $false
if ($Action -eq 'coding') {
  $dialog = Wait-OwnedExact ([System.Windows.Automation.ControlType]::Window) 'Open Controlled Coding Workbench?' 5
  $button = Wait-CodingTaskDialogAction $dialog $OwnerProcess 5
  $fixedExplanation =
    (Has-DescendantText $dialog 'Open live coding configuration?') -and
    (Has-DescendantText $dialog 'This enables configuration only.') -and
    (Has-DescendantText $dialog 'no model calls') -and
    (Has-DescendantText $dialog 'no Docker commands') -and
    (Has-DescendantText $dialog 'separate approvals')
  if (-not $fixedExplanation) { throw 'Native coding explanation mismatch' }
  Invoke-CodingTaskDialogAction $dialog $button $OwnerProcess
  $confirmation = $true
  $destinationHandle = Wait-Destination 'Foundry — Controlled Coding Workbench' 30
  $destinationRoot = [System.Windows.Automation.AutomationElement]::FromHandle([IntPtr]$destinationHandle)
  $create = $null
  $deadline = [DateTime]::UtcNow.AddSeconds(30)
  while ([DateTime]::UtcNow -lt $deadline -and $null -eq $create) {
    $create = Find-DescendantExact $destinationRoot ([System.Windows.Automation.ControlType]::Button) '创建交付任务'
    if ($null -eq $create) { Start-Sleep -Milliseconds 100 }
  }
  Invoke-Exact $create 'create-mission-button'
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
