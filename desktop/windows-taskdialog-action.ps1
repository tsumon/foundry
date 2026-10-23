if ($null -eq ('FoundryTaskDialogNative' -as [type])) {
  Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Text;

public sealed class FoundryTaskDialogFacts {
  public bool DialogOwned { get; set; }
  public bool DialogVisible { get; set; }
  public bool DialogEnabled { get; set; }
  public string DialogClass { get; set; }
  public string DialogTitle { get; set; }
  public bool ActionOwned { get; set; }
  public bool ActionIsChild { get; set; }
  public bool ActionVisible { get; set; }
  public bool ActionEnabled { get; set; }
  public string ActionClass { get; set; }
  public int ActionControlId { get; set; }
  public string ActionTitle { get; set; }
  public int ActionStyleType { get; set; }
}

public static class FoundryTaskDialogNative {
  private const int GWL_STYLE = -16;
  private const long BS_TYPEMASK = 0x0000000F;
  private const int BS_PUSHBUTTON = 0;
  private const int BS_DEFPUSHBUTTON = 1;
  private const uint WM_GETTEXT = 0x000D;
  private const uint TDM_CLICK_BUTTON = 0x0400 + 102;
  private const uint SMTO_BLOCK = 0x0001;
  private const uint SMTO_ABORTIFHUNG = 0x0002;
  private const int LogicalTaskDialogActionId = 100;
  private const string DialogTitle = "Open Controlled Coding Workbench?";
  private const string ActionTitle = "Open Coding Workbench";

  [DllImport("user32.dll", ExactSpelling = true)]
  private static extern int GetDlgCtrlID(IntPtr control);

  [DllImport("user32.dll", ExactSpelling = true)]
  [return: MarshalAs(UnmanagedType.Bool)]
  private static extern bool IsChild(IntPtr parent, IntPtr child);

  [DllImport("user32.dll", ExactSpelling = true)]
  private static extern uint GetWindowThreadProcessId(
      IntPtr window, out uint processId);

  [DllImport("user32.dll", CharSet = CharSet.Unicode, ExactSpelling = true)]
  private static extern int GetClassNameW(
      IntPtr window, StringBuilder className, int maximum);

  [DllImport("user32.dll", CharSet = CharSet.Unicode, ExactSpelling = true)]
  private static extern int GetWindowTextLengthW(IntPtr window);

  [DllImport("user32.dll", CharSet = CharSet.Unicode, ExactSpelling = true)]
  private static extern int GetWindowTextW(
      IntPtr window, StringBuilder text, int maximum);

  [DllImport("user32.dll", ExactSpelling = true)]
  [return: MarshalAs(UnmanagedType.Bool)]
  private static extern bool IsWindowVisible(IntPtr window);

  [DllImport("user32.dll", ExactSpelling = true)]
  [return: MarshalAs(UnmanagedType.Bool)]
  private static extern bool IsWindowEnabled(IntPtr window);

  [DllImport("user32.dll", EntryPoint = "GetWindowLongPtrW", ExactSpelling = true)]
  private static extern IntPtr GetWindowLongPtrW(IntPtr window, int index);

  [DllImport("user32.dll", CharSet = CharSet.Unicode, ExactSpelling = true,
             SetLastError = true)]
  private static extern IntPtr SendMessageTimeoutW(
      IntPtr window, uint message, IntPtr wParam, IntPtr lParam,
      uint flags, uint timeout, out UIntPtr result);

  [DllImport("user32.dll", CharSet = CharSet.Unicode, ExactSpelling = true,
             SetLastError = true)]
  private static extern IntPtr SendMessageTimeoutW(
      IntPtr window, uint message, UIntPtr wParam, StringBuilder lParam,
      uint flags, uint timeout, out UIntPtr result);

  public static IntPtr AutomationHandle(int value) {
    return new IntPtr(unchecked((long)(uint)value));
  }

  public static string ClassName(IntPtr window) {
    if (window == IntPtr.Zero) return String.Empty;
    StringBuilder value = new StringBuilder(128);
    int length = GetClassNameW(window, value, value.Capacity);
    return length > 0 ? value.ToString(0, length) : String.Empty;
  }

  public static int DiagnosticControlId(IntPtr window) {
    return window == IntPtr.Zero ? -1 : GetDlgCtrlID(window);
  }

  public static int StyleType(IntPtr window) {
    if (window == IntPtr.Zero) return -1;
    return (int)(GetWindowLongPtrW(window, GWL_STYLE).ToInt64() & BS_TYPEMASK);
  }

  public static FoundryTaskDialogFacts CaptureFacts(
      IntPtr dialog, IntPtr action, int expectedProcessId) {
    uint dialogProcess;
    uint actionProcess;
    uint dialogThread = GetWindowThreadProcessId(dialog, out dialogProcess);
    uint actionThread = GetWindowThreadProcessId(action, out actionProcess);
    return new FoundryTaskDialogFacts {
      DialogOwned = dialogThread != 0 && dialogProcess == (uint)expectedProcessId,
      DialogVisible = dialog != IntPtr.Zero && IsWindowVisible(dialog),
      DialogEnabled = dialog != IntPtr.Zero && IsWindowEnabled(dialog),
      DialogClass = ClassName(dialog),
      DialogTitle = TopLevelWindowText(dialog),
      ActionOwned = actionThread != 0 && actionProcess == (uint)expectedProcessId,
      ActionIsChild = dialog != IntPtr.Zero && action != IntPtr.Zero &&
                      IsChild(dialog, action),
      ActionVisible = action != IntPtr.Zero && IsWindowVisible(action),
      ActionEnabled = action != IntPtr.Zero && IsWindowEnabled(action),
      ActionClass = ClassName(action),
      ActionControlId = DiagnosticControlId(action),
      ActionTitle = ControlText(action),
      ActionStyleType = StyleType(action)
    };
  }

  public static void ValidateFacts(FoundryTaskDialogFacts facts) {
    if (facts == null) throw new InvalidOperationException("TaskDialog facts unavailable");
    if (!facts.DialogOwned) throw new InvalidOperationException("TaskDialog ownership mismatch");
    if (!facts.DialogVisible || !facts.DialogEnabled)
      throw new InvalidOperationException("TaskDialog state mismatch");
    if (!String.Equals(facts.DialogClass, "#32770", StringComparison.OrdinalIgnoreCase))
      throw new InvalidOperationException("TaskDialog native class mismatch");
    if (!String.Equals(facts.DialogTitle, DialogTitle, StringComparison.Ordinal))
      throw new InvalidOperationException("TaskDialog native title mismatch");
    if (!facts.ActionOwned || !facts.ActionIsChild)
      throw new InvalidOperationException("TaskDialog action ownership mismatch");
    if (!facts.ActionVisible || !facts.ActionEnabled)
      throw new InvalidOperationException("TaskDialog action state mismatch");
    if (!String.Equals(facts.ActionClass, "Button", StringComparison.OrdinalIgnoreCase))
      throw new InvalidOperationException("TaskDialog action native class mismatch");
    if (!String.Equals(facts.ActionTitle, ActionTitle, StringComparison.Ordinal))
      throw new InvalidOperationException("TaskDialog action native title mismatch");
    if (facts.ActionStyleType != BS_PUSHBUTTON &&
        facts.ActionStyleType != BS_DEFPUSHBUTTON)
      throw new InvalidOperationException("TaskDialog action native style mismatch");
  }

  public static void ValidateAndClick(
      IntPtr dialog, IntPtr action, int expectedProcessId) {
    ValidateFacts(CaptureFacts(dialog, action, expectedProcessId));
    UIntPtr ignored;
    IntPtr sent = SendMessageTimeoutW(
        dialog, TDM_CLICK_BUTTON,
        new IntPtr(LogicalTaskDialogActionId), IntPtr.Zero,
        SMTO_BLOCK | SMTO_ABORTIFHUNG, 2000, out ignored);
    if (sent == IntPtr.Zero)
      throw new InvalidOperationException("TaskDialog action dispatch failed");
  }

  private static string TopLevelWindowText(IntPtr window) {
    if (window == IntPtr.Zero) return String.Empty;
    int length = GetWindowTextLengthW(window);
    if (length < 0 || length > 256) return String.Empty;
    StringBuilder value = new StringBuilder(length + 1);
    int copied = GetWindowTextW(window, value, value.Capacity);
    return copied > 0 ? value.ToString(0, copied) : String.Empty;
  }

  private static string ControlText(IntPtr window) {
    if (window == IntPtr.Zero) return String.Empty;
    const int capacity = 257;
    StringBuilder value = new StringBuilder(capacity);
    UIntPtr copied;
    IntPtr sent = SendMessageTimeoutW(
        window, WM_GETTEXT, new UIntPtr((uint)capacity), value,
        SMTO_BLOCK | SMTO_ABORTIFHUNG, 1000, out copied);
    ulong length = copied.ToUInt64();
    if (sent == IntPtr.Zero || length > 256) return String.Empty;
    return value.ToString(0, (int)length);
  }
}
'@
}

