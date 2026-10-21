import { describe, expect, it } from "vitest";
import { mission } from "./test/fixtures";
import type { Mission, Review, Verification } from "./types";
import { decisionMaterial } from "./decisionMaterials";

const evidence = (candidate_digest: string, passed: boolean): Verification => ({
  id: `e-${candidate_digest}`,
  candidate_digest,
  image_id: "sha256:image",
  created_at: "2026-10-18T01:00:00Z",
  acceptance_revision: 3,
  results: [],
  passed,
});
const review = (
  role: "qa" | "auditor",
  candidate_digest: string,
  passed: boolean,
): Review => ({
  role,
  candidate_digest,
  passed,
  summary: "fixture",
  findings: [],
});
const values = (value: Mission) =>
  Object.fromEntries(
    decisionMaterial(value).facts.map((fact) => [fact.label, fact.value]),
  );

describe("decisionMaterial", () => {
  it("derives context facts from the frozen mission without filling missing fields", () => {
    const material = decisionMaterial({ ...mission, state: "draft" });
    expect(material.actionLabel).toBe("查看上下文");
    expect(material.view).toBe("总览");
    expect(values({ ...mission, state: "draft" })).toEqual({
      文件清单: "1 个文件",
      基线提交: "abc123",
      "Planner 连接": "测试连接 · responses · fixture-model",
      调用预算: "已用 2 / 上限 16",
    });
    expect(
      values({
        ...mission,
        state: "draft",
        context_manifest: null as unknown as Mission["context_manifest"],
        base_commit: "",
        profiles: {} as Mission["profiles"],
        budget: {} as Mission["budget"],
      }),
    ).toEqual({
      文件清单: "未记录",
      基线提交: "未记录",
      "Planner 连接": "未记录",
      调用预算: "未记录",
    });
  });

  it("derives plan counts and distinguishes an unavailable plan from empty arrays", () => {
    const planned = { ...mission, state: "awaiting_approval" };
    expect(decisionMaterial(planned).actionLabel).toBe("审阅计划");
    expect(decisionMaterial(planned).view).toBe("计划链路");
    expect(values(planned)).toEqual({
      计划版本: "r3",
      计划步骤: "1 步",
      范围: "1 项",
      验收命令: "1 条",
    });
    expect(values({ ...planned, plan: null })).toEqual({
      计划版本: "未记录",
      计划步骤: "未记录",
      范围: "1 项",
      验收命令: "1 条",
    });
    expect(
      values({
        ...planned,
        plan: { revision: 4, summary: "empty", steps: [] },
        spec: { ...mission.spec, scope: [], acceptance: [] },
      }),
    ).toEqual({
      计划版本: "r4",
      计划步骤: "0 步",
      范围: "0 项",
      验收命令: "0 条",
    });
  });

  it("reports the latest evidence separately from current candidate review outcomes", () => {
    const delivery = {
      ...mission,
      state: "awaiting_delivery",
      candidate_digest: "current",
      evidence: [evidence("current", false), evidence("old", true)],
      reviews: [
        review("qa", "old", true),
        review("qa", "current", true),
        review("qa", "current", false),
        review("auditor", "current", false),
      ],
    };
    expect(decisionMaterial(delivery).actionLabel).toBe("审阅交付证据");
    expect(decisionMaterial(delivery).view).toBe("验证");
    expect(values(delivery)).toEqual({
      最新验证: "验证记录通过 · 来源未记录",
      当前候选匹配: "不匹配（最新证据属于旧候选）",
      "QA 审查": "当前候选 2 条：1 通过，1 未通过",
      "Auditor 审查": "当前候选：未通过",
    });
  });

  it("does not turn passed evidence with stale or missing reviews into current readiness", () => {
    const stale = {
      ...mission,
      state: "awaiting_delivery",
      candidate_digest: "current",
      evidence: [evidence("current", true)],
      reviews: [review("qa", "old", true)],
    };
    expect(values(stale)).toEqual({
      最新验证: "验证记录通过 · 来源未记录",
      当前候选匹配: "匹配当前候选",
      "QA 审查": "当前候选审查缺失",
      "Auditor 审查": "当前候选审查缺失",
    });
    expect(
      values({
        ...stale,
        candidate_digest: "",
        evidence: [],
        reviews: [],
      }),
    ).toEqual({
      最新验证: "未记录",
      当前候选匹配: "当前候选摘要缺失",
      "QA 审查": "当前候选摘要缺失，无法匹配审查",
      "Auditor 审查": "当前候选摘要缺失，无法匹配审查",
    });
  });

  it("bounds the actual attention reason and states when none was recorded", () => {
    const attention = { ...mission, state: "blocked", error: "x".repeat(200) };
    const material = decisionMaterial(attention);
    expect(material.actionLabel).toBe("查看原因");
    expect(material.view).toBe("总览");
    expect(values(attention).原因).toBe(`${"x".repeat(160)}…`);
    expect(values({ ...attention, error: "" })).toEqual({
      原因: "未记录阻塞或暂停原因",
    });
  });
});

it("keeps the latest source explicit when the same candidate has mixed sources", () => {
  const rows = [
    {
      ...evidence("same", true),
      source_version: 1,
      source_kind: "isolated_execution",
    },
    {
      ...evidence("same", false),
      source_version: 1,
      source_kind: "synthetic_content_check",
    },
  ];
  expect(
    values({
      ...mission,
      state: "awaiting_delivery",
      candidate_digest: "same",
      evidence: rows,
    }).最新验证,
  ).toBe("模拟验证未通过 · 内容检查模拟（未执行命令）");
  rows[1].passed = true;
  expect(
    values({ ...mission, state: "awaiting_delivery", evidence: rows }).最新验证,
  ).toBe("模拟验证通过 · 内容检查模拟（未执行命令）");
  rows[1].source_version = 2;
  expect(
    values({ ...mission, state: "awaiting_delivery", evidence: rows }).最新验证,
  ).toBe("验证记录通过 · 来源未记录");
});
