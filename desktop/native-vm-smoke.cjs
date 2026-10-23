"use strict";
// Explicit acceptance driver only. Ordinary installed startup never imports it.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { execFileSync } = require("node:child_process");
const {
  verifyResources,
  resourceDigest,
  childEnvironment,
  executableName,
} = require("./runtime.cjs");
const hash = /^[a-f0-9]{64}$/;
const receiptKeys = [
  "schema_version",
  "kind",
  "passed",
  "stage",
  "source",
  "models",
  "host_platform",
  "guest_platform",
  "manifest_digest",
  "acceleration",
  "mission_id",
  "role_counts",
  "operations",
  "checks",
];
const roles = ["researcher", "planner", "worker", "debugger", "qa", "auditor"];
const digests = ["source_digest", "patch_digest", "report_digest"];
const optionalFields = ["failure_code", "protected_digests", ...digests];
const checks = [
  "approval_gates",
  "protected_unchanged",
  "source_unchanged",
  "delivery_frozen",
  "exact_instances",
  "local_model_only",
  "qa_auditor_bound",
];
function keys(value, expected) {
  assert.ok(value && typeof value === "object" && !Array.isArray(value));
  assert.deepEqual(Object.keys(value).sort(), [...expected].sort());
}
function assertNativeVMReceipt(
  receipt,
  manifestDigest,
  platform = process.platform,
) {
  assert.ok(["darwin", "win32"].includes(platform));
  keys(receipt, [...receiptKeys, "protected_digests", ...digests]);
  assert.equal(receipt.schema_version, 1);
  assert.equal(receipt.kind, "foundry-native-vm-acceptance");
  assert.equal(receipt.passed, true);
  assert.equal(receipt.stage, "complete");
  assert.equal(receipt.source, "embedded_vm_c4_control");
  assert.equal(receipt.models, "local_protocol_fixture/not_real_provider");
  assert.match(manifestDigest, hash);
  assert.equal(receipt.manifest_digest, manifestDigest);
  assert.equal(
    receipt.host_platform,
    platform === "win32" ? "windows/amd64" : "darwin/arm64",
  );
  assert.equal(
    receipt.guest_platform,
    platform === "win32" ? "linux/amd64" : "linux/arm64",
  );
  assert.equal(
    receipt.acceleration,
    platform === "win32" ? "whpx" : "virtualization_framework",
  );
  assert.match(receipt.mission_id, /^[A-Za-z0-9_-]{1,128}$/);
  keys(receipt.protected_digests, ["go.mod", "triple_test.go"]);
  for (const digest of Object.values(receipt.protected_digests))
    assert.match(digest, hash);
  for (const field of digests) assert.match(receipt[field], hash);
  keys(receipt.role_counts, roles);
  for (const role of roles) assert.equal(receipt.role_counts[role], 1);
  keys(receipt.checks, checks);
  for (const check of checks) assert.equal(receipt.checks[check], true);
  assert.ok(Array.isArray(receipt.operations));
  assert.equal(receipt.operations.length, 3);
  const expected = [
    ["baseline", "passed"],
    ["candidate", "test_failed"],
    ["candidate", "passed"],
  ];
  receipt.operations.forEach((operation, index) => {
    keys(operation, [
      "kind",
      "class",
      "tests",
      "instance_id",
      "cleanup_confirmed",
      "candidate_digest",
    ]);
    assert.equal(operation.kind, expected[index][0]);
    assert.equal(operation.class, expected[index][1]);
    assert.equal(operation.tests, 2);
    assert.equal(operation.cleanup_confirmed, true);
    assert.match(operation.instance_id, hash);
    assert.match(operation.candidate_digest, hash);
  });
  assert.equal(new Set(receipt.operations.map((o) => o.instance_id)).size, 3);
  assert.notEqual(
    receipt.operations[1].candidate_digest,
    receipt.operations[2].candidate_digest,
  );
}
function boundedReceipt(raw) {
  assert.ok(typeof raw === "string" && Buffer.byteLength(raw) <= 64 * 1024);
  const receipt = JSON.parse(raw);
  keys(receipt, [
    ...receiptKeys,
    ...optionalFields.filter((field) => Object.hasOwn(receipt, field)),
  ]);
  assert.equal(receipt.schema_version, 1);
  assert.equal(receipt.kind, "foundry-native-vm-acceptance");
  assert.equal(typeof receipt.passed, "boolean");
  for (const key of ["stage", "failure_code"])
    if (receipt[key] !== undefined)
      assert.match(receipt[key], /^[a-z_]{1,64}$/);
  assert.equal(receipt.source, "embedded_vm_c4_control");
  assert.equal(receipt.models, "local_protocol_fixture/not_real_provider");
  for (const [key, allowed] of Object.entries({
    host_platform: ["", "darwin/arm64", "windows/amd64"],
    guest_platform: ["", "linux/arm64", "linux/amd64"],
    acceleration: ["", "virtualization_framework", "whpx"],
  }))
    assert.ok(allowed.includes(receipt[key]));
  assert.match(receipt.manifest_digest, /^(?:[a-f0-9]{64})?$/);
  assert.match(receipt.mission_id, /^[A-Za-z0-9_-]{0,128}$/);
  for (const field of digests)
    if (Object.hasOwn(receipt, field)) assert.match(receipt[field], hash);
  if (Object.hasOwn(receipt, "protected_digests")) {
    keys(receipt.protected_digests, ["go.mod", "triple_test.go"]);
    for (const digest of Object.values(receipt.protected_digests))
      assert.match(digest, hash);
  }
  for (const [field, allowed] of [
    ["role_counts", roles],
    ["checks", checks],
  ]) {
    const object = receipt[field];
    if (object === null) continue;
    assert.ok(object && typeof object === "object" && !Array.isArray(object));
    for (const [key, value] of Object.entries(object)) {
      assert.ok(allowed.includes(key));
      if (field === "checks") assert.equal(typeof value, "boolean");
      else assert.ok(Number.isSafeInteger(value) && value >= 0 && value <= 16);
    }
  }
  assert.ok(
    receipt.operations === null ||
      (Array.isArray(receipt.operations) && receipt.operations.length <= 3),
  );
  for (const op of receipt.operations || []) {
    keys(op, [
      "kind",
      "class",
      "tests",
      "instance_id",
      "cleanup_confirmed",
      "candidate_digest",
    ]);
    assert.ok(["baseline", "candidate"].includes(op.kind));
    assert.match(op.class, /^[a-z_]{1,64}$/);
    assert.ok(
      Number.isSafeInteger(op.tests) && op.tests >= 0 && op.tests <= 100000,
    );
    assert.equal(typeof op.cleanup_confirmed, "boolean");
    for (const key of ["instance_id", "candidate_digest"])
      assert.match(op[key], /^(?:[a-f0-9]{64})?$/);
  }
  return receipt;
}
function runNativeVMSmoke(
  { resources, dataDir, integrity, reportFile },
  execute = execFileSync,
) {
  assert.match(integrity, hash);
  verifyResources(resources, integrity);
  assert.ok(
    path.isAbsolute(dataDir) && !fs.existsSync(dataDir),
    "fresh absolute acceptance directory required",
  );
  assert.ok(
    path.isAbsolute(reportFile) && !fs.existsSync(reportFile),
    "fresh absolute receipt path required",
  );
  const relative = path.relative(resources, reportFile);
  assert.ok(
    relative === ".." ||
      relative.startsWith(".." + path.sep) ||
      path.isAbsolute(relative),
    "receipt must be outside installed resources",
  );
  const bundle = path.join(resources, "sandbox");
  let output,
    executionFailed = false;
  try {
    output = execute(
      path.join(resources, "bin", executableName("foundry-vm-acceptance")),
      ["--native-vm-acceptance", "--bundle", bundle, "--data-dir", dataDir],
      {
        encoding: "utf8",
        timeout: 11 * 60 * 1000,
        maxBuffer: 64 * 1024,
        windowsHide: true,
        env: childEnvironment(
          process.env,
          dataDir,
          path.join(resources, "web"),
        ),
        stdio: ["ignore", "pipe", "pipe"],
      },
    );
  } catch (error) {
    executionFailed = true;
    output = Buffer.isBuffer(error.stdout)
      ? error.stdout.toString("utf8")
      : error.stdout;
  }
  const receipt = boundedReceipt(output);
  fs.writeFileSync(reportFile, output, { flag: "wx", mode: 0o600 });
  assert.equal(executionFailed, false, "native harness returned nonzero");
  assertNativeVMReceipt(
    receipt,
    resourceDigest(path.join(bundle, "manifest.json")),
  );
  return receipt;
}
module.exports = { assertNativeVMReceipt, runNativeVMSmoke };
if (require.main === module)
  (async () => {
    const resources = process.env.FOUNDRY_INSTALLED_RESOURCES;
    const { extractFile } = await import("@electron/asar");
    const { manifestSHA256: integrity } = JSON.parse(
      extractFile(path.join(resources, "app.asar"), "resource-integrity.json"),
    );
    const receipt = runNativeVMSmoke({
      resources,
      dataDir: process.env.FOUNDRY_VM_ACCEPTANCE_DATA,
      reportFile: process.env.FOUNDRY_VM_ACCEPTANCE_REPORT,
      integrity,
    });
    process.stdout.write(JSON.stringify(receipt) + "\n");
  })().catch(() => {
    process.stderr.write(
      "Native VM acceptance failed; no artifact may be published.\n",
    );
    process.exitCode = 1;
  });