function Get-AutomationNativeHandle([object]$Element) {
  return [FoundryTaskDialogNative]::AutomationHandle($Element.Current.NativeWindowHandle)
}

function Find-CodingTaskDialogAction([object]$Dialog, [int]$OwnerProcess) {
  $dialogCurrent = $Dialog.Current
  if (
    $dialogCurrent.ProcessId -ne $OwnerProcess -or
    $dialogCurrent.ControlType -ne [System.Windows.Automation.ControlType]::Window -or
    $dialogCurrent.Name -cne 'Open Controlled Coding Workbench?' -or
    -not $dialogCurrent.IsEnabled -or
    $dialogCurrent.IsOffscreen -or
    $dialogCurrent.NativeWindowHandle -eq 0
  ) { throw 'coding-confirmation-button: owned dialog identity mismatch' }
  $dialogHandle = Get-AutomationNativeHandle $Dialog
  $freshDialog = [System.Windows.Automation.AutomationElement]::FromHandle($dialogHandle)
  if ($null -eq $freshDialog -or -not [System.Windows.Automation.Automation]::Compare($Dialog, $freshDialog)) {
    throw 'coding-confirmation-button: owned dialog native identity mismatch'
  }
  $items = $Dialog.FindAll(
    [System.Windows.Automation.TreeScope]::Descendants,
    [System.Windows.Automation.Condition]::TrueCondition
  )
  if ($items.Count -gt 256) { throw 'Dialog UI tree exceeds bound' }
  $match = $null
  $exactCount = 0
  foreach ($element in $items) {
    $current = $element.Current
    $allowedType =
      $current.ControlType -eq [System.Windows.Automation.ControlType]::Button -or
      $current.ControlType -eq [System.Windows.Automation.ControlType]::Pane
    if (
      $current.ProcessId -eq $OwnerProcess -and
      $allowedType -and
      $current.Name -ceq 'Open Coding Workbench' -and
      $current.IsEnabled -and
      -not $current.IsOffscreen -and
      $current.NativeWindowHandle -ne 0
    ) {
      $exactCount++
      if ($exactCount -gt 1) { throw 'coding-confirmation-button: native action is ambiguous' }
      $elementHandle = Get-AutomationNativeHandle $element
      $nativeElement = [System.Windows.Automation.AutomationElement]::FromHandle($elementHandle)
      if (
        $null -eq $nativeElement -or
        -not [System.Windows.Automation.Automation]::Compare($element, $nativeElement)
      ) {
        throw 'coding-confirmation-button: native action identity mismatch'
      }
      $match = $element
    }
  }
  return $match
}

