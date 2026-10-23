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
    provider_families: ["openai"],
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
  assert.doesNotThrow(() =>
    assertCodingMetadata(metadata("win32", false), "win32", false),
  );
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
