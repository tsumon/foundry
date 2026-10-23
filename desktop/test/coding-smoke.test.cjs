"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const { assertCodingMetadata } = require("../coding-smoke.cjs");

const metadata = {
  enabled: true,
  available: false,
  reason: "native Docker execution is unavailable on Windows",
  profile: {
    schema_version: 1,
    control_version: 1,
    id: "c4_go_model_v1",
    mode: "docker",
    platform: "linux/arm64",
    verifier: "/opt/foundry/verify-go-v1",
    docker_path: "C:\\__foundry_desktop_docker_disabled__.exe",
    endpoint: "",
    image_id: "",
  },
  limits: { max_calls: 10, max_repairs: 2 },
  provider_families: ["openai"],
  approval_required: [
    "readiness",
    "baseline",
    "context",
    "execute",
    "delivery",
  ],
};

test("Windows installed coding smoke accepts configuration metadata without execution availability", () => {
  assert.doesNotThrow(() => assertCodingMetadata(metadata, "win32"));
});

test("normal startup exposes disabled metadata without enabling configuration", () => {
  const disabled = structuredClone(metadata);
  disabled.enabled = false;
  disabled.reason = "coding_workbench_disabled";
  assert.doesNotThrow(() =>
    assertCodingMetadata(disabled, "win32", false),
  );
});

test("installed coding smoke rejects startup authority, effect results, and unsupported availability claims", () => {
  for (const change of [
    (value) => (value.enabled = false),
    (value) => (value.available = true),
    (value) => (value.reason = ""),
    (value) => (value.model_calls = []),
    (value) => (value.docker_commands = []),
    (value) => (value.tasks = []),
    (value) => value.approval_required.pop(),
  ]) {
    const altered = structuredClone(metadata);
    change(altered);
    assert.throws(() => assertCodingMetadata(altered, "win32"));
  }
});
