import type { Mission, Profile } from "../types";
import { MissionWorkspace } from "./MissionWorkspace";
const connection: Profile = {
  id: "demo-only",
  name: "演示连接（未配置）",
  kind: "compatible",
  protocol: "responses",
  base_url: "https://example.invalid/v1",
  model: "示意模型 · 无实际调用",
  has_key: false,
};
const demo: Mission = {
  id: "demonstration-only",
  title: "客户数据导入",
  goal: "从 CSV 文件导入客户数据，完成校验，保留清晰可追溯的错误说明。",
  repo_path: "演示仓库 / sample-project",
  base_commit: "演示基线 · 非真实提交",
  state: "awaiting_approval",
  stage: "plan",
  revision: 4,
  epoch: 0,
  spec: {
    scope: ["src/import/"],
    protected: ["tests/"],
    acceptance: ["npm run test:import"],
    image: "演示镜像（未运行）",
    image_id: "",
    profile_id: "demo-only",
    research: true,
    max_calls: 16,
  },
  profiles: {
    main: connection,
    researcher: connection,
    planner: connection,
    worker: connection,
    debugger: connection,
    qa: connection,
    auditor: connection,
  },
  context_manifest: [
    { path: "src/import/parser.ts", bytes: 1240, sha256: "演示摘要" },
    { path: "tests/import.test.ts", bytes: 840, sha256: "演示摘要" },
  ],
  research: {
    summary: "梳理现有数据结构、字段规范和导入约束，为实现计划提供依据。",
    findings: [
      {
        severity: "info",
        summary: "示例解析器已有统一入口，可复用字段映射逻辑。",
        paths: ["src/import/parser.ts"],
      },
      {
        severity: "medium",
        summary: "示例验收文件需要保持受保护，避免实现自行降低标准。",
        paths: ["tests/import.test.ts"],
      },
    ],
    unknowns: ["实际文件规模和编码边界尚待确认。"],
  },
  plan: {
    revision: 1,
    summary: "从输入契约开始，依次实现解析与错误呈现；完成后检查真实命令证据。",
    steps: [
      {
        id: "define",
        title: "定义导入契约",
        description: "明确输入格式、字段映射与校验规则。",
        depends_on: [],
        files: ["src/import/parser.ts"],
        status: "pending",
      },
      {
        id: "parse",
        title: "实现导入解析",
        description: "解析 CSV，并为无效行提供可追溯的错误信息。",
        depends_on: ["define"],
        files: ["src/import/parser.ts"],
        status: "pending",
      },
      {
        id: "errors",
        title: "呈现校验结果",
        description: "让用户理解失败原因，保留原始行号。",
        depends_on: ["parse"],
        files: ["src/import/results.ts"],
        status: "pending",
      },
    ],
  },
  messages: [],
  changes: [],
  evidence: [],
  reviews: [],
  approvals: [],
  budget: {
    max_calls: 16,
    used_calls: 0,
    input_tokens: 0,
    output_tokens: 0,
    usage_unknown: true,
  },
  candidate_digest: "",
  error: "",
  created_at: "",
  updated_at: "",
  delivery: null,
};
export function DemoWorkspace({ onClose }: { onClose: () => void }) {
  return (
    <div className="demo-wrapper">
      <div className="demo-banner">
        <span>演示数据 · 仅供界面预览，不连接模型或执行任务</span>
        <button onClick={onClose}>退出演示</button>
      </div>
      <MissionWorkspace mission={demo} onUpdate={() => {}} demo />
    </div>
  );
}
