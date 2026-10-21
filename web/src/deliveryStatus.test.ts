import { describe, expect, it } from "vitest";
import type { Mission } from "./types";
import {
  decisionKind,
  isMissionInProgress,
  missionStateName,
  orderMissions,
} from "./deliveryStatus";
import { profile } from "./test/fixtures";
import { c4Mission, c4Operation } from "./test/c4Fixtures";

const complete = (id: string, state: string, updated_at: string): Mission => ({
  id,
  title: `任务 ${id}`,
  goal: "交付一个可验证的改动",
  repo_path: "/tmp/repo",
  base_commit: "abc123",
  state,
  stage: "plan",
  revision: 4,
  epoch: 1,
  spec: {
    scope: ["src/"],
    protected: ["tests/"],
    acceptance: ["npm test"],
    image: "node:test",
    image_id: "sha256:image",
    profile_id: "p1",
    research: false,
    max_calls: 16,
  },
  profiles: {
    main: profile,
    researcher: profile,
    planner: profile,
    worker: profile,
    debugger: profile,
    qa: profile,
    auditor: profile,
  },
  context_manifest: [],
  research: null,
  plan: null,
  messages: [],
  changes: [],
  evidence: [],
  reviews: [],
  approvals: [],
  budget: {
    max_calls: 16,
    used_calls: 3,
    input_tokens: 0,
    output_tokens: 0,
    usage_unknown: true,
  },
  candidate_digest: "",
  error: "",
  created_at: "2026-10-18T00:00:00Z",
  updated_at,
  delivery: null,
});

describe("decisionKind", () => {
  it.each([
    ["draft", "context"],
    ["awaiting_approval", "plan"],
    ["awaiting_delivery", "delivery"],
    ["paused", "attention"],
    ["blocked", "attention"],
    ["planning", null],
    ["running", null],
    ["verifying", null],
    ["pausing", null],
    ["delivered", null],
    ["future_state", null],
  ])("maps %s without inferring authority", (state, expected) => {
    expect(decisionKind(complete("m1", state, "2026-10-18T10:00:00Z"))).toBe(
      expected,
    );
  });
});

it("orders rows newest first and uses id as a stable tie-breaker", () => {
  const rows = [
    complete("m3", "running", "2026-10-18T09:00:00Z"),
    complete("m2", "draft", "2026-10-18T10:00:00Z"),
    complete("m1", "blocked", "2026-10-18T10:00:00Z"),
  ];
  expect(orderMissions(rows).map((row) => row.id)).toEqual(["m1", "m2", "m3"]);
  expect(rows.map((row) => row.id)).toEqual(["m3", "m2", "m1"]);
});

it.each([
  ["paused", "已暂停", "attention", false],
  ["blocked", "已阻塞", "attention", false],
  ["pausing", "正在暂停", null, true],
])(
  "keeps %s visible above retained research stages",
  (state, label, kind, active) => {
    for (const stage of [
      "research_turn",
      "planner_ready",
      "awaiting_tool_approval",
    ]) {
      const row = {
        ...complete("m1", String(state), "2026-10-18T10:00:00Z"),
        workspace_mode: "generations_controlled_mcp_read_v2",
        tool_mode: "controlled_mcp_read_v2",
        stage,
      } as Mission;
      expect(missionStateName(row)).toBe(label);
      expect(decisionKind(row)).toBe(kind);
      expect(isMissionInProgress(row)).toBe(active);
    }
  },
);

it("retains planning-stage labels and pending counts for a v2 tool decision", () => {
  const row = {
    ...complete("m1", "planning", "2026-10-18T10:00:00Z"),
    workspace_mode: "generations_controlled_mcp_read_v2",
    tool_mode: "controlled_mcp_read_v2",
    stage: "awaiting_tool_approval",
  } as Mission;
  expect(missionStateName(row)).toBe("等待工具审批");
  expect(decisionKind(row)).toBe("mcp_call");
  expect(isMissionInProgress(row)).toBe(false);
});

it.each(["infrastructure_failed", "outcome_unknown"])(
  "labels a C4 %s baseline as needing handling",
  (resultClass) => {
    const row: Mission = {
      ...c4Mission,
      state: "draft",
      c4_operations: [
        {
          ...c4Operation,
          result: { ...c4Operation.result!, class: resultClass },
          cleanup: [{ ...c4Operation.cleanup![0], state: "failed" }],
        },
      ],
    };
    expect(missionStateName(row)).toBe("C4 需要处理");
  },
);

it("keeps the legacy draft state label unchanged", () => {
  expect(
    missionStateName(complete("legacy", "draft", "2026-10-18T10:00:00Z")),
  ).toBe("待授权上下文");
});
