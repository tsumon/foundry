import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { mission } from "../test/fixtures";
import { ResearchBrief } from "./ResearchBrief";

it("keeps repository paths separate from synthetic MCP citations", () => {
  render(
    <ResearchBrief
      mission={{
        ...mission,
        workspace_mode: "generations_controlled_mcp_read_v2",
        tool_mode: "controlled_mcp_read_v2",
        research: {
          summary: "核对完成",
          findings: [
            {
              severity: "info",
              summary: "观察来自两类来源",
              paths: ["src/import.ts"],
              sources: [
                {
                  kind: "mcp",
                  invocation_id: "call-1",
                  digest: "result-digest",
                },
              ],
            },
          ],
          unknowns: [],
        },
      }}
    />,
  );
  expect(screen.getByText("src/import.ts")).toBeVisible();
  expect(screen.getByText(/本地合成参考 · 调用 call-1/)).toBeVisible();
  expect(screen.getByText(/result-digest/)).toBeVisible();
  expect(screen.getByText(/不是 Docker 验证或交付证据/)).toBeVisible();
});
