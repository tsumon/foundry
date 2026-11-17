import { useMemo, useState } from "react";

export type DiffLineKind = "context" | "add" | "remove" | "info";

export interface DiffLine {
  kind: DiffLineKind;
  oldLine: number | null;
  newLine: number | null;
  text: string;
}

function splitLines(text: string): string[] {
  if (!text) return [];
  return text.split(/\r?\n/);
}

/**
 * Computes a bounded line-level diff between beforeText and afterText.
 * Uses exact LCS on the changed middle span up to 250k cell comparisons and
 * degrades cleanly to a hunk block replacement for huge generated files.
 */
export function computeLineDiff(
  beforeText: string,
  afterText: string,
): DiffLine[] {
  const a = splitLines(beforeText);
  const b = splitLines(afterText);
  let prefix = 0;
  while (prefix < a.length && prefix < b.length && a[prefix] === b[prefix]) {
    prefix++;
  }
  let suffix = 0;
  while (
    suffix < a.length - prefix &&
    suffix < b.length - prefix &&
    a[a.length - 1 - suffix] === b[b.length - 1 - suffix]
  ) {
    suffix++;
  }

  const out: DiffLine[] = [];
  for (let i = 0; i < prefix; i++) {
    out.push({ kind: "context", oldLine: i + 1, newLine: i + 1, text: a[i] });
  }

  const aMid = a.slice(prefix, a.length - suffix);
  const bMid = b.slice(prefix, b.length - suffix);

  if (aMid.length === 0 && bMid.length === 0) {
    return out;
  }

  if (aMid.length * bMid.length > 250000) {
    out.push({
      kind: "info",
      oldLine: null,
      newLine: null,
      text: `变更跨度较大（${aMid.length} 行 → ${bMid.length} 行），展示块级替换`,
    });
    for (let i = 0; i < aMid.length; i++) {
      out.push({
        kind: "remove",
        oldLine: prefix + i + 1,
        newLine: null,
        text: aMid[i],
      });
    }
    for (let j = 0; j < bMid.length; j++) {
      out.push({
        kind: "add",
        oldLine: null,
        newLine: prefix + j + 1,
        text: bMid[j],
      });
    }
  } else {
    const rows = aMid.length + 1;
    const cols = bMid.length + 1;
    const dp = new Int32Array(rows * cols);
    for (let i = aMid.length - 1; i >= 0; i--) {
      for (let j = bMid.length - 1; j >= 0; j--) {
        if (aMid[i] === bMid[j]) {
          dp[i * cols + j] = dp[(i + 1) * cols + (j + 1)] + 1;
        } else {
          const down = dp[(i + 1) * cols + j];
          const right = dp[i * cols + (j + 1)];
          dp[i * cols + j] = down >= right ? down : right;
        }
      }
    }
    let i = 0;
    let j = 0;
    while (i < aMid.length && j < bMid.length) {
      if (aMid[i] === bMid[j]) {
        out.push({
          kind: "context",
          oldLine: prefix + i + 1,
          newLine: prefix + j + 1,
          text: aMid[i],
        });
        i++;
        j++;
      } else if (dp[(i + 1) * cols + j] >= dp[i * cols + (j + 1)]) {
        out.push({
          kind: "remove",
          oldLine: prefix + i + 1,
          newLine: null,
          text: aMid[i],
        });
        i++;
      } else {
        out.push({
          kind: "add",
          oldLine: null,
          newLine: prefix + j + 1,
          text: bMid[j],
        });
        j++;
      }
    }
    while (i < aMid.length) {
      out.push({
        kind: "remove",
        oldLine: prefix + i + 1,
        newLine: null,
        text: aMid[i],
      });
      i++;
    }
    while (j < bMid.length) {
      out.push({
        kind: "add",
        oldLine: null,
        newLine: prefix + j + 1,
        text: bMid[j],
      });
      j++;
    }
  }

  const aSuffixStart = a.length - suffix;
  const bSuffixStart = b.length - suffix;
  for (let k = 0; k < suffix; k++) {
    out.push({
      kind: "context",
      oldLine: aSuffixStart + k + 1,
      newLine: bSuffixStart + k + 1,
      text: a[aSuffixStart + k],
    });
  }
  return out;
}

export function collapseDiffContext(
  lines: DiffLine[],
  contextRadius = 3,
): DiffLine[] {
  const keep = new Uint8Array(lines.length);
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].kind !== "context") {
      const start = Math.max(0, i - contextRadius);
      const end = Math.min(lines.length - 1, i + contextRadius);
      for (let k = start; k <= end; k++) keep[k] = 1;
    }
  }
  const result: DiffLine[] = [];
  let skipping = false;
  for (let i = 0; i < lines.length; i++) {
    if (keep[i]) {
      result.push(lines[i]);
      skipping = false;
    } else if (!skipping) {
      skipping = true;
      result.push({
        kind: "info",
        oldLine: null,
        newLine: null,
        text: "⋯ 未变更上下文已折叠 ⋯",
      });
    }
  }
  return result;
}

export function CodeDiffView({
  before,
  after,
  sideBySide = false,
}: {
  before: string;
  after: string;
  sideBySide?: boolean;
}) {
  const [showAll, setShowAll] = useState(false);
  const diff = useMemo(() => computeLineDiff(before, after), [before, after]);
  const counts = useMemo(() => {
    let added = 0;
    let removed = 0;
    for (const line of diff) {
      if (line.kind === "add") added++;
      if (line.kind === "remove") removed++;
    }
    return { added, removed };
  }, [diff]);

  if (sideBySide) {
    return (
      <div className="code-pair">
        <section>
          <h3>变更前</h3>
          <pre>{before || "（空文件 / 新文件）"}</pre>
        </section>
        <section>
          <h3>变更后</h3>
          <pre>{after || "（空内容）"}</pre>
        </section>
      </div>
    );
  }

  const hasLongContext =
    diff.length > 40 && diff.some((line) => line.kind === "context");
  const visible =
    showAll || !hasLongContext ? diff : collapseDiffContext(diff, 3);

  return (
    <div className="code-diff">
      <div className="code-diff-toolbar">
        <span className="diff-stats">
          <span className="diff-stat-add">+{counts.added}</span>
          <span className="diff-stat-remove">−{counts.removed}</span>
          {before === after && <span className="muted">（内容未变化）</span>}
        </span>
        {hasLongContext && (
          <button
            type="button"
            className="text-button"
            onClick={() => setShowAll((current) => !current)}
          >
            {showAll ? "折叠未变更上下文" : `展开全部 ${diff.length} 行`}
          </button>
        )}
      </div>
      {visible.length === 0 ? (
        <pre className="diff-empty">（空内容）</pre>
      ) : (
        <div className="diff-table" role="region" aria-label="行级代码差异">
          {visible.map((line, index) => (
            <div key={index} className={`diff-row diff-${line.kind}`}>
              <span className="diff-ln">{line.oldLine ?? ""}</span>
              <span className="diff-ln">{line.newLine ?? ""}</span>
              <span className="diff-sign" aria-hidden="true">
                {line.kind === "add"
                  ? "+"
                  : line.kind === "remove"
                    ? "-"
                    : line.kind === "info"
                      ? "⋯"
                      : " "}
              </span>
              <code>{line.text || " "}</code>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
