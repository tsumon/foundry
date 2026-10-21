import type { Verification } from "./types";

export function verificationSource(evidence?: Verification): string {
  if (evidence?.source_version === 1) {
    switch (evidence.source_kind) {
      case "synthetic_stub":
      case "synthetic_content_check":
      case "isolated_execution":
        return evidence.source_kind;
    }
  }
  return "unknown";
}

export function verificationSourceLabel(evidence?: Verification): string {
  switch (verificationSource(evidence)) {
    case "synthetic_stub":
      return "流程模拟（未执行命令）";
    case "synthetic_content_check":
      return "内容检查模拟（未执行命令）";
    case "isolated_execution":
      return "隔离命令执行";
    default:
      return "来源未记录";
  }
}

export function verificationOutcome(evidence: Verification): string {
  const source = verificationSource(evidence);
  const prefix =
    source === "isolated_execution"
      ? "验收命令"
      : source.startsWith("synthetic_")
        ? "模拟验证"
        : "验证记录";
  return `${prefix}${evidence.passed ? "通过" : "未通过"}`;
}