function Write-CodingTaskDialogDiagnostic([object]$Dialog, [int]$OwnerProcess) {
  $candidates = New-Object 'System.Collections.Generic.List[object]'
  $items = $Dialog.FindAll(
    [System.Windows.Automation.TreeScope]::Descendants,
    [System.Windows.Automation.Condition]::TrueCondition
  )
  if ($items.Count -gt 256) {
    [Console]::Error.WriteLine('Owned dialog candidates: {"phase":"coding-confirmation-button","overBound":true,"emitted":0}')
    return
  }
  foreach ($element in $items) {
    if ($candidates.Count -ge 32) { break }
    try {
      $current = $element.Current
      if ($current.ProcessId -ne $OwnerProcess) { continue }
      $name = [string]$current.Name
      if ($name.Length -gt 80) { $name = $name.Substring(0, 80) }
      $name = [Regex]::Replace($name, '[^\p{L}\p{Nd} ._():/+&-]', '?')
      $nativeClass = ''
      $nativeControlId = -1
      $nativeStyle = -1
      if ($current.NativeWindowHandle -ne 0) {
        $handle = Get-AutomationNativeHandle $element
        $nativeClass = [FoundryTaskDialogNative]::ClassName($handle)
        $nativeControlId = [FoundryTaskDialogNative]::DiagnosticControlId($handle)
        $nativeStyle = [FoundryTaskDialogNative]::StyleType($handle)
      }
      $invoke = $null
      $hasInvoke = $element.TryGetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern, [ref]$invoke)
      $candidates.Add([ordered]@{
        type=[string]$current.ControlType.ProgrammaticName
        enabled=[bool]$current.IsEnabled
        offscreen=[bool]$current.IsOffscreen
        name=$name
        nativeHandle=($current.NativeWindowHandle -ne 0)
        nativeClass=$nativeClass
        nativeControlId=$nativeControlId
        nativeStyleType=$nativeStyle
        invoke=[bool]$hasInvoke
      }) | Out-Null
    } catch [System.Windows.Automation.ElementNotAvailableException] {
      continue
    } catch [System.InvalidOperationException] {
      continue
    }
  }
  $dialogClass = ''
  try {
    if ($Dialog.Current.NativeWindowHandle -ne 0) {
      $dialogClass = [FoundryTaskDialogNative]::ClassName((Get-AutomationNativeHandle $Dialog))
    }
  } catch [System.Windows.Automation.ElementNotAvailableException] {
  } catch [System.InvalidOperationException] {}
  $summary = [ordered]@{
    phase='coding-confirmation-button'
    total=$items.Count
    overBound=$false
    dialogClass=$dialogClass
    emitted=$candidates.Count
    truncated=($items.Count -gt $candidates.Count)
    candidates=$candidates.ToArray()
  } | ConvertTo-Json -Depth 4 -Compress
  [Console]::Error.WriteLine('Owned dialog candidates: ' + $summary)
}

