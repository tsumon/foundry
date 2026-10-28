"use strict";
const { test } = require("node:test"),
  assert = require("node:assert/strict");
const { assertCodingMetadata } = require("../coding-smoke.cjs");
function metadata(platform = "win32", enabled = true) {
  const identity = {
    schema_version: 1,
    host_platform: platform === "win32" ? "windows/amd64" : "darwin/arm64",
    guest_platform: platform === "win32" ? "linux/amd64" : "linux/arm64",
    manifest_digest: "1".repeat(64),
    runtime_digest: "2".repeat(64),
    kernel_digest: "3".repeat(64),
    rootfs_digest: "4".repeat(64),
    verifier_digest: "5".repeat(64),
    protocol: "foundry-vm-v1",
    policy_version: "go-vm-v1",
  };
  const profile = {
    schema_version: 1,
    control_version: 1,
    id: "c4_go_model_v1",
    mode: "embedded_vm",
    platform: enabled ? identity.guest_platform : "",
    verifier: "/opt/foundry/verify-go-v1",
    docker_path: "",
    endpoint: "",
    image_id: "",
    ...(enabled ? { embedded_vm: identity } : {}),
  };
  const docker = {
    ...profile,
    mode: "docker",
    platform: "linux/arm64",
    docker_path: "/usr/bin/docker",
    endpoint: "unix:///var/run/docker.sock",
  };
  delete docker.embedded_vm;
  return {
    enabled,
    available: enabled,
    reason: enabled ? "" : "coding_workbench_disabled",
    default_backend: "embedded_vm",
    profile,
    profiles: [
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
    ].map(([profile_id, language, runner_id, verifier]) => ({
      schema_version: 2,
      profile_id,
      language,
      runner_id,
      verifier,
      mode: "embedded_vm",
      available: enabled,
      reason: enabled ? "" : "embedded_vm_v2_bundle_unavailable",
      limits: {
        max_calls: 16,
        max_repairs: 2,
        max_source_files: 500,
        max_source_bytes: 10485760,
        max_dependency_files: 10000,
        max_dependency_bytes: 67108864,
      },
      ...(enabled
        ? {
            embedded_vm: {
              ...identity,
              schema_version: 2,
              manifest_digest: "6".repeat(64),
              protocol: "foundry-vm-v2",
              policy_version: "multilang-vm-v2",
            },
            runtime_inventory_digest: "7".repeat(64),
          }
        : {}),
    })),
    backends: [
      {
        mode: "embedded_vm",
        available: enabled,
        profile,
        prerequisite: "hardware virtualization; readiness approval required",
      },
      {
        mode: "docker",
        available: enabled && platform !== "win32",
        profile: docker,
        prerequisite: "explicit Docker selection",
      },
    ],
    limits: { max_calls: 16, max_repairs: 2 },
    provider_families: ["openai", "custom"],
    approval_required: [
      "readiness",
      "baseline",
      "context",
      "execute",
      "delivery",
    ],
  };
}
test("installed VM coding metadata advertises availability without readiness or account claims on both hosts", () => {
  for (const platform of ["darwin", "win32"])
    assert.doesNotThrow(() =>
      assertCodingMetadata(metadata(platform), platform),
    );
});
test("normal startup remains disabled and need not expose coding runtime identity", () => {
  for (const platform of ["darwin", "win32"])
    assert.doesNotThrow(() =>
      assertCodingMetadata(metadata(platform, false), platform, false),
    );
});
test("disabled metadata may retain installed v2 identity without availability or readiness authority", () => {
  for (const platform of ["darwin", "win32"]) {
    for (const reason of [
      "coding_workbench_disabled",
      "coding_runtime_unavailable_or_expired",
      "recovery_read_only",
    ]) {
      const value = metadata(platform);
      value.enabled = value.available = false;
      value.reason = "coding_workbench_disabled";
      for (const backend of value.backends) backend.available = false;
      for (const profile of value.profiles) {
        profile.available = false;
        profile.reason = reason;
      }
      assert.doesNotThrow(() => assertCodingMetadata(value, platform, false));
    }
  }
});
test("installed metadata validates the closed v2 profiles and shared runtime rather than allowing arbitrary additions", () => {
  for (const mutate of [
    (v) => delete v.profiles,
    (v) => (v.profiles = null),
    (v) => (v.profiles = []),
    (v) => (v.profiles[0] = null),
    (v) => v.profiles.push(v.profiles[0]),
    (v) => v.profiles.reverse(),
    (v) => (v.profiles[1].profile_id = v.profiles[0].profile_id),
    (v) => (v.profiles[0].schema_version = 1),
    (v) => (v.profiles[0].language = "go"),
    (v) => (v.profiles[0].runner_id = "python-unittest-v2"),
    (v) => (v.profiles[0].verifier = "/untrusted/runner"),
    (v) => (v.profiles[0].mode = "docker"),
    (v) => (v.profiles[0].available = false),
    (v) => (v.profiles[0].reason = "readiness_passed"),
    (v) => delete v.profiles[0].limits,
    (v) => (v.profiles[0].limits.max_calls = 17),
    (v) => (v.profiles[0].limits.max_source_bytes = "10485760"),
    (v) => (v.profiles[0].limits.model_calls = 0),
    (v) => (v.profiles[0].ready = true),
    (v) => delete v.profiles[0].embedded_vm,
    (v) => (v.profiles[0].embedded_vm = null),
    (v) => (v.profiles[0].embedded_vm.schema_version = 1),
    (v) => (v.profiles[0].embedded_vm.host_platform = "darwin/arm64"),
    (v) => (v.profiles[0].embedded_vm.guest_platform = "linux/arm64"),
    (v) => (v.profiles[0].embedded_vm.protocol = "foundry-vm-v1"),
    (v) => (v.profiles[0].embedded_vm.policy_version = "go-vm-v1"),
    (v) => (v.profiles[0].embedded_vm.kernel_digest = "bad"),
    (v) => delete v.profiles[0].embedded_vm.rootfs_digest,
    (v) => (v.profiles[0].embedded_vm.ready = true),
    (v) => (v.profiles[0].embedded_vm.manifest_digest = "8".repeat(64)),
    (v) => delete v.profiles[0].runtime_inventory_digest,
    (v) => (v.profiles[0].runtime_inventory_digest = "BAD"),
    (v) => (v.profiles[0].runtime_inventory_digest = "8".repeat(64)),
  ]) {
    const value = metadata();
    mutate(value);
    assert.throws(() => assertCodingMetadata(value, "win32"));
  }
});
test("disabled profiles reject execution claims and identity/reason ambiguity", () => {
  for (const mutate of [
    (v) => (v.profiles[0].available = true),
    (v) => (v.profiles[0].reason = ""),
    (v) => (v.profiles[0].reason = "readiness_passed"),
    (v) => (v.profiles[0].reason = "coding_workbench_disabled"),
    (v) => (v.profiles[0].embedded_vm = metadata().profiles[0].embedded_vm),
    (v) => (v.profiles[0].runtime_inventory_digest = "7".repeat(64)),
    (v) =>
      Object.assign(v.profiles[0], {
        reason: "coding_workbench_disabled",
        embedded_vm: metadata().profiles[0].embedded_vm,
        runtime_inventory_digest: "7".repeat(64),
      }),
  ]) {
    const value = metadata("win32", false);
    mutate(value);
    assert.throws(() => assertCodingMetadata(value, "win32", false));
  }
});
test("installed metadata rejects missing bundle, mixed authority, identity mismatch and effect claims", () => {
  for (const mutate of [
    (v) => (v.enabled = false),
    (v) => (v.available = false),
    (v) => (v.model_calls = []),
    (v) => (v.docker_commands = []),
    (v) => (v.tasks = []),
    (v) => (v.profile.embedded_vm.kernel_digest = "bad"),
    (v) => (v.profile.embedded_vm.host_platform = "darwin/arm64"),
    (v) => (v.profile.docker_path = "/evil"),
    (v) => (v.default_backend = "docker"),
    (v) => (v.backends[0].available = false),
    (v) => v.approval_required.pop(),
  ]) {
    const value = metadata();
    mutate(value);
    assert.throws(() => assertCodingMetadata(value, "win32"));
  }
});
