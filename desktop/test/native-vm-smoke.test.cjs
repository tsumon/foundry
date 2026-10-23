"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs"),
  os = require("node:os"),
  path = require("node:path");
const {
  assertNativeVMReceipt,
  runNativeVMSmoke,
} = require("../native-vm-smoke.cjs");
const { sandboxFixture } = require("./sandbox-fixture.cjs");
const { writeManifest } = require("../build-support.cjs");
const {
  verifyResources,
  resourceDigest,
  executableName,
} = require("../runtime.cjs");
function success() {
  return {
    schema_version: 1,
    kind: "foundry-native-vm-acceptance",
    passed: true,
    stage: "complete",
    source: "embedded_vm_c4_control",
    models: "local_protocol_fixture/not_real_provider",
    host_platform: "windows/amd64",
    guest_platform: "linux/amd64",
    manifest_digest: "a".repeat(64),
    acceleration: "whpx",
    mission_id: "mission_fixture",
    protected_digests: {
      "go.mod": "4".repeat(64),
      "triple_test.go": "5".repeat(64),
    },
    source_digest: "6".repeat(64),
    patch_digest: "7".repeat(64),
    report_digest: "8".repeat(64),
    role_counts: {
      researcher: 1,
      planner: 1,
      worker: 1,
      debugger: 1,
      qa: 1,
      auditor: 1,
    },
    operations: [
      {
        kind: "baseline",
        class: "passed",
        tests: 2,
        instance_id: "b".repeat(64),
        cleanup_confirmed: true,
        candidate_digest: "1".repeat(64),
      },
      {
        kind: "candidate",
        class: "test_failed",
        tests: 2,
        instance_id: "c".repeat(64),
        cleanup_confirmed: true,
        candidate_digest: "2".repeat(64),
      },
      {
        kind: "candidate",
        class: "passed",
        tests: 2,
        instance_id: "d".repeat(64),
        cleanup_confirmed: true,
        candidate_digest: "3".repeat(64),
      },
    ],
    checks: {
      approval_gates: true,
      protected_unchanged: true,
      source_unchanged: true,
      delivery_frozen: true,
      exact_instances: true,
      local_model_only: true,
      qa_auditor_bound: true,
    },
  };
}
test("installed native gate admits only complete real VM fail/repair receipt bound to installed bytes", () => {
  assert.doesNotThrow(() =>
    assertNativeVMReceipt(success(), "a".repeat(64), "win32"),
  );
  for (const mutation of [
    (r) => (r.passed = false),
    (r) => (r.stage = "baseline"),
    (r) => (r.source = "synthetic_c4_control"),
    (r) => (r.models = "real_provider"),
    (r) => (r.manifest_digest = "0".repeat(64)),
    (r) => (r.host_platform = "darwin/arm64"),
    (r) => (r.acceleration = "tcg"),
    (r) => delete r.patch_digest,
    (r) => (r.protected_digests["triple_test.go"] = "invalid"),
    (r) => (r.protected_digests["private-path"] = "0".repeat(64)),
    (r) => (r.role_counts.debugger = 0),
    (r) => delete r.checks.approval_gates,
    (r) => (r.checks.qa_auditor_bound = false),
    (r) => r.operations.pop(),
    (r) => (r.operations[1].class = "passed"),
    (r) => (r.operations[0].tests = 0),
    (r) => (r.operations[2].cleanup_confirmed = false),
    (r) => (r.operations[2].instance_id = r.operations[1].instance_id),
    (r) =>
      (r.operations[2].candidate_digest = r.operations[1].candidate_digest),
    (r) => (r.operations[0].raw_output = "untrusted"),
    (r) => (r.extra = "untrusted"),
  ]) {
    const receipt = success();
    mutation(receipt);
    assert.throws(() =>
      assertNativeVMReceipt(receipt, "a".repeat(64), "win32"),
    );
  }
});
function installedFixture(t) {
  const root = fs.realpathSync(
    fs.mkdtempSync(path.join(os.tmpdir(), "foundry-native-vm-driver-")),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const resources = path.join(root, "resources");
  fs.mkdirSync(path.join(resources, "bin"), { recursive: true });
  fs.mkdirSync(path.join(resources, "web"));
  fs.writeFileSync(path.join(resources, "web/index.html"), "fixture");
  for (const name of ["foundry", "foundry-acceptance", "foundry-vm-acceptance"])
    fs.writeFileSync(
      path.join(resources, "bin", executableName(name)),
      "inert fixture, never executed",
    );
  sandboxFixture(path.join(resources, "sandbox"));
  writeManifest(resources);
  return {
    resources,
    dataDir: path.join(root, "private-acceptance"),
    reportFile: path.join(root, "native-vm-acceptance.json"),
    integrity: resourceDigest(path.join(resources, "resources-manifest.json")),
  };
}
test("installed resources cannot omit the native acceptance executable even with an otherwise valid manifest", (t) => {
  const options = installedFixture(t);
  fs.unlinkSync(
    path.join(
      options.resources,
      "bin",
      executableName("foundry-vm-acceptance"),
    ),
  );
  fs.unlinkSync(path.join(options.resources, "resources-manifest.json"));
  writeManifest(options.resources);
  assert.throws(
    () => verifyResources(options.resources),
    /Missing installed resources/,
  );
});
test("explicit native driver executes only the verified installed harness with bounded clean environment", (t) => {
  const options = installedFixture(t),
    receipt = success();
  receipt.manifest_digest = resourceDigest(
    path.join(options.resources, "sandbox/manifest.json"),
  );
  if (process.platform === "darwin") {
    receipt.host_platform = "darwin/arm64";
    receipt.guest_platform = "linux/arm64";
    receipt.acceleration = "virtualization_framework";
  }
  let calls = 0;
  const actual = runNativeVMSmoke(options, (binary, args, config) => {
    calls++;
    assert.equal(
      binary,
      path.join(
        options.resources,
        "bin",
        executableName("foundry-vm-acceptance"),
      ),
    );
    assert.deepEqual(args, [
      "--native-vm-acceptance",
      "--bundle",
      path.join(options.resources, "sandbox"),
      "--data-dir",
      options.dataDir,
    ]);
    assert.equal(config.timeout, 660000);
    assert.equal(config.maxBuffer, 65536);
    for (const key of ["OPENAI_API_KEY", "FOUNDRY_VM_BUNDLE", "NODE_OPTIONS"])
      assert.equal(config.env[key], undefined);
    return JSON.stringify(receipt);
  });
  assert.equal(calls, 1);
  assert.equal(actual.passed, true);
  assert.deepEqual(
    JSON.parse(fs.readFileSync(options.reportFile, "utf8")),
    receipt,
  );
  fs.writeFileSync(path.join(options.resources, "sandbox/kernel"), "tampered");
  assert.throws(
    () =>
      runNativeVMSmoke(options, () => {
        calls++;
      }),
    /integrity/,
  );
  assert.equal(calls, 1);
});
test("nonzero native guest result remains failed and retains only its bounded structured receipt", (t) => {
  const options = installedFixture(t),
    receipt = success();
  receipt.passed = false;
  receipt.stage = "readiness";
  receipt.failure_code = "readiness_failed";
  const raw = JSON.stringify(receipt) + "\n";
  assert.throws(() =>
    runNativeVMSmoke(options, () => {
      throw Object.assign(new Error("raw stderr must not be retained"), {
        stdout: raw,
      });
    }),
  );
  assert.equal(fs.readFileSync(options.reportFile, "utf8"), raw);
  fs.unlinkSync(options.reportFile);
  receipt.raw_stderr = "PRIVATE_SENTINEL";
  assert.throws(() =>
    runNativeVMSmoke(options, () => {
      throw Object.assign(new Error("failed"), {
        stdout: JSON.stringify(receipt),
      });
    }),
  );
  assert.equal(fs.existsSync(options.reportFile), false);
});