function Wait-CodingTaskDialogAction(
  [object]$Dialog,
  [int]$OwnerProcess,
  [ValidateRange(1,5)][int]$Seconds
) {
  $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
  while ([DateTime]::UtcNow -lt $deadline) {
    $action = Find-CodingTaskDialogAction $Dialog $OwnerProcess
    if ($null -ne $action) { return $action }
    Start-Sleep -Milliseconds 100
  }
  try { Write-CodingTaskDialogDiagnostic $Dialog $OwnerProcess } catch {
    [Console]::Error.WriteLine('coding-confirmation-button: owned dialog diagnostic unavailable')
  }
  throw 'coding-confirmation-button: required native TaskDialog action unavailable'
}

function Invoke-CodingTaskDialogAction(
  [object]$Dialog,
  [object]$Action,
  [int]$OwnerProcess
) {
  if ($null -eq $Dialog -or $null -eq $Action) {
    throw 'coding-confirmation-button: required native TaskDialog action unavailable'
  }
  $fresh = Find-CodingTaskDialogAction $Dialog $OwnerProcess
  if ($null -eq $fresh -or -not [System.Windows.Automation.Automation]::Compare($Action, $fresh)) {
    throw 'coding-confirmation-button: native action identity changed'
  }
  try {
    [FoundryTaskDialogNative]::ValidateAndClick(
      (Get-AutomationNativeHandle $Dialog),
      (Get-AutomationNativeHandle $fresh),
      $OwnerProcess
    )
  } catch {
    $primaryFailure = $_
    try { Write-CodingTaskDialogDiagnostic $Dialog $OwnerProcess } catch {
      [Console]::Error.WriteLine('coding-confirmation-button: owned dialog diagnostic unavailable')
    }
    throw $primaryFailure
  }
}
