import { isControlledMCPReadMission, stateNames, type Mission } from "./types";
import { hasC4, c4BaselinePassed } from "./c4";

export type DecisionKind =
  "context" | "mcp_call" | "plan" | "delivery" | "attention" | "c4_baseline";

export const isMCPDecisionPending = (mission: Mission) =>
  isControlledMCPReadMission(mission) &&
  mission.state === "planning" &&
  mission.stage === "awaiting_tool_approval";

const mcpStageNames: Record<string, string> = {
  research_turn: "正在调研",
  awaiting_tool_approval: "等待工具审批",
  mcp_authorized_pending: "已批准，等待派发",
  mcp_call: "正在调用本地测试工具",
  mcp_observation_ready: "工具结果已保存",
  planner_ready: "调研完成，等待生成计划",
};

export const missionStateName = (mission: Mission) =>
  hasC4(mission) &&
  ["draft", "blocked", "paused"].includes(mission.state) &&
  !c4BaselinePassed(mission) &&
  mission.c4_operations?.some(
    (operation) => operation.kind === "baseline" && !!operation.result,
  )
    ? "C4 需要处理"
    : isControlledMCPReadMission(mission) &&
        mission.state === "planning" &&
        mcpStageNames[mission.stage]
      ? mcpStageNames[mission.stage]
      : (stateNames[mission.state] ?? mission.state);

export function decisionKind(mission: Mission): DecisionKind | null {
  if (
    hasC4(mission) &&
    ["draft", "blocked", "paused"].includes(mission.state) &&
    !c4BaselinePassed(mission)
  )
    return mission.c4_operations?.some((o) => o.result)
      ? "attention"
      : "c4_baseline";
  if (isMCPDecisionPending(mission)) return "mcp_call";
  switch (mission.state) {
    case "draft":
      return "context";
    case "awaiting_approval":
      return "plan";
    case "awaiting_delivery":
      return "delivery";
    case "paused":
    case "blocked":
      return "attention";
    default:
      return null;
  }
}

export function orderMissions(missions: Mission[]): Mission[] {
  return [...missions].sort(
    (left, right) =>
      right.updated_at.localeCompare(left.updated_at) ||
      left.id.localeCompare(right.id),
  );
}

export const inProgressStates = ["planning", "running", "verifying", "pausing"];
export const isMissionInProgress = (mission: Mission) =>
  inProgressStates.includes(mission.state) && !isMCPDecisionPending(mission);
export const knownStates = [
  "draft",
  ...inProgressStates,
  "awaiting_approval",
  "awaiting_delivery",
  "paused",
  "blocked",
  "delivered",
];
