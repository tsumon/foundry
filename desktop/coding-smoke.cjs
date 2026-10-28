"use strict";

const assert = require("node:assert/strict");

const codingMetadataFields = [
  "approval_required",
  "available",
  "backends",
  "default_backend",
  "enabled",
  "limits",
  "profile",
  "provider_families",
  "reason",
];

function assertCodingMetadata(
  value,
  platform = process.platform,
  expectedEnabled = true,
) {
  assert.equal(typeof value, "object");
  assert.notEqual(value, null);
  assert.deepEqual(Object.keys(value).sort(), codingMetadataFields);
  assert.equal(value.enabled, expectedEnabled);
  assert.equal(value.available, expectedEnabled);
  assert.equal(typeof value.reason, "string");
  if (!value.available) assert.ok(value.reason.length > 0);
  if (!expectedEnabled) assert.equal(value.reason, "coding_workbench_disabled");
  assert.equal(value.profile?.id, "c4_go_model_v1");
  assert.equal(value.default_backend, "embedded_vm");
  assert.equal(value.profile?.mode, "embedded_vm");
  assert.equal(value.profile.schema_version, 1);
  assert.equal(value.profile.control_version, 1);
  for (const key of ["docker_path", "endpoint", "image_id"])
    assert.equal(value.profile[key], "");
  assert.equal(value.profile.verifier, "/opt/foundry/verify-go-v1");
  assert.equal(value.backends.length, 2);
  assert.deepEqual(
    value.backends.map((b) => b.mode),
    ["embedded_vm", "docker"],
  );
  assert.equal(value.backends[0].available, expectedEnabled);
  assert.deepEqual(value.backends[0].profile, value.profile);
  assert.equal(
    value.backends[1].available,
    expectedEnabled && platform !== "win32",
  );
  assert.equal(value.backends[1].profile.mode, "docker");
  assert.equal(value.backends[1].profile.embedded_vm, undefined);
  for (const backend of value.backends)
    assert.equal(typeof backend.prerequisite, "string");
  if (expectedEnabled) {
    const vm = value.profile.embedded_vm;
    assert.equal(vm?.schema_version, 1);
    assert.equal(
      vm.host_platform,
      platform === "win32" ? "windows/amd64" : "darwin/arm64",
    );
    assert.equal(
      vm.guest_platform,
      platform === "win32" ? "linux/amd64" : "linux/arm64",
    );
    assert.equal(value.profile.platform, vm.guest_platform);
    assert.equal(vm.protocol, "foundry-vm-v1");
    assert.equal(vm.policy_version, "go-vm-v1");
    for (const key of [
      "manifest_digest",
      "runtime_digest",
      "kernel_digest",
      "rootfs_digest",
      "verifier_digest",
    ]) {
      assert.equal(typeof vm[key], "string");
      assert.match(vm[key], /^[a-f0-9]{64}$/);
    }
  }
  assert.deepEqual(value.provider_families, ["openai", "custom"]);
  assert.deepEqual(value.approval_required, [
    "readiness",
    "baseline",
    "context",
    "execute",
    "delivery",
  ]);
}

module.exports = { assertCodingMetadata };
