import type { Mission, Profile } from "../types";
export const profile: Profile = {
  id: "p1",
  name: "测试连接",
  kind: "direct",
  protocol: "responses",
  base_url: "https://models.example/v1",
  model: "fixture-model",
  has_key: true,
};
export const mission: Mission = {
  id: "m1",
  title: "改进导入校验",
  goal: "检查已有实现并改进错误说明",
  repo_path: "/tmp/fixture",
  base_commit: "abc123",
  state: "awaiting_approval",
  stage: "plan",
  revision: 9,
  epoch: 1,
  spec: {
    scope: ["src/"],
    protected: ["tests/"],
    acceptance: ["npm test"],
    image: "fixture:local",
    image_id: "sha256:image",
    profile_id: "p1",
    research: true,
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
  context_manifest: [
    { path: "src/import.ts", bytes: 81, sha256: "source-sha" },
  ],
  research: {
    summary: "已有解析入口",
    findings: [
      { severity: "info", summary: "校验位于解析器", paths: ["src/import.ts"] },
    ],
    unknowns: ["尚未确认编码边界"],
  },
  plan: {
    revision: 3,
    summary: "先验证，再调整",
    steps: [
      {
        id: "s1",
        title: "检查校验边界",
        description: "保留既有接口",
        depends_on: [],
        files: ["src/import.ts"],
        status: "pending",
      },
    ],
  },
  messages: [],
  changes: [],
  evidence: [],
  reviews: [],
  approvals: [
    {
      kind: "context",
      digest: "context-digest",
      created_at: "2026-10-18T00:00:00Z",
      revision: 2,
    },
  ],
  budget: {
    max_calls: 16,
    used_calls: 2,
    input_tokens: 0,
    output_tokens: 0,
    usage_unknown: true,
  },
  candidate_digest: "",
  error: "",
  created_at: "2026-10-18T00:00:00Z",
  updated_at: "2026-10-18T00:00:00Z",
  delivery: null,
};
export function response(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
