import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { mission } from "../test/fixtures";
import type { Mission } from "../types";
import { DeliveryOverview } from "./DeliveryOverview";

const item = (id: string, state: string, updated_at: string): Mission => ({
  ...mission,
  id,
  title: `任务 ${id}`,
  state,
  revision: Number(id.slice(1)),
  plan: state === "awaiting_approval" ? null : mission.plan,
  evidence: state === "awaiting_delivery" ? [] : mission.evidence,
  budget: { ...mission.budget, used_calls: Number(id.slice(1)), max_calls: 16 },
  updated_at,
});

it("shows real grouped counts, unknown states, ordered task facts and a bounded session event list", () => {
  const missions = [
    item("m6", "future_state", "2026-10-18T06:00:00Z"),
    item("m5", "delivered", "2026-10-18T07:00:00Z"),
    item("m4", "blocked", "2026-10-18T08:00:00Z"),
    item("m3", "awaiting_delivery", "2026-10-18T09:00:00Z"),
    item("m2", "draft", "2026-10-18T10:00:00Z"),
    item("m1", "running", "2026-10-18T11:00:00Z"),
  ];
  render(
    <DeliveryOverview
      missions={missions}
      events={[
        {
          seq: 11,
          mission_id: "m1",
          kind: "mission.changed",
          revision: 1,
          created_at: "2026-10-18T11:01:00Z",
        },
        {
          seq: 12,
          mission_id: "m4",
          kind: "mission.blocked",
          revision: 4,
          created_at: "2026-10-18T11:02:00Z",
        },
      ]}
      loading={false}
      error=""
      stream="事件流已连接"
      onOpenMission={() => {}}
      onCreate={() => {}}
      onSettings={() => {}}
      onDemo={() => {}}
    />,
  );
  expect(screen.getByTestId("metric-in-progress")).toHaveTextContent("1");
  expect(screen.getByTestId("metric-decisions")).toHaveTextContent("2");
  expect(screen.getByTestId("metric-attention")).toHaveTextContent("1");
  expect(screen.getByTestId("metric-delivered")).toHaveTextContent("1");
  expect(screen.getByTestId("metric-unknown")).toHaveTextContent("1");
  const rows = screen.getAllByRole("article");
  expect(
    rows.map((row) => within(row).getByRole("heading").textContent),
  ).toEqual(["任务 m1", "任务 m2", "任务 m3", "任务 m4", "任务 m5", "任务 m6"]);
  expect(rows[0]).toHaveTextContent("r1");
  expect(rows[0]).toHaveTextContent("1 / 16 次调用");
  expect(rows[0]).toHaveTextContent("2026-10-18T11:00:00Z");
  expect(screen.getByText("未知状态：future_state")).toBeVisible();
  expect(screen.getByText(/本次页面会话最多保留最近 200 条/)).toBeVisible();
  expect(screen.getByText("#12").parentElement).toHaveTextContent("r4");
});

it("attributes recent events to known mission titles and falls back to the stable mission id", () => {
  render(
    <DeliveryOverview
      missions={[
        item("m1", "running", "2026-10-18T11:00:00Z"),
        {
          ...item("m2", "draft", "2026-10-18T10:00:00Z"),
          title: "第二个任务",
        },
      ]}
      events={[
        {
          seq: 21,
          mission_id: "m2",
          kind: "mission.changed",
          revision: 2,
          created_at: "2026-10-18T11:01:00Z",
        },
        {
          seq: 22,
          mission_id: "missing-id",
          kind: "mission.changed",
          revision: 3,
          created_at: "2026-10-18T11:02:00Z",
        },
      ]}
      loading={false}
      error=""
      stream="事件流已连接"
      onOpenMission={() => {}}
      onCreate={() => {}}
      onSettings={() => {}}
      onDemo={() => {}}
    />,
  );
  const knownEvent = screen.getByText("#21").parentElement!;
  expect(knownEvent).toHaveTextContent("第二个任务");
  expect(
    within(knownEvent).getByText("m2", { selector: "code" }),
  ).toBeVisible();
  const missingEvent = screen.getByText("#22").parentElement!;
  expect(missingEvent).toHaveTextContent("任务 ID missing-id");
  expect(missingEvent).not.toHaveTextContent("未知任务");
});

it("does not present zero metrics as facts while loading or after a failed read", () => {
  const props = {
    missions: [],
    events: [],
    stream: "未连接",
    onOpenMission: vi.fn(),
    onCreate: vi.fn(),
    onSettings: vi.fn(),
    onDemo: vi.fn(),
  };
  const { rerender } = render(<DeliveryOverview {...props} loading error="" />);
  expect(screen.getByText("正在读取本地交付状态…")).toBeVisible();
  expect(screen.queryByTestId("metric-in-progress")).not.toBeInTheDocument();
  rerender(<DeliveryOverview {...props} loading={false} error="offline" />);
  expect(screen.getByRole("alert")).toHaveTextContent("无法读取最新状态");
  expect(screen.queryByTestId("metric-delivered")).not.toBeInTheDocument();
});

it("keeps explicit onboarding actions when there are no missions", () => {
  const onCreate = vi.fn();
  render(
    <DeliveryOverview
      missions={[]}
      events={[]}
      loading={false}
      error=""
      stream="事件流已连接"
      onOpenMission={() => {}}
      onCreate={onCreate}
      onSettings={() => {}}
      onDemo={() => {}}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "创建交付任务" }));
  expect(onCreate).toHaveBeenCalledOnce();
  expect(screen.getByRole("button", { name: "配置模型" })).toBeVisible();
  expect(screen.getByRole("button", { name: "查看演示" })).toBeVisible();
});

it("keeps received mission rows visible while a refresh is loading", () => {
  render(
    <DeliveryOverview
      missions={[item("m1", "running", "2026-10-18T11:00:00Z")]}
      events={[]}
      loading
      error=""
      stream="未连接"
      onOpenMission={() => {}}
      onCreate={() => {}}
      onSettings={() => {}}
      onDemo={() => {}}
    />,
  );
  expect(screen.getByRole("heading", { name: "任务 m1" })).toBeVisible();
  expect(screen.getByRole("status")).toHaveTextContent(
    "正在刷新 · 以下为最后收到的任务状态",
  );
  expect(screen.queryByText("正在读取本地交付状态…")).not.toBeInTheDocument();
});

it("offers an explicit reconnect while retained overview rows are stale", () => {
  const onRetry = vi.fn();
  render(
    <DeliveryOverview
      missions={[item("m1", "running", "2026-10-18T11:00:00Z")]}
      events={[]}
      loading={false}
      error="offline"
      stream="未连接"
      onOpenMission={() => {}}
      onCreate={() => {}}
      onSettings={() => {}}
      onDemo={() => {}}
      onRetry={onRetry}
    />,
  );
  expect(screen.getByRole("heading", { name: "任务 m1" })).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "重新连接" }));
  expect(onRetry).toHaveBeenCalledOnce();
});

it("counts an exact v2 tool proposal as pending but not simultaneously in progress", () => {
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
    <DeliveryOverview
      missions={[waiting]}
      events={[]}
      loading={false}
      error=""
      stream="事件流已连接"
      onOpenMission={() => {}}
      onCreate={() => {}}
      onSettings={() => {}}
      onDemo={() => {}}
    />,
  );
  expect(screen.getByTestId("metric-decisions")).toHaveTextContent("1");
  expect(screen.getByTestId("metric-in-progress")).toHaveTextContent("0");
  expect(screen.getByText("等待工具审批")).toBeVisible();
});
