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
  "profiles",
  "provider_families",
  "reason",
];

function assertCodingProfiles(profiles, platform, expectedEnabled) {
  const tuples = [
    [
      "c4_node_model_v2",
      "javascript",
      "node-test-v2",
      "/opt/foundry/verify-node-v2",
    ],
    [
      "c4_typescript_model_v2",
      "typescript",
      "typescript-node-test-v2",
      "/opt/foundry/verify-typescript-v2",
    ],
    [
      "c4_python_model_v2",
      "python",
      "python-unittest-v2",
      "/opt/foundry/verify-python-v2",
    ],
  ];
  assert.ok(Array.isArray(profiles));
  assert.equal(profiles.length, tuples.length);
  for (const [index, row] of profiles.entries()) {
    assert.ok(row && typeof row === "object" && !Array.isArray(row));
    const hasIdentity = Object.hasOwn(row, "embedded_vm");
    assert.deepEqual(
      Object.keys(row).sort(),
      [
        "schema_version",
        "profile_id",
        "language",
        "runner_id",
        "verifier",
        "mode",
        "available",
        "reason",
        "limits",
        ...(hasIdentity ? ["embedded_vm", "runtime_inventory_digest"] : []),
      ].sort(),
    );
    assert.deepEqual(
      [
        row.schema_version,
        row.profile_id,
        row.language,
        row.runner_id,
        row.verifier,
        row.mode,
      ],
      [2, ...tuples[index], "embedded_vm"],
    );
    assert.equal(row.available, expectedEnabled);
    if (expectedEnabled) assert.equal(row.reason, "");
    else
      assert.ok(
        [
          "embedded_vm_v2_bundle_unavailable",
          "coding_workbench_disabled",
          "coding_runtime_unavailable_or_expired",
          "recovery_read_only",
        ].includes(row.reason),
      );
    assert.deepEqual(row.limits, {
      max_calls: 16,
      max_repairs: 2,
      max_source_files: 500,
      max_source_bytes: 10485760,
      max_dependency_files: 10000,
      max_dependency_bytes: 67108864,
    });
    assert.equal(
      hasIdentity,
      row.reason !== "embedded_vm_v2_bundle_unavailable",
    );
    assert.equal(hasIdentity, Object.hasOwn(profiles[0], "embedded_vm"));
    if (!hasIdentity) continue;
    const vm = row.embedded_vm;
    assert.ok(vm && typeof vm === "object" && !Array.isArray(vm));
    assert.deepEqual(
      Object.keys(vm).sort(),
      [
        "schema_version",
        "host_platform",
        "guest_platform",
        "manifest_digest",
        "runtime_digest",
        "kernel_digest",
        "rootfs_digest",
        "verifier_digest",
        "protocol",
        "policy_version",
      ].sort(),
    );
    assert.equal(vm.schema_version, 2);
    assert.equal(
      vm.host_platform,
      platform === "win32" ? "windows/amd64" : "darwin/arm64",
    );
    assert.equal(
      vm.guest_platform,
      platform === "win32" ? "linux/amd64" : "linux/arm64",
    );
    assert.equal(vm.protocol, "foundry-vm-v2");
    assert.equal(vm.policy_version, "multilang-vm-v2");
    for (const digest of [
      vm.manifest_digest,
      vm.runtime_digest,
      vm.kernel_digest,
      vm.rootfs_digest,
      vm.verifier_digest,
      row.runtime_inventory_digest,
    ]) {
      assert.equal(typeof digest, "string");
      assert.match(digest, /^[a-f0-9]{64}$/);
    }
    // All selected languages share the same installed v2 image and inventory.
    assert.deepEqual(vm, profiles[0].embedded_vm);
    assert.equal(
      row.runtime_inventory_digest,
      profiles[0].runtime_inventory_digest,
    );
  }
}

function assertCodingMetadata(
  value,
  platform = process.platform,
  expectedEnabled = true,
) {
  assert.equal(typeof value, "object");
  assert.notEqual(value, null);
  assert.deepEqual(Object.keys(value).sort(), codingMetadataFields);
  assertCodingProfiles(value.profiles, platform, expectedEnabled);
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
