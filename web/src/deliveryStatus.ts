import { isControlledMCPReadMission, stateNames, type Mission } from "./types";

export type DecisionKind =
  "context" | "mcp_call" | "plan" | "delivery" | "attention";

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
  isControlledMCPReadMission(mission) &&
  mission.state === "planning" &&
  mcpStageNames[mission.stage]
    ? mcpStageNames[mission.stage]
    : (stateNames[mission.state] ?? mission.state);

export function decisionKind(mission: Mission): DecisionKind | null {
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
