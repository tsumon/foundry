"use strict";

const assert = require("node:assert/strict");

const codingMetadataFields = [
  "approval_required",
  "available",
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
  assert.equal(value.available, expectedEnabled && platform !== "win32");
  assert.equal(typeof value.reason, "string");
  if (!value.available) assert.ok(value.reason.length > 0);
  if (!expectedEnabled) assert.equal(value.reason, "coding_workbench_disabled");
  assert.equal(value.profile?.id, "c4_go_model_v1");
  assert.equal(value.profile?.mode, "docker");
  assert.deepEqual(value.provider_families, ["openai"]);
  assert.deepEqual(value.approval_required, [
    "readiness",
    "baseline",
    "context",
    "execute",
    "delivery",
  ]);
}

module.exports = { assertCodingMetadata };
