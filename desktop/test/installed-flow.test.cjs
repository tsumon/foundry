const { test } = require("node:test");
const assert = require("node:assert/strict");
const { assertFlowEvidence } = require("../installed-flow.cjs");
const evidence = {
  state: "awaiting_delivery",
  delivery: null,
  c4_operations: [
    {},
    { result: { class: "test_failed" }, candidate_digest: "first" },
    { result: { class: "passed" }, candidate_digest: "second" },
  ],
  evidence: [
    { passed: false, source_kind: "synthetic_stub" },
    { passed: true, source_kind: "synthetic_stub" },
  ],
  reviews: [
    { passed: true, binding_digest: "qa", checks: [{}] },
    { passed: true, binding_digest: "audit", checks: [{}] },
  ],
};
test("installed acceptance requires real failure/repair records labeled synthetic", () => {
  assert.doesNotThrow(() => assertFlowEvidence(evidence));
  for (const change of [
    (m) => (m.evidence[0].source_kind = "unknown"),
    (m) => (m.evidence[0].passed = true),
    (m) => (m.c4_operations[2].candidate_digest = "first"),
    (m) => (m.reviews[0].binding_digest = ""),
    (m) => (m.delivery = {}),
  ]) {
    const altered = structuredClone(evidence);
    change(altered);
    assert.throws(() => assertFlowEvidence(altered));
  }
});
