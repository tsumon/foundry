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

function Get-CodingScrollGeometry([object]$Facts) {
  $safe=@{}
  foreach ($name in @('upper','checkbox','refresh')) {
    $a=$Facts.$name
    $safe[$name]=@{ matches=[int]$a.matches; owned=[bool]$a.owned; enabled=[bool]$a.enabled; visible=[bool]$a.visible; top=[double]$a.top; bottom=[double]$a.bottom }
  }
  $safe.checkbox.unchecked=[bool]$Facts.checkbox.unchecked
  $s=$Facts.scroll
  $safe.scroll=@{ available=[bool]$s.available; owned=[bool]$s.owned; top=[double]$s.top; bottom=[double]$s.bottom; percent=[double]$s.percent; viewSize=[double]$s.viewSize }
  return $safe
}

function Wait-CodingScrollProgress([object]$Before, [scriptblock]$ReadCapture, [hashtable]$Record, [scriptblock]$Clock, [scriptblock]$Pause) {
  $started=& $Clock
  $last=$Before.fingerprint
  $moved=$false
  $Record.polls=0; $Record.elapsed_ms=0; $Record.outcome='waiting'
  # UIA actions can return before the renderer/AX update is observable. Only
  # read fresh facts here: never resend the scroll action or relax readiness.
  for ($poll=0; $poll -lt 41; $poll++) {
    if ($poll -gt 0) { & $Pause 50 }
    $elapsed=(& $Clock)-$started
    $Record.elapsed_ms=$elapsed
    if ($elapsed -ge 2000) { break }
    if ($elapsed -lt 0) { throw 'coding-scroll: invalid observation clock' }
    $capture=& $ReadCapture
    $Record.polls++
    $elapsed=(& $Clock)-$started
    $Record.elapsed_ms=$elapsed
    if ($null -eq $capture) { throw 'coding-scroll: required anchor disappeared' }
    $Record.after=Get-CodingScrollGeometry $capture.facts
    if (-not (Test-CodingScrollReady $capture.facts)) { throw 'coding-scroll: invalid anchor identity/state' }
    if ($elapsed -ge 2000) { break }
    if ($elapsed -lt 0) { throw 'coding-scroll: invalid observation clock' }
    $facts=$capture.facts
    if ($facts.fingerprint -cne $Before.fingerprint) {
      $moved=$true
      if ($facts.upper.visible -and $facts.checkbox.visible -and $facts.refresh.visible) { $Record.outcome='visible'; return $capture }
      if ($facts.fingerprint -ceq $last) { $Record.outcome='settled'; return $capture }
    }
    $last=$facts.fingerprint
  }
  if ($moved) { $Record.outcome='not_settled'; throw 'coding-scroll: did not settle before deadline' }
  $Record.outcome='no_progress'; throw 'coding-scroll: no progress before deadline'
}

function Invoke-CodingScrollLoop([scriptblock]$ReadCapture, [scriptblock]$Apply, [scriptblock]$Clock, [scriptblock]$Pause) {
  $previous = $null
  $attempted = @{}
  $capture = $null
  $decision = $null
  $step=0
  $history=New-Object 'System.Collections.Generic.List[object]'
  try {
    $capture=& $ReadCapture
    for ($step = 0; $step -lt 16; $step++) {
      if ($null -eq $capture) { throw 'coding-scroll: required anchor disappeared' }
      $decision = Get-CodingScrollDecision $capture.facts $step $previous $attempted
      if ($decision.kind -eq 'done') {
        [Console]::Error.WriteLine('coding-scroll: ' + (@{ outcome='visible'; history=$history.ToArray() } | ConvertTo-Json -Compress -Depth 6))
        return
      }
      $previous = $capture.facts
      if ($decision.kind -eq 'item') { $attempted[$decision.target] = $true }
      $record=@{ action=$decision.kind; target=$decision.target; direction=$decision.direction; before=(Get-CodingScrollGeometry $capture.facts); polls=0; outcome='action_pending' }
      if ($decision.kind -eq 'percent') { $record.requested_percent=[double]$decision.percent }
      $history.Add($record)
      & $Apply $capture $decision
      $capture=Wait-CodingScrollProgress $previous $ReadCapture $record $Clock $Pause
    }
    if ($null -eq $capture) { throw 'coding-scroll: required anchor disappeared' }
    $decision = Get-CodingScrollDecision $capture.facts 16 $previous $attempted
    if ($decision.kind -ne 'done') { throw 'coding-scroll: decision bound exceeded' }
    [Console]::Error.WriteLine('coding-scroll: ' + (@{ outcome='visible'; history=$history.ToArray() } | ConvertTo-Json -Compress -Depth 6))
  } catch {
    # At most sixteen records of typed geometry/state and fixed enum labels.
    # No arbitrary UI text, fingerprint, paths, exception body or account data.
    $diagnostic = @{ outcome='failed'; decisions=$step; history=$history.ToArray() }
    [Console]::Error.WriteLine('coding-scroll: ' + ($diagnostic | ConvertTo-Json -Compress -Depth 6))
    throw
  }
}

function Scroll-CodingControlsIntoView([object]$Section, [int]$OwnerProcess) {
  $watch=[System.Diagnostics.Stopwatch]::StartNew()
  Invoke-CodingScrollLoop { Get-CodingScrollCapture $Section $OwnerProcess } { param($Capture,$Decision) Invoke-CodingScrollStep $Capture $Decision } { $watch.Elapsed.TotalMilliseconds } { param($Milliseconds) Start-Sleep -Milliseconds $Milliseconds }
}
