import { decisionKind, type DecisionKind } from "./deliveryStatus";
import type { Mission, Review } from "./types";
import { c4OperationLabel, c4Supported, hasC4 } from "./c4";
import {
  verificationOutcome,
  verificationSourceLabel,
} from "./verificationSource";

export type DecisionTargetView = "总览" | "计划链路" | "验证";
export interface DecisionFact {
  label: string;
  value: string;
}
export interface DecisionMaterial {
  kind: DecisionKind;
  actionLabel: string;
  view: DecisionTargetView;
  facts: DecisionFact[];
}

const count = (value: unknown[] | null | undefined, suffix: string) =>
  Array.isArray(value) ? `${value.length} ${suffix}` : "未记录";
const recorded = (value: unknown) =>
  typeof value === "string" && value.trim() ? value.trim() : "未记录";

function reviewOutcome(mission: Mission, role: "qa" | "auditor"): string {
  const digest = mission.candidate_digest?.trim();
  if (!digest) return "当前候选摘要缺失，无法匹配审查";
  const reviews = (mission.reviews ?? []).filter(
    (item: Review) =>
      item.role === role && item.candidate_digest === mission.candidate_digest,
  );
  if (!reviews.length) return "当前候选审查缺失";
  if (reviews.length === 1)
    return `当前候选：${reviews[0].passed ? "通过" : "未通过"}`;
  const passed = reviews.filter((item) => item.passed).length;
  return `当前候选 ${reviews.length} 条：${passed} 通过，${reviews.length - passed} 未通过`;
}

function contextMaterial(mission: Mission): DecisionMaterial {
  const planner = mission.profiles?.planner;
  const budget = mission.budget;
  const budgetRecorded =
    Number.isFinite(budget?.used_calls) && Number.isFinite(budget?.max_calls);
  return {
    kind: "context",
    actionLabel: "查看上下文",
    view: "总览",
    facts: [
      { label: "文件清单", value: count(mission.context_manifest, "个文件") },
      { label: "基线提交", value: recorded(mission.base_commit) },
      {
        label: "Planner 连接",
        value: planner
          ? `${planner.name} · ${planner.protocol} · ${planner.model}`
          : "未记录",
      },
      {
        label: "调用预算",
        value: budgetRecorded
          ? `已用 ${budget.used_calls} / 上限 ${budget.max_calls}`
          : "未记录",
      },
    ],
  };
}

function planMaterial(mission: Mission): DecisionMaterial {
  return {
    kind: "plan",
    actionLabel: "审阅计划",
    view: "计划链路",
    facts: [
      {
        label: "计划版本",
        value: mission.plan ? `r${mission.plan.revision}` : "未记录",
      },
      {
        label: "计划步骤",
        value: mission.plan ? count(mission.plan.steps, "步") : "未记录",
      },
      { label: "范围", value: count(mission.spec?.scope, "项") },
      { label: "验收命令", value: count(mission.spec?.acceptance, "条") },
    ],
  };
}

function mcpMaterial(mission: Mission): DecisionMaterial {
  const selection = mission.mcp_selection;
  return {
    kind: "mcp_call",
    actionLabel: "审阅本次工具调用",
    view: "总览",
    facts: [
      {
        label: "本地测试服务",
        value: selection?.test_only ? "合成参考数据（测试专用）" : "未记录",
      },
      {
        label: "冻结连接",
        value: selection
          ? `${selection.connection_id} · 配置 v${selection.connection_revision}`
          : "未记录",
      },
      {
        label: "当前阶段",
        value: "等待检查一次精确调用",
      },
      {
        label: "调用预算",
        value: selection ? `最多 ${selection.budget.max_calls} 次` : "未记录",
      },
    ],
  };
}

function deliveryMaterial(mission: Mission): DecisionMaterial {
  const latest = mission.evidence?.at(-1);
  const digest = mission.candidate_digest?.trim();
  const match = !digest
    ? "当前候选摘要缺失"
    : !latest
      ? "最新证据缺失"
      : latest.candidate_digest === mission.candidate_digest
        ? "匹配当前候选"
        : "不匹配（最新证据属于旧候选）";
  return {
    kind: "delivery",
    actionLabel: "审阅交付证据",
    view: "验证",
    facts: [
      {
        label: "最新验证",
        value: latest
          ? `${verificationOutcome(latest)} · ${verificationSourceLabel(latest)}`
          : "未记录",
      },
      { label: "当前候选匹配", value: match },
      { label: "QA 审查", value: reviewOutcome(mission, "qa") },
      { label: "Auditor 审查", value: reviewOutcome(mission, "auditor") },
    ],
  };
}

function attentionMaterial(mission: Mission): DecisionMaterial {
  const reason = mission.error?.trim();
  return {
    kind: "attention",
    actionLabel: "查看原因",
    view: "总览",
    facts: [
      {
        label: "原因",
        value: reason
          ? `${reason.slice(0, 160)}${reason.length > 160 ? "…" : ""}`
          : "未记录阻塞或暂停原因",
      },
    ],
  };
}

export function decisionMaterial(mission: Mission): DecisionMaterial {
  const kind = decisionKind(mission);
  if (kind === "c4_baseline" || (kind === "attention" && hasC4(mission))) {
    const baseline = mission.c4_operations
      ?.filter((o) => o.kind === "baseline")
      .at(-1);
    const candidate = mission.c4_operations
      ?.filter((o) => o.kind === "candidate")
      .at(-1);
    return {
      kind,
      actionLabel: kind === "attention" ? "检查 C4 停止原因" : "检查 C4 基线",
      view: "验证",
      facts: [
        ...(kind === "attention" ? attentionMaterial(mission).facts : []),
        ...(kind === "attention" && candidate
          ? [
              {
                label: "最新候选记录",
                value: c4OperationLabel(mission, candidate),
              },
              { label: "候选操作", value: candidate.id },
              { label: "候选材料", value: candidate.material_digest },
            ]
          : []),
        {
          label: "基线",
          value: !baseline
            ? "尚未准备材料"
            : c4OperationLabel(mission, baseline),
        },
        { label: "基线材料", value: baseline?.material_digest ?? "未记录" },
        {
          label: "来源",
          value: c4Supported(mission)
            ? "synthetic_stub · C4-C 未执行"
            : "来源未确认 · C4-C 未执行",
        },
      ],
    };
  }
  if (kind === "context") return contextMaterial(mission);
  if (kind === "mcp_call") return mcpMaterial(mission);
  if (kind === "plan") return planMaterial(mission);
  if (kind === "delivery") return deliveryMaterial(mission);
  return attentionMaterial(mission);
}
