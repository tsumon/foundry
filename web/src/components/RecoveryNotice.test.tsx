import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { response } from "../test/fixtures";
import { RecoveryNotice } from "./RecoveryNotice";

it.each([
  ["c4_runtime_mismatch", "当前安装的内置沙盒与任务冻结的版本或摘要不一致"],
  ["c4_unavailable", "C4 配置、控制记录或任务绑定不可用"],
  ["coding_workbench_unavailable", "当前安装的编码工作台运行环境不可用"],
  ["future_reason_with_private_details", "恢复条件未满足或诊断类型不受支持"],
  [undefined, "恢复条件未满足或诊断类型不受支持"],
])(
  "uses explicit diagnostic %s without claiming snapshot corruption",
  async (code, text) => {
    const fetcher = vi.fn(async (_path: string) =>
      response({
        status: "recovery_limited",
        read_only: true,
        c4_controlled_only: false,
        coding_workbench: true,
        recovery_error: code,
      }),
    );
    vi.stubGlobal("fetch", fetcher);
    render(<RecoveryNotice enabled />);
    const notice = await screen.findByRole("alert");
    expect(notice).toHaveTextContent(text);
    expect(notice).toHaveTextContent("历史仍可查看");
    expect(notice).toHaveTextContent("所有业务变更、模型和工具调用已禁止");
    expect(notice).not.toHaveTextContent("任务模型快照损坏");
    expect(notice).not.toHaveTextContent("future_reason_with_private_details");
    if (code === "c4_runtime_mismatch") {
      expect(notice).toHaveTextContent("原基线、证据和已用预算保留");
      expect(notice).toHaveTextContent("不会自动更换绑定");
    }
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0][0]).toBe("/api/health");
  },
);

it("still identifies genuinely invalid snapshots even on a C4 instance", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      response({
        status: "recovery_limited",
        read_only: true,
        c4_controlled_only: true,
        recovery_error: "invalid_mission_model_snapshot",
      }),
    ),
  );
  render(<RecoveryNotice enabled />);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "任务模型快照损坏或版本不支持",
  );
});

it.each([undefined, ""])(
  "does not infer missing diagnostic %s from legacy C4 mode",
  async (code) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        response({
          status: "recovery_limited",
          read_only: true,
          c4_controlled_only: true,
          recovery_error: code,
        }),
      ),
    );
    render(<RecoveryNotice enabled />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "恢复条件未满足或诊断类型不受支持",
    );
  },
);
