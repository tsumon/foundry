import { expect, it, vi } from "vitest";
import { api } from "./api";
import {
  c4BaselinePassed,
  c4CandidatePassed,
  c4OperationLabel,
  c4Passed,
  c4Supported,
  c4ReadinessFresh,
  type C4Control,
} from "./c4";
import { mission } from "./test/fixtures";
import {
  c4CandidateOperation,
  c4Mission,
  c4Operation,
  c4Profile,
} from "./test/c4Fixtures";
import { decisionKind } from "./deliveryStatus";
import { decisionMaterial } from "./decisionMaterials";

it("requires current activation digest and expiry for fresh readiness, not historical pass", () => {
  const at = Date.parse("2026-10-22T00:00:00Z");
  const expires = "2026-10-22T00:10:00Z";
  const receipt: C4Control = {
    schema_version: 1,
    id: "ready",
    kind: "readiness",
    mission_id: c4Mission.id,
    epoch: c4Mission.epoch,
    plan_revision: 0,
    base_commit: c4Mission.base_commit,
    base_digest: c4Mission.repository_import!.base_digest,
    profile_digest: "c".repeat(64),
    activation_digest: "f".repeat(64),
    activation_expires_at: expires,
    source_kind: "synthetic_c4_control",
    prepared_revision: 1,
    prepared_at: "2026-10-22T00:00:00Z",
    expires_at: expires,
    material_digest: "a".repeat(64),
    state: "completed",
    approval: {
      digest: "a".repeat(64),
      revision: 2,
      at: "2026-10-22T00:00:01Z",
    },
    dispatch: { revision: 3, at: "2026-10-22T00:00:02Z" },
    result: {
      status: "ready",
      revision: 4,
      at: "2026-10-22T00:00:03Z",
      valid_until: expires,
    },
  };
  const m = {
    ...c4Mission,
    spec: {
      ...c4Mission.spec,
      c4: { ...c4Mission.spec.c4!, control_version: 1 },
    },
    c4_controls: [receipt],
    c4_operations: [
      {
        ...c4Operation,
        readiness_id: "ready",
        readiness_digest: receipt.material_digest,
      },
    ],
  };
  const current = {
    available: true,
    structurally_available: true,
    expired: false,
    source_kind: "synthetic_c4_control",
    activation_digest: "f".repeat(64),
    expires_at: expires,
  };
  expect(c4ReadinessFresh(m, at, current)).toBe(true);
  expect(
    c4ReadinessFresh(m, at, { ...current, activation_digest: "b".repeat(64) }),
  ).toBe(false);
  expect(
    c4ReadinessFresh(m, at, { ...current, expires_at: "2026-10-22T00:20:00Z" }),
  ).toBe(false);
  expect(c4ReadinessFresh(m, at)).toBe(false);
  expect(c4BaselinePassed(m)).toBe(true);
});

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

const matchingCandidateApproval = {
  kind: "execute",
  plan_revision: c4CandidateOperation.plan_revision,
  digest: c4CandidateOperation.approval!.digest,
  revision: c4CandidateOperation.approval!.revision,
  created_at: c4CandidateOperation.approval!.at,
};

const currentCandidateMission = {
  ...c4Mission,
  candidate_digest: c4CandidateOperation.candidate_digest!,
  candidate_generation: c4CandidateOperation.candidate_generation!,
  c4_operations: [c4Operation, c4CandidateOperation],
  approvals: [...c4Mission.approvals, matchingCandidateApproval],
};

it.each([
  ["missing execute approval", null],
  ["wrong approval kind", { kind: "context" }],
  [
    "wrong plan revision",
    { plan_revision: matchingCandidateApproval.plan_revision + 1 },
  ],
  ["wrong digest", { digest: "5".repeat(64) }],
  [
    "wrong approval revision",
    { revision: matchingCandidateApproval.revision + 1 },
  ],
  ["wrong approval timestamp", { created_at: "2026-10-21T00:00:00Z" }],
])("rejects a passing candidate with %s", (_, change) => {
  const approvals = change
    ? [...c4Mission.approvals, { ...matchingCandidateApproval, ...change }]
    : c4Mission.approvals;
  const invalid = { ...currentCandidateMission, approvals };

  expect(c4CandidatePassed(invalid)).toBe(false);
  expect(c4OperationLabel(invalid, c4CandidateOperation)).toBe(
    "审批绑定缺失或不匹配 · 不能确认通过",
  );
});

it("keeps matching current and historical candidate results distinct", () => {
  expect(c4CandidatePassed(currentCandidateMission)).toBe(true);
  expect(c4OperationLabel(currentCandidateMission, c4CandidateOperation)).toBe(
    "模拟验证通过 · 非真实执行",
  );

  const historicalMission = {
    ...currentCandidateMission,
    epoch: currentCandidateMission.epoch + 1,
    plan: {
      ...currentCandidateMission.plan!,
      revision: currentCandidateMission.plan!.revision + 1,
    },
    candidate_digest: "6".repeat(64),
    candidate_generation: {
      id: "generation-2",
      digest: "6".repeat(64),
    },
  };
  expect(c4OperationLabel(historicalMission, c4CandidateOperation)).toBe(
    "模拟验证通过 · 非真实执行",
  );
  expect(c4CandidatePassed(historicalMission)).toBe(false);
});
