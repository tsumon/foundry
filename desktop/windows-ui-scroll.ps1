# Acceptance driver only. Scrolls fixed configuration controls, never invokes
# them. The final current-viewport semantics/paint gate remains independent.
function Test-CodingScrollReady([object]$Facts) {
  if ($null -eq $Facts) { return $false }
  foreach ($name in @('upper', 'checkbox', 'refresh')) {
    $anchor = $Facts.$name
    if ($anchor.matches -ne 1 -or -not $anchor.owned) { throw 'coding-scroll: invalid anchor identity/state' }
  }
  if (-not $Facts.checkbox.unchecked) { throw 'coding-scroll: opt-in state changed' }
  return ($Facts.upper.enabled -and $Facts.checkbox.enabled -and $Facts.refresh.enabled)
}

function Get-CodingScrollDecision([object]$Facts, [int]$Attempt, [object]$Previous, [hashtable]$ItemAttempts) {
  if (-not (Test-CodingScrollReady $Facts)) { throw 'coding-scroll: invalid anchor identity/state' }
  if ($Attempt -lt 0 -or $Attempt -gt 16) { throw 'coding-scroll: decision bound exceeded' }
  if ($Facts.upper.visible -and $Facts.checkbox.visible -and $Facts.refresh.visible) { return @{ kind='done'; target='none'; direction='none' } }
  if ($Attempt -eq 16) { throw 'coding-scroll: decision bound exceeded' }
  if ($null -ne $Previous -and $Facts.fingerprint -ceq $Previous.fingerprint) { throw 'coding-scroll: no progress' }
  # Refresh is the last control. Prefer it without assuming ScrollIntoView
  # aligns either edge; the next capture proves upper metadata and both controls.
  $target = if (-not $Facts.refresh.visible) { 'refresh' } elseif (-not $Facts.upper.visible) { 'upper' } else { 'checkbox' }
  $anchor = $Facts.$target
  if ($anchor.itemAvailable -and -not $ItemAttempts.ContainsKey($target)) {
    return @{ kind='item'; target=$target; direction='target' }
  }
  $scroll = $Facts.scroll
  if (-not $scroll.available -or -not $scroll.owned) { throw 'coding-scroll: owned scroll ancestor unavailable' }
  foreach ($value in @($anchor.top, $anchor.bottom, $scroll.top, $scroll.bottom, $scroll.percent, $scroll.viewSize)) {
    if ([double]::IsNaN($value) -or [double]::IsInfinity($value)) { throw 'coding-scroll: nonfinite geometry' }
  }
  if ($anchor.bottom -le $anchor.top -or $scroll.bottom -le $scroll.top -or $scroll.percent -lt 0 -or $scroll.percent -gt 100 -or $scroll.viewSize -le 0 -or $scroll.viewSize -ge 100) { throw 'coding-scroll: invalid geometry' }
  $delta = if ($anchor.bottom -gt $scroll.bottom) { $anchor.bottom - $scroll.bottom } elseif ($anchor.top -lt $scroll.top) { $anchor.top - $scroll.top } else { 0 }
  if ($delta -eq 0) { throw 'coding-scroll: offscreen without vertical displacement' }
  $range = ($scroll.bottom - $scroll.top) * (100.0 / $scroll.viewSize - 1.0)
  $percent = [Math]::Max(0.0, [Math]::Min(100.0, $scroll.percent + $delta / $range * 100.0))
  if ([Math]::Abs($percent - $scroll.percent) -lt 0.000001) { throw 'coding-scroll: scroll boundary reached' }
  return @{ kind='percent'; target=$target; direction=$(if ($percent -gt $scroll.percent) { 'down' } else { 'up' }); percent=$percent }
}

