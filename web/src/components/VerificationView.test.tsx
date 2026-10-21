import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { mission } from "../test/fixtures";
import { VerificationView } from "./VerificationView";
import type { Verification } from "../types";

it.each([
  [1, "synthetic_stub", "流程模拟（未执行命令）", "模拟验证"],
  [1, "synthetic_content_check", "内容检查模拟（未执行命令）", "模拟验证"],
  [1, "isolated_execution", "隔离命令执行", "验收命令"],
  [1, "unknown", "来源未记录", "验证记录"],
  [1, "future", "来源未记录", "验证记录"],
  [2, "isolated_execution", "来源未记录", "验证记录"],
  [undefined, "isolated_execution", "来源未记录", "验证记录"],
  [undefined, undefined, "来源未记录", "验证记录"],
])(
  "shows source %s/%s without upgrading success or failure",
  (version, kind, label, outcome) => {
    const evidence = [true, false].map(
      (passed, i) =>
        ({
          id: String(i),
          candidate_digest: "same",
          image_id: "sha256:isolated_execution",
          created_at: "today",
          acceptance_revision: 1,
          passed,
          results: [
            {
              command: "test",
              exit_code: passed ? 0 : 1,
              output: "",
              timed_out: false,
            },
          ],
          source_version: version,
          source_kind: kind,
        }) as Verification,
    );
    render(
      <VerificationView mission={{ ...mission, evidence, reviews: [] }} />,
    );
    expect(
      screen.getAllByRole("heading", {
        name: new RegExp(label!.replace(/[（ ）]/g, ".")),
      }),
    ).toHaveLength(2);
    expect(screen.getByText(`${outcome}通过`)).toBeVisible();
    expect(screen.getByText(`${outcome}未通过`)).toBeVisible();
    if (kind !== "isolated_execution" || version !== 1)
      expect(screen.queryByText("验收命令通过")).not.toBeInTheDocument();
  },
);

it("retains individual sources for mixed attempts on the same candidate", () => {
  const evidence = ["synthetic_stub", "isolated_execution"].map(
    (source_kind, i) => ({
      id: String(i),
      candidate_digest: "same",
      image_id: "image",
      created_at: "today",
      acceptance_revision: 1,
      passed: true,
      results: [],
      source_version: 1,
      source_kind,
    }),
  );
  render(<VerificationView mission={{ ...mission, evidence, reviews: [] }} />);
  expect(
    screen.getByRole("heading", { name: /流程模拟（未执行命令）/ }),
  ).toBeVisible();
  expect(screen.getByRole("heading", { name: /隔离命令执行/ })).toBeVisible();
});
