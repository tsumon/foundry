import { expect, it, vi } from "vitest";
import { api } from "./api";
import { c4Passed, c4BaselinePassed, c4CandidatePassed } from "./c4";
import { mission } from "./test/fixtures";
import { c4Operation } from "./test/c4Fixtures";
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
  ])
    expect(c4Passed({ ...c4Operation, ...changed })).toBe(false);
});

it("routes failed baselines to attention with facts, not another approval invitation", () => {
  const failed = {
    ...mission,
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
    ...mission,
    base_commit: "base",
    candidate_digest: "current",
    c4_operations: [c4Operation],
  };
  expect(c4BaselinePassed(m)).toBe(true);
  expect(c4BaselinePassed({ ...m, base_commit: "different" })).toBe(false);
  expect(c4CandidatePassed(m)).toBe(false);
  const candidate = {
    ...c4Operation,
    id: "candidate-1",
    kind: "candidate",
    epoch: m.epoch,
    plan_revision: m.plan!.revision,
    candidate_digest: m.candidate_digest,
    candidate_generation: { id: "generation", digest: m.candidate_digest },
  };
  expect(
    c4CandidatePassed({
      ...m,
      c4_operations: [c4Operation, candidate],
      candidate_generation: candidate.candidate_generation,
    }),
  ).toBe(true);
  expect(
    c4CandidatePassed({
      ...m,
      c4_operations: [
        candidate,
        { ...candidate, id: "candidate-2", result: undefined },
      ],
    }),
  ).toBe(false);
});
