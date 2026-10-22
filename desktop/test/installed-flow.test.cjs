const { test } = require("node:test");
const assert = require("node:assert/strict");
const {
  assertFlowEvidence,
  assertDeliveryArtifact,
} = require("../installed-flow.cjs");
const crypto = require("node:crypto");

test("short delivery diagnostic identifies route and seal without content or paths", () => {
  const bytes = Buffer.from("PRIVATE_CONTENT_SENTINEL");
  const seal = "a".repeat(64);
  for (const route of ["patch", "report"]) {
    assert.throws(
      () =>
        assertDeliveryArtifact(route, bytes, {
          [route + "_sha256"]: seal,
          patch_path: "PRIVATE_PATH_SENTINEL",
        }),
      (error) => {
        const diagnostic = JSON.parse(error.message);
        assert.deepEqual(diagnostic, {
          artifact: route,
          bytes: bytes.length,
          sha256: crypto.createHash("sha256").update(bytes).digest("hex"),
          expected_sha256: seal,
        });
        assert.doesNotMatch(error.message, /PRIVATE_/);
        return true;
      },
    );
  }
  assert.throws(() => assertDeliveryArtifact("patch", Buffer.alloc(100), {}));
  assert.doesNotThrow(() =>
    assertDeliveryArtifact("patch", Buffer.alloc(101), {}),
  );
});
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
