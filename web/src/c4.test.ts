import { expect, it, vi } from "vitest";
import { api } from "./api";
import {
  c4BaselinePassed,
  c4CandidatePassed,
  c4OperationLabel,
  c4Passed,
  c4Supported,
} from "./c4";
import { mission } from "./test/fixtures";
import { c4Mission, c4Operation, c4Profile } from "./test/c4Fixtures";
import { decisionKind } from "./deliveryStatus";
import { decisionMaterial } from "./decisionMaterials";

it("sends only the typed action bindings to the task-scoped endpoint", async () => {
  const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ mission }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }),
  );
  try {
    await api.c4Action("task/one", {
      action: "cleanup",
      revision: 19,
      operation_id: "operation",
      material_digest: "material",
      container_id: "b".repeat(64),
    });
    expect(fetch).toHaveBeenCalledExactlyOnceWith(
      "/api/missions/task%2Fone/c4",
      {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          action: "cleanup",
          revision: 19,
          operation_id: "operation",
          material_digest: "material",
          container_id: "b".repeat(64),
        }),
      },
    );
  } finally {
    fetch.mockRestore();
  }
});

it("never promotes missing lifecycle, unknown results or failed/foreign cleanup to a pass", () => {
  expect(c4Passed(c4Operation)).toBe(true);
  for (const changed of [
    { started: undefined },
    { result: undefined },
    { cleanup: [] },
    { schema_version: 2 },
    { kind: "future_operation" },
    { result: { ...c4Operation.result!, class: "outcome_unknown" } },
    { cleanup: [{ ...c4Operation.cleanup![0], state: "failed" }] },
    { cleanup: [{ ...c4Operation.cleanup![0], owner: "foreign" }] },
    { owner: "", cleanup: [{ ...c4Operation.cleanup![0], owner: "" }] },
    { approval: { ...c4Operation.approval!, digest: "" } },
    { dispatch: { ...c4Operation.dispatch!, revision: 1 } },
    {
      started: {
        ...c4Operation.started!,
        at: "2026-10-20T23:59:59Z",
      },
    },
  ])
    expect(c4Passed({ ...c4Operation, ...changed })).toBe(false);
});

it.each([
  ["schema_version", { schema_version: 2 }],
  ["id", { id: "c4_go_v2" }],
  ["mode", { mode: "docker" }],
  ["image_id", { image_id: "golang:latest" }],
  ["platform", { platform: "linux/amd64" }],
  ["docker_path", { docker_path: "docker" }],
  ["endpoint", { endpoint: "tcp://127.0.0.1:2375" }],
  ["verifier", { verifier: "" }],
])("fails closed when the schema1 profile has an invalid %s", (_, change) => {
  const invalid = {
    ...c4Mission,
    spec: { ...c4Mission.spec, c4: { ...c4Profile, ...change } },
  };
  expect(c4Supported(invalid)).toBe(false);
  expect(c4BaselinePassed(invalid)).toBe(false);
  expect(c4OperationLabel(invalid, c4Operation)).toBe(
    "配置不支持 · 仅显示原始事实",
  );
});

it.each([
  ["missing id", { id: undefined as unknown as string }],
  ["id", { id: "" }],
  ["mission", { mission_id: "other" }],
  ["epoch", { epoch: c4Mission.epoch + 1 }],
  ["plan revision", { plan_revision: -1 }],
  ["base commit", { base_commit: "9".repeat(40) }],
  ["base digest", { base_digest: "8".repeat(64) }],
  ["profile digest", { profile_digest: "" }],
  ["material digest", { material_digest: "" }],
  ["container name", { container_name: "" }],
  [
    "missing container name",
    { container_name: undefined as unknown as string },
  ],
  ["owner", { owner: "" }],
  ["missing owner", { owner: undefined as unknown as string }],
  ["preparation timestamp", { prepared_at: "today" }],
  [
    "future stage revision",
    {
      dispatch: {
        ...c4Operation.dispatch!,
        revision: c4Mission.revision + 1,
      },
    },
  ],
])(
  "does not use a passing-shaped baseline with an invalid %s binding",
  (_, change) => {
    const operation = { ...c4Operation, ...change };
    const invalid = { ...c4Mission, c4_operations: [operation] };
    expect(c4BaselinePassed(invalid)).toBe(false);
    expect(c4OperationLabel(invalid, operation)).toBe(
      "配置不支持 · 仅显示原始事实",
    );
  },
);

it("routes failed baselines to attention with facts, not another approval invitation", () => {
  const failed = {
    ...c4Mission,
    state: "draft",
    c4_operations: [
      {
        ...c4Operation,
        result: { ...c4Operation.result!, class: "infrastructure_failed" },
        cleanup: [{ ...c4Operation.cleanup![0], state: "failed" }],
      },
    ],
  };
  expect(decisionKind(failed)).toBe("attention");
  expect(
    decisionMaterial(failed).facts.some((f) =>
      f.value.includes("infrastructure_failed"),
    ),
  ).toBe(true);
});

it("does not use a successful baseline as candidate delivery evidence", () => {
  const m = {
    ...c4Mission,
    candidate_digest: "2".repeat(64),
    c4_operations: [c4Operation],
  };
  expect(c4BaselinePassed(m)).toBe(true);
  expect(c4BaselinePassed({ ...m, base_commit: "3".repeat(40) })).toBe(false);
  expect(c4CandidatePassed(m)).toBe(false);
  const candidate = {
    ...c4Operation,
    id: "candidate-1",
    kind: "candidate",
    epoch: m.epoch,
    plan_revision: m.plan!.revision,
    candidate_digest: m.candidate_digest,
    candidate_generation: { id: "generation", digest: m.candidate_digest },
    approval: { ...c4Operation.approval!, digest: "4".repeat(64) },
  };
  const current = {
    ...m,
    c4_operations: [c4Operation, candidate],
    candidate_generation: candidate.candidate_generation,
    approvals: [
      ...m.approvals,
      {
        kind: "execute",
        plan_revision: candidate.plan_revision,
        digest: candidate.approval.digest,
        revision: candidate.approval.revision,
        created_at: candidate.approval.at,
      },
    ],
  };
  expect(c4CandidatePassed({ ...current, approvals: m.approvals })).toBe(false);
  expect(c4CandidatePassed(current)).toBe(true);
  expect(
    c4CandidatePassed({
      ...current,
      c4_operations: [
        candidate,
        { ...candidate, id: "candidate-2", result: undefined },
      ],
    }),
  ).toBe(false);
});
