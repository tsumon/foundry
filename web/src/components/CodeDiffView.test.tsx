import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { mission } from "../test/fixtures";
import {
  CodeDiffView,
  collapseDiffContext,
  computeLineDiff,
} from "./CodeDiffView";
import { MissionViews } from "./MissionViews";

it("computes exact line-level additions, removals, and context collapse", () => {
  const diff = computeLineDiff("a\nb\nc\nd", "a\nb-edited\nc\nd\ne");
  expect(diff).toEqual([
    { kind: "context", oldLine: 1, newLine: 1, text: "a" },
    { kind: "remove", oldLine: 2, newLine: null, text: "b" },
    { kind: "add", oldLine: null, newLine: 2, text: "b-edited" },
    { kind: "context", oldLine: 3, newLine: 3, text: "c" },
    { kind: "context", oldLine: 4, newLine: 4, text: "d" },
    { kind: "add", oldLine: null, newLine: 5, text: "e" },
  ]);

  const longBefore = Array.from({ length: 50 }, (_, i) => `line ${i}`).join("\n");
  const longAfter = longBefore.replace("line 25", "line 25 changed");
  const collapsed = collapseDiffContext(computeLineDiff(longBefore, longAfter), 2);
  expect(collapsed.some((line) => line.kind === "info")).toBe(true);
  expect(collapsed.length).toBeLessThan(15);
});

it("switches between step history, net baseline diff, and side-by-side in code changes view", () => {
  const withChanges = {
    ...mission,
    changes: [
      { path: "src/app.js", before: "const a = 1;", after: "const a = 2;" },
      { path: "src/app.js", before: "const a = 2;", after: "const a = 3;" },
    ],
  };
  render(<MissionViews mission={withChanges} view="代码变更" />);
  expect(screen.getAllByText("src/app.js")).toHaveLength(2);
  expect(screen.getByText("变更 1")).toBeVisible();
  expect(screen.getByText("变更 2")).toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "最终候选对比基线" }));
  expect(screen.getAllByText("src/app.js")).toHaveLength(1);
  expect(screen.getByText("const a = 1;")).toBeVisible();
  expect(screen.getByText("const a = 3;")).toBeVisible();

  fireEvent.click(screen.getByRole("button", { name: "并排全文" }));
  expect(screen.getAllByText("变更前")).toHaveLength(2);
  expect(screen.getAllByText("变更后")).toHaveLength(2);
});

it("renders standalone html download and continue iteration actions on delivered idea missions", () => {
  const onContinueIdea = vi.fn();
  const deliveredIdea = {
    ...mission,
    state: "delivered",
    candidate_digest: "digest-1",
    idea_project: {
      schema_version: 2 as const,
      template: "browser_app_v1" as const,
      acceptance: ["Req 1"],
    },
    delivery: {
      patch_path: "/tmp/p",
      report_path: "/tmp/r",
      candidate_digest: "digest-1",
      created_at: "today",
    },
  };
  render(
    <MissionViews
      mission={deliveredIdea}
      view="交付报告"
      onContinueIdea={onContinueIdea}
    />,
  );
  expect(
    screen.getByRole("link", { name: "下载完整应用 ZIP" }),
  ).toHaveAttribute("href", "/api/missions/m1/source");
  expect(
    screen.getByRole("link", { name: "下载单文件应用 (.html)" }),
  ).toHaveAttribute("href", "/api/missions/m1/app-html");
  fireEvent.click(
    screen.getByRole("button", { name: "基于当前版本继续改进" }),
  );
  expect(onContinueIdea).toHaveBeenCalledWith(deliveredIdea);
});

it("supports expanding collapsed long diffs in CodeDiffView", () => {
  const before = Array.from({ length: 45 }, (_, i) => `row-${i}`).join("\n");
  const after = before.replace("row-20", "row-20-updated");
  render(<CodeDiffView before={before} after={after} />);
  expect(screen.getAllByText(/未变更上下文已折叠/)).toHaveLength(2);
  fireEvent.click(screen.getByRole("button", { name: /展开全部/ }));
  expect(screen.getByText("row-0")).toBeVisible();
  expect(screen.getByText("row-20-updated")).toBeVisible();
});
