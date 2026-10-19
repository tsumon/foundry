import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { roles, type Mission } from "../types";
import type { MissionRolePolicySnapshot } from "../rolePolicyTypes";
import { mission, profile } from "../test/fixtures";
import { RolePolicySnapshot } from "./RolePolicySnapshot";
import { ModelSnapshot } from "./ModelSnapshot";
import { accountActionBlocked, invalidModelSnapshot } from "../modelSnapshot";
import { readAppStyles } from "../test/appStyles";

const frozenPolicy = (): MissionRolePolicySnapshot => ({
  schema_version: 1,
  decision_id: "decision-" + "d".repeat(48),
  policy_revision: 7,
  recommendation_digest: "4".repeat(64),
  confirmed_at: "2026-10-19T01:04:00Z",
  digest: "5".repeat(64),
  roles: Object.fromEntries(
    roles.map((role) => [
      role,
      {
        selected_profile_id: profile.id,
        required_capabilities: ["reasoning"],
        candidates: [
          {
            profile_id: profile.id,
            profile_digest: "1".repeat(64),
            spend_class: "unknown",
            eligible: true,
            reasons: [],
            capabilities: ["reasoning"],
            rationale: "用户声明依据",
            execution_notice: "credentials_and_quota_unknown",
          },
        ],
      },
    ]),
  ) as unknown as MissionRolePolicySnapshot["roles"],
});

it("renders only the frozen future-only source and never fetches current policy", () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  render(<RolePolicySnapshot snapshot={frozenPolicy()} />);
  const region = screen.getByRole("region", { name: "冻结的角色策略来源" });
  expect(within(region).getByText(/策略版本 7/)).toBeVisible();
  expect(within(region).getByText(/只说明任务创建时的选择来源/)).toBeVisible();
  expect(
    within(region).getByText(/不会授权模型调用、工具或交付/),
  ).toBeVisible();
  expect(within(region).getAllByText(/用户声明，未独立验证/)).toHaveLength(7);
  expect(within(region).getAllByText(/凭据与额度未知/)).toHaveLength(7);
  expect(fetcher).not.toHaveBeenCalled();
});

it("discloses frozen requirements and receipt identity without fetching current metadata", () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const snapshot = frozenPolicy();
  snapshot.roles.planner.required_capabilities = [
    "structured_output",
    "reasoning",
  ];
  snapshot.roles.planner.candidates[0].evidence = {
    invocation_id: "historical-invocation-" + "i".repeat(160),
    mission_id: "historical-mission-" + "m".repeat(160),
    role: "planner",
    status: "outcome_unknown",
    error_code: "response_incomplete",
    environment: "simulation",
    finished_at: "2026-10-18T03:02:01Z",
  };
  const style = document.createElement("style");
  style.textContent = readAppStyles();
  document.head.append(style);
  try {
    render(<RolePolicySnapshot snapshot={snapshot} />);
    const disclosure = screen.getByText("查看冻结调用收据").closest("details")!;
    expect(disclosure).not.toHaveAttribute("open");
    fireEvent.click(screen.getByText("查看冻结调用收据"));
    expect(screen.getByText("冻结能力要求：结构化输出、推理")).toBeVisible();
    for (const value of [
      snapshot.roles.planner.candidates[0].evidence.invocation_id,
      snapshot.roles.planner.candidates[0].evidence.mission_id,
      "2026-10-18T03:02:01Z",
      "响应不完整",
    ]) {
      expect(within(disclosure).getByText(value)).toBeVisible();
    }
    expect(disclosure).toHaveTextContent("结果未知");
    expect(disclosure).toHaveTextContent("本地模拟记录");
    expect(disclosure).toHaveTextContent("不是能力验证");
    expect(getComputedStyle(disclosure).overflowWrap).toBe("anywhere");
    expect(getComputedStyle(disclosure).minWidth).toBe("0px");
    expect(fetcher).not.toHaveBeenCalled();
  } finally {
    style.remove();
  }
});

it("shows invalid policy provenance without trusting partial fields and blocks legacy mission actions", () => {
  const invalid = {
    ...mission,
    workspace_mode: undefined,
    model_policy_snapshot_status: "invalid_or_unsupported" as const,
    model_policy_snapshot: undefined,
  };
  expect(invalidModelSnapshot(invalid)).toBe(true);
  expect(accountActionBlocked(invalid, "plan")).toBe(true);
  render(<ModelSnapshot mission={invalid} />);
  expect(screen.getByText(/角色策略来源损坏或版本不受支持/)).toBeVisible();
  expect(screen.queryByText(/策略版本/)).not.toBeInTheDocument();
});

it("fails closed when a valid policy status is paired with missing provenance", () => {
  const partial: Mission = {
    ...mission,
    model_policy_snapshot_status: "valid",
    model_policy_snapshot: undefined,
  };
  expect(invalidModelSnapshot(partial)).toBe(true);
  expect(accountActionBlocked(partial, "approve")).toBe(true);
});

it("keeps a valid frozen source attached to historical mission details", () => {
  const historical: Mission = {
    ...mission,
    model_policy_snapshot_status: "valid",
    model_policy_snapshot: frozenPolicy(),
  };
  render(<ModelSnapshot mission={historical} />);
  expect(
    screen.getByRole("region", { name: "冻结的角色策略来源" }),
  ).toBeVisible();
  expect(
    screen.getByText(/任务创建时确认于 2026-10-19T01:04:00Z/),
  ).toBeVisible();
});
