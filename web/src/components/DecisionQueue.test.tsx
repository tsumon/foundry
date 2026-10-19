import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { mission } from "../test/fixtures";
import type { Mission } from "../types";
import { DecisionQueue } from "./DecisionQueue";

const item = (id: string, state: string, updated_at: string): Mission => ({
  ...mission,
  id,
  title: `任务 ${id}`,
  state,
  revision: Number(id.slice(1)),
  plan: state === "awaiting_approval" ? null : mission.plan,
  evidence: state === "awaiting_delivery" ? [] : mission.evidence,
  updated_at,
});

it("separates approvals from attention, discloses missing review objects and only navigates", () => {
  const onOpenMission = vi.fn();
  render(
    <DecisionQueue
      missions={[
        item("m5", "running", "2026-10-18T12:00:00Z"),
        item("m4", "paused", "2026-10-18T08:00:00Z"),
        item("m3", "awaiting_delivery", "2026-10-18T09:00:00Z"),
        item("m2", "awaiting_approval", "2026-10-18T10:00:00Z"),
        item("m1", "draft", "2026-10-18T11:00:00Z"),
      ]}
      loading={false}
      error=""
      onOpenMission={onOpenMission}
    />,
  );
  const approvals = screen.getByRole("region", { name: "待审批" });
  expect(
    within(approvals)
      .getAllByRole("article")
      .map((row) => within(row).getByRole("heading").textContent),
  ).toEqual(["任务 m1", "任务 m2", "任务 m3"]);
  expect(within(approvals).getByText("计划尚不可用")).toBeVisible();
  expect(within(approvals).getByText(/交付证据尚不可用/)).toBeVisible();
  const attention = screen.getByRole("region", { name: "需要处理" });
  expect(within(attention).getByText("任务 m4")).toBeVisible();
  expect(
    within(attention).queryByRole("button", { name: /恢复|重试|批准/ }),
  ).not.toBeInTheDocument();
  fireEvent.click(
    within(approvals).getByRole("button", { name: "查看上下文：任务 m1" }),
  );
  expect(onOpenMission).toHaveBeenCalledWith("m1", true, "总览");
});

it("uses stage-specific action labels and exact initial task views", () => {
  const onOpenMission = vi.fn();
  render(
    <DecisionQueue
      missions={[
        item("m4", "blocked", "2026-10-18T08:00:00Z"),
        item("m3", "awaiting_delivery", "2026-10-18T09:00:00Z"),
        item("m2", "awaiting_approval", "2026-10-18T10:00:00Z"),
        item("m1", "draft", "2026-10-18T11:00:00Z"),
      ]}
      loading={false}
      error=""
      onOpenMission={onOpenMission}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "审阅计划：任务 m2" }));
  fireEvent.click(
    screen.getByRole("button", { name: "审阅交付证据：任务 m3" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "查看原因：任务 m4" }));
  expect(onOpenMission.mock.calls).toEqual([
    ["m2", true, "计划链路"],
    ["m3", true, "验证"],
    ["m4", true, "总览"],
  ]);
});

it("routes an exact v2 tool proposal to the existing overview without exposing inline approval", () => {
  const onOpenMission = vi.fn();
  const waiting = {
    ...item("m1", "planning", "2026-10-18T11:00:00Z"),
    workspace_mode: "generations_controlled_mcp_read_v2",
    tool_mode: "controlled_mcp_read_v2",
    stage: "awaiting_tool_approval",
    research_status: {
      current: true,
      can_decide: true,
      can_continue: false,
    },
  } as Mission;
  render(
    <DecisionQueue
      missions={[waiting]}
      loading={false}
      error=""
      onOpenMission={onOpenMission}
    />,
  );
  expect(screen.getByText("等待工具审批")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: /批准本次调用|拒绝本次调用/ }),
  ).not.toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "审阅本次工具调用：任务 m1" }),
  );
  expect(onOpenMission).toHaveBeenCalledWith("m1", true, "总览");
});

it("has honest loading, error and empty states", () => {
  const onRetry = vi.fn();
  const { rerender } = render(
    <DecisionQueue
      missions={[]}
      loading
      error=""
      onOpenMission={() => {}}
      onRetry={onRetry}
    />,
  );
  expect(screen.getByText("正在读取待决策任务…")).toBeVisible();
  rerender(
    <DecisionQueue
      missions={[]}
      loading={false}
      error="offline"
      onOpenMission={() => {}}
      onRetry={onRetry}
    />,
  );
  expect(screen.getByRole("alert")).toHaveTextContent("无法读取待决策任务");
  fireEvent.click(screen.getByRole("button", { name: "重新连接" }));
  expect(onRetry).toHaveBeenCalledOnce();
  rerender(
    <DecisionQueue
      missions={[item("m1", "running", "2026-10-18T11:00:00Z")]}
      loading={false}
      error=""
      onOpenMission={() => {}}
      onRetry={onRetry}
    />,
  );
  expect(screen.getByText("当前没有待审批或需要处理的任务。")).toBeVisible();
});

it("preserves received queue rows and labels them stale after a refresh error", () => {
  render(
    <DecisionQueue
      missions={[item("m1", "draft", "2026-10-18T11:00:00Z")]}
      loading={false}
      error="offline"
      stream="事件流已连接"
      onOpenMission={() => {}}
    />,
  );
  expect(screen.getByRole("heading", { name: "任务 m1" })).toBeVisible();
  expect(screen.getByRole("alert")).toHaveTextContent(
    "以下为最后收到的待决策状态 · 更新失败：offline",
  );
  expect(
    screen.queryByText("无法读取待决策任务：offline"),
  ).not.toBeInTheDocument();
});

it("labels received rows as last received while reconnecting or refreshing", () => {
  const onRetry = vi.fn();
  const props = {
    missions: [item("m1", "draft", "2026-10-18T11:00:00Z")],
    error: "",
    onOpenMission: () => {},
    onRetry,
  };
  const { rerender } = render(
    <DecisionQueue {...props} loading={false} stream="事件流中断 · 正在重连" />,
  );
  expect(screen.getByRole("status")).toHaveTextContent(
    "以下为最后收到的待决策状态 · 事件流中断 · 正在重连",
  );
  expect(screen.getByRole("heading", { name: "任务 m1" })).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "重新连接" }));
  expect(onRetry).toHaveBeenCalledOnce();
  rerender(<DecisionQueue {...props} loading stream="事件流已连接" />);
  expect(screen.getByRole("status")).toHaveTextContent(
    "正在刷新 · 以下为最后收到的待决策状态",
  );
  expect(screen.queryByText("正在读取待决策任务…")).not.toBeInTheDocument();
});
