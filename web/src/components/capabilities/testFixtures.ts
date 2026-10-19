import { vi } from "vitest";
import { response } from "../../test/fixtures";

export const connection = {
  id: "mcp-1",
  revision: 2,
  name: "本机发现 fixture",
  endpoint: "http://127.0.0.1:9998/mcp",
  protocol: "2025-11-25",
  enabled: true,
  has_credential: true,
  credential_revision: 1,
  created_at: "2026-10-18T01:00:00Z",
  updated_at: "2026-10-18T02:00:00Z",
};
export const budget = {
  deadline_seconds: 15,
  request_timeout_seconds: 5,
  cleanup_timeout_seconds: 1,
  max_requests: 13,
  max_pages: 10,
  max_response_bytes: 1048576,
  max_total_response_bytes: 4194304,
  max_event_bytes: 1048576,
  max_notifications: 64,
  max_tools: 500,
  max_schema_bytes: 65536,
  max_json_depth: 32,
  max_cursor_bytes: 4096,
};
export const operation = {
  id: "request-original",
  connection_id: "mcp-1",
  connection_revision: 2,
  endpoint: connection.endpoint,
  credential_revision: 1,
  consent: true,
  authorized_at: "2026-10-18T03:00:00Z",
  status: "outcome_unknown",
  budget,
  requests: null,
  response_bytes: null,
  snapshot_id: "",
  error_code: "interrupted",
  created_at: "2026-10-18T03:00:00Z",
  finished_at: "2026-10-18T03:01:00Z",
};
export const toolSnapshot = {
  id: "snapshot-old",
  connection_id: "mcp-1",
  connection_revision: 1,
  protocol: "2025-11-25",
  server_name: "fixture",
  server_version: "1.0",
  tools: [
    {
      name: "read-only-description",
      title: "文本定义",
      description: "<img src=x onerror=alert(1)>",
      input_schema: { type: "object" },
      output_schema: null,
      annotations: null,
      input_schema_json: '{"type":"object"}',
      definition_digest: "tool-digest",
    },
  ],
  digest: "snapshot-digest",
  observed_at: "2026-10-18T01:10:00Z",
};
export const skill = {
  id: "skill-1",
  revision: 1,
  name: "review-source",
  description: "Review source",
  source: "显式粘贴",
  files: [
    {
      path: "SKILL.md",
      content:
        "---\nname: review-source\ndescription: Review source\n---\n<script>approve()</script>\n完整旧正文",
    },
    {
      path: "references/rules.md",
      content: "不得执行 shell 或自动获取 https://example.invalid",
    },
  ],
  digest: "skill-digest-1",
  created_at: "2026-10-18T01:00:00Z",
};
export const repository = {
  id: "git-1",
  revision: 2,
  name: "临时仓库",
  path: "/temporary/fixture",
  project_id: "",
  created_at: "2026-10-18T01:00:00Z",
  updated_at: "2026-10-18T02:00:00Z",
};
export const observation = {
  id: "git-observation",
  repository_id: "git-1",
  repository_revision: 1,
  root: "/temporary/fixture",
  branch: "main",
  head: "abc123",
  detached: false,
  unborn: false,
  checked_at: "2026-10-18T01:10:00Z",
  status: "succeeded",
  error_code: "",
  working_tree_status: "not_checked",
};
export const emptyCapabilities = {
  connections: [],
  snapshots: [],
  discoveries: [],
  skills: [],
  repositories: [],
  git_observations: [],
  builtin_capabilities: [],
  tool_execution: false,
  skill_execution: false,
  git_write: false,
};
export const catalogState = {
  snapshot: null,
  ready: false,
  loading: false,
  error: "",
};
export function mockServer(
  snapshot: object = emptyCapabilities,
  handler?: (
    path: string,
    init?: RequestInit,
  ) => Promise<Response> | Response | undefined,
) {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    const handled = handler?.(path, init);
    if (handled) return handled;
    if (path === "/api/profiles")
      return response({
        profiles: [],
        settings: { default_profile_id: "", role_bindings: {} },
      });
    if (path === "/api/model-routes")
      return response({
        routes: [],
        runtime: {
          live_authorization: false,
          live_discovery: false,
          live_inference: false,
          simulation_inference: false,
          simulation_authorization: false,
          automatic_paid_fallback: false,
          adapters: [],
        },
      });
    if (path === "/api/capabilities") return response(snapshot);
    if (path === "/api/mcp-local-test")
      return response({
        enabled: false,
        eligible: false,
        reason: "controller-owned local test service disabled",
        test_only: true,
        budget: {
          max_calls: 3,
          max_argument_bytes: 8192,
          max_result_bytes: 65536,
          max_total_result_bytes: 196608,
          deadline_seconds: 30,
          max_requests: 14,
          max_pages: 10,
          max_tools: 500,
          max_response_bytes: 1048576,
          max_total_response_bytes: 4194304,
          max_event_bytes: 1048576,
          max_notifications: 64,
          request_timeout_seconds: 5,
          cleanup_timeout_seconds: 1,
        },
      });
    throw new Error(`Unexpected test request: ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
export function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