function Get-CodingScrollCapture([object]$Section, [int]$OwnerProcess) {
  if ($null -eq $Section -or $Section.Current.ProcessId -ne $OwnerProcess -or $Section.Current.Name -cne '账户模型编码设置') { throw 'coding-scroll: section ownership changed' }
  $items = $Section.FindAll([System.Windows.Automation.TreeScope]::Descendants, [System.Windows.Automation.Condition]::TrueCondition)
  if ($items.Count -gt 2048) { throw 'coding-scroll: section tree exceeds bound' }
  $facts = @{}
  $elements = @{}
  $patterns = @{}
  $pending = $false
  foreach ($name in @('upper', 'checkbox', 'refresh')) {
    $type = if ($name -eq 'upper') { [System.Windows.Automation.ControlType]::Text } elseif ($name -eq 'checkbox') { [System.Windows.Automation.ControlType]::CheckBox } else { [System.Windows.Automation.ControlType]::Button }
    $label = if ($name -eq 'upper') { '运行时 / 提供商' } elseif ($name -eq 'checkbox') { '启用账户模型与内置 VM（仅创建配置，不批准或执行）' } else { '刷新编码能力' }
    $matches = @($items | Where-Object { $_.Current.ControlType -eq $type -and $_.Current.Name -ceq $label })
    if ($matches.Count -gt 1) { throw 'coding-scroll: required anchor ambiguous' }
    if ($matches.Count -eq 0) { $pending=$true; continue }
    $element = $matches[0]
    $current = $element.Current
    if ($current.ProcessId -ne $OwnerProcess) { throw 'coding-scroll: anchor ownership changed' }
    $bounds = $current.BoundingRectangle
    $itemPattern = $null
    $itemAvailable = $element.TryGetCurrentPattern([System.Windows.Automation.ScrollItemPattern]::Pattern, [ref]$itemPattern)
    $record = @{ matches=1; owned=($current.ProcessId -eq $OwnerProcess); enabled=$current.IsEnabled; visible=(-not $current.IsOffscreen -and $bounds.Width -gt 0 -and $bounds.Height -gt 0); top=$bounds.Top; bottom=$bounds.Bottom; itemAvailable=$itemAvailable }
    if ($name -eq 'checkbox') {
      $toggle = $null
      $record.unchecked = ($element.TryGetCurrentPattern([System.Windows.Automation.TogglePattern]::Pattern, [ref]$toggle) -and $toggle.Current.ToggleState -eq [System.Windows.Automation.ToggleState]::Off)
      if (-not $record.unchecked) { throw 'coding-scroll: opt-in state changed' }
    }
    $facts[$name] = $record
    $elements[$name] = $element
    $patterns[$name] = $itemPattern
  }
  if ($pending) { return $null }
  $target = if (-not $facts.refresh.visible) { 'refresh' } elseif (-not $facts.upper.visible) { 'upper' } else { 'checkbox' }
  $walker = [System.Windows.Automation.TreeWalker]::ControlViewWalker
  $ancestor = $walker.GetParent($elements[$target])
  $scrollPattern = $null
  $facts.scroll = @{ available=$false; owned=$false; percent=-1; viewSize=0; top=0; bottom=0 }
  for ($depth = 0; $depth -lt 32 -and $null -ne $ancestor; $depth++) {
    $current = $ancestor.Current
    if ($current.ProcessId -ne $OwnerProcess) { break }
    $candidate = $null
    if ($ancestor.TryGetCurrentPattern([System.Windows.Automation.ScrollPattern]::Pattern, [ref]$candidate) -and $candidate.Current.VerticallyScrollable) {
      $scrollPattern = $candidate
      $bounds = $current.BoundingRectangle
      $facts.scroll = @{ available=$true; owned=$true; percent=$candidate.Current.VerticalScrollPercent; viewSize=$candidate.Current.VerticalViewSize; top=$bounds.Top; bottom=$bounds.Bottom }
      break
    }
    $ancestor = $walker.GetParent($ancestor)
  }
  $facts.fingerprint = (@($facts.upper.visible, $facts.upper.top, $facts.upper.bottom, $facts.checkbox.visible, $facts.checkbox.top, $facts.checkbox.bottom, $facts.refresh.visible, $facts.refresh.top, $facts.refresh.bottom, $facts.scroll.percent) | ConvertTo-Json -Compress)
  return @{ facts=$facts; elements=$elements; items=$patterns; scroll=$scrollPattern }
}

function Invoke-CodingScrollStep([object]$Capture, [object]$Decision) {
  if ($Decision.kind -eq 'item') {
    ([System.Windows.Automation.ScrollItemPattern]$Capture.items[$Decision.target]).ScrollIntoView()
  } elseif ($Decision.kind -eq 'percent') {
    ([System.Windows.Automation.ScrollPattern]$Capture.scroll).SetScrollPercent([System.Windows.Automation.ScrollPattern]::NoScroll, $Decision.percent)
  } else { throw 'coding-scroll: invalid action' }
}

function Scroll-CodingControlsIntoView([object]$Section, [int]$OwnerProcess) {
  $previous = $null
  $attempted = @{}
  $capture = $null
  $decision = $null
  try {
    for ($step = 0; $step -lt 16; $step++) {
      $capture = Get-CodingScrollCapture $Section $OwnerProcess
      if ($null -eq $capture) { throw 'coding-scroll: required anchor disappeared' }
      $decision = Get-CodingScrollDecision $capture.facts $step $previous $attempted
      if ($decision.kind -eq 'done') { return }
      $previous = $capture.facts
      if ($decision.kind -eq 'item') { $attempted[$decision.target] = $true }
      Invoke-CodingScrollStep $capture $decision
      Start-Sleep -Milliseconds 100
    }
    $capture = Get-CodingScrollCapture $Section $OwnerProcess
    if ($null -eq $capture) { throw 'coding-scroll: required anchor disappeared' }
    $decision = Get-CodingScrollDecision $capture.facts 16 $previous $attempted
    if ($decision.kind -ne 'done') { throw 'coding-scroll: decision bound exceeded' }
  } catch {
    # Fixed, bounded geometry/state only; no UI text, paths or account data.
    $diagnostic = @{ decisions=$step; available=($null -ne $capture) }
    if ($null -ne $capture) { $diagnostic.upper=$capture.facts.upper; $diagnostic.checkbox=$capture.facts.checkbox; $diagnostic.refresh=$capture.facts.refresh; $diagnostic.scroll=$capture.facts.scroll }
    if ($null -ne $decision) { $diagnostic.action=$decision.kind; $diagnostic.target=$decision.target; $diagnostic.direction=$decision.direction }
    [Console]::Error.WriteLine('coding-scroll: ' + ($diagnostic | ConvertTo-Json -Compress -Depth 4))
    throw
  }
}
