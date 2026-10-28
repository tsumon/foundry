"use strict";
// Explicit native acceptance receipt policy; never imported by app startup.
const a = require("node:assert/strict");
const hash = /^[a-f0-9]{64}$/,
  roles = ["researcher", "planner", "worker", "debugger", "qa", "auditor"];
const checks = [
  "approval_gates",
  "protected_unchanged",
  "source_unchanged",
  "delivery_frozen",
  "exact_instances",
  "local_model_only",
  "qa_auditor_bound",
];
const nativeChecks = [
  "read_only_workspace",
  "read_only_toolchain",
  "no_network",
  "private_control",
  "unprivileged",
  "no_new_privileges",
  "cgroup_limits",
  "tmpfs_limits",
  "read_only_dependencies",
  "runtime_inventory_matches",
  "input_binding_valid",
  "phase_cleanup_confirmed",
];
const profiles = [
  "c4_node_model_v2",
  "c4_typescript_model_v2",
  "c4_python_model_v2",
];
function keys(v, n) {
  a.ok(v && typeof v === "object" && !Array.isArray(v));
  a.deepEqual(Object.keys(v).sort(), [...n].sort());
}
function digest(v, empty = false) {
  a.equal(typeof v, "string");
  a.ok(hash.test(v) || (empty && v === ""));
}
function integer(v, min, max) {
  a.ok(Number.isSafeInteger(v) && v >= min && v <= max);
}
function nativeV2Cases() {
  return [
    ...profiles.map((profile) => ({ profile, case: "workflow" })),
    { profile: profiles[1], case: "typecheck" },
    ...[
      "setup",
      "discovery",
      "memory",
      "pids",
      "timeout",
      "signal",
      "descendant",
      "held_stream",
    ].map((caseID) => ({ profile: profiles[2], case: caseID })),
  ];
}
function selectedV2(profile, caseID) {
  a.ok(
    nativeV2Cases().some((r) => r.profile === profile && r.case === caseID),
    "Unsupported v2 native fixture selection",
  );
  return { profile, case: caseID };
}
function expectedV2Files(profile) {
  const py = profile === profiles[2],
    ts = profile === profiles[1];
  return {
    source: py ? "src/triple.py" : `src/triple.${ts ? "ts" : "js"}`,
    test: py ? "tests/test_triple.py" : `tests/triple.test.${ts ? "ts" : "js"}`,
    helper: py ? "tests/guards.py" : "tests/guards.cjs",
    metadata: py
      ? ["foundry.python.lock.json"]
      : ["package-lock.json", "package.json", ...(ts ? ["tsconfig.json"] : [])],
  };
}
function identityShape(v) {
  keys(v, [
    "schema_version",
    "manifest_digest",
    "host_platform",
    "guest_platform",
    "runtime_digest",
    "kernel_digest",
    "rootfs_digest",
    "verifier_digest",
    "protocol",
    "policy_version",
  ]);
  a.equal(v.schema_version, 2);
  for (const k of [
    "manifest_digest",
    "runtime_digest",
    "kernel_digest",
    "rootfs_digest",
    "verifier_digest",
  ])
    digest(v[k]);
  a.ok(["darwin/arm64", "windows/amd64"].includes(v.host_platform));
  a.equal(
    v.guest_platform,
    v.host_platform === "windows/amd64" ? "linux/amd64" : "linux/arm64",
  );
  a.equal(v.protocol, "foundry-vm-v2");
  a.equal(v.policy_version, "multilang-vm-v2");
}
function targetShape(v) {
  keys(v, ["profile_id", "identity", "runtime_inventory_digest"]);
  a.ok(profiles.includes(v.profile_id));
  identityShape(v.identity);
  digest(v.runtime_inventory_digest);
}
function bindingShape(v) {
  keys(v, [
    "schema_version",
    "ecosystem",
    "bundle_digest",
    "manifest_digest",
    "file_count",
    "total_bytes",
  ]);
  a.equal(v.schema_version, 1);
  a.ok(["node", "python"].includes(v.ecosystem));
  digest(v.bundle_digest);
  digest(v.manifest_digest);
  integer(v.file_count, 1, 10000);
  integer(v.total_bytes, 1, 67108864);
}
function rows(v, fields) {
  a.ok(Array.isArray(v) && v.length > 0 && v.length <= 500);
  const names = [];
  for (const row of v) {
    keys(row, fields);
    a.match(row.path, /^[A-Za-z0-9_./-]{1,1024}$/);
    a.ok(!row.path.split("/").some((s) => !s || s === "." || s === ".."));
    digest(row.sha256);
    if (Object.hasOwn(row, "bytes")) integer(row.bytes, 0, 10485760);
    if (Object.hasOwn(row, "size")) integer(row.size, 0, 67108864);
    if (Object.hasOwn(row, "mode")) a.equal(row.mode, 420);
    if (Object.hasOwn(row, "role"))
      a.ok(["test", "helper", "manifest", "config", "lock"].includes(row.role));
    names.push(row.path);
  }
  a.equal(new Set(names).size, names.length);
  a.deepEqual(names, [...names].sort());
}
function inputsShape(v) {
  keys(v, [
    "schema_version",
    "profile_id",
    "dependencies",
    "admission_digest",
    "source_files",
    "protected",
    "protected_prefixes",
    "test_files",
    "source_roots",
    "harness_version",
    "protected_inputs_digest",
    "execution_contract_digest",
    ...(Object.hasOwn(v, "typescript") ? ["typescript"] : []),
  ]);
  a.equal(v.schema_version, 2);
  a.ok(profiles.includes(v.profile_id));
  bindingShape(v.dependencies);
  for (const k of [
    "admission_digest",
    "protected_inputs_digest",
    "execution_contract_digest",
  ])
    digest(v[k]);
  rows(v.source_files, ["path", "bytes", "sha256"]);
  rows(v.protected, ["path", "mode", "bytes", "sha256", "role"]);
  a.equal(v.harness_version, "foundry-test-v2");
  a.deepEqual(v.protected_prefixes, [
    ".foundry-build",
    ".venv",
    "node_modules",
    "site-packages",
    "tests",
  ]);
  const f = expectedV2Files(v.profile_id);
  a.deepEqual(v.source_roots, [v.profile_id === profiles[2] ? "src" : "."]);
  a.deepEqual(v.test_files, [f.test]);
  a.deepEqual(
    v.source_files.map((r) => r.path),
    [...f.metadata].sort(),
  );
  a.deepEqual(
    v.protected.map((r) => r.path),
    [...f.metadata, f.test, f.helper].sort(),
  );
  a.equal(
    v.dependencies.ecosystem,
    v.profile_id === profiles[2] ? "python" : "node",
  );
  if (v.profile_id === profiles[1]) {
    keys(v.typescript, [
      "config_path",
      "config_digest",
      "input_files",
      "output_root",
    ]);
    a.equal(v.typescript.config_path, "tsconfig.json");
    digest(v.typescript.config_digest);
    a.deepEqual(v.typescript.input_files, [f.source, f.test]);
    a.equal(v.typescript.output_root, "/foundry-build/compiled");
    a.equal(
      v.typescript.config_digest,
      v.source_files.find((r) => r.path === "tsconfig.json").sha256,
    );
  } else a.equal(v.typescript, undefined);
}
function factsShape(n) {
  keys(n, [
    "session_id",
    "operation_id",
    "mission_id",
    "owner",
    "name",
    "target",
    "request_digest",
    "checks",
    "phases",
    "class",
    "tests",
    "exit_code",
    "truncated",
  ]);
  digest(n.session_id);
  digest(n.request_digest);
  for (const k of ["operation_id", "mission_id", "owner", "name"])
    a.match(n[k], /^[A-Za-z0-9_-]{1,256}$/);
  targetShape(n.target);
  keys(n.checks, nativeChecks);
  for (const v of Object.values(n.checks)) a.equal(typeof v, "boolean");
  a.ok(
    ["passed", "test_failed", "infrastructure_failed", "timeout"].includes(
      n.class,
    ),
  );
  integer(n.tests, 0, 4096);
  integer(n.exit_code, -1, 255);
  a.equal(typeof n.truncated, "boolean");
  a.ok(Array.isArray(n.phases) && n.phases.length <= 2);
  for (const p of n.phases) {
    keys(p, [
      "phase",
      "exited",
      "exit_code",
      "deadline_exceeded",
      "canceled",
      "resource_limits_ok",
      "cleanup_confirmed",
      "compiled_output_digest",
    ]);
    a.ok(["compile-typescript", "test-node", "test-python"].includes(p.phase));
    for (const k of [
      "exited",
      "deadline_exceeded",
      "canceled",
      "resource_limits_ok",
      "cleanup_confirmed",
    ])
      a.equal(typeof p[k], "boolean");
    integer(p.exit_code, -1, 255);
    digest(p.compiled_output_digest, true);
  }
}
const baseKeys = [
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
  "v2",
];
const optional = [
  "failure_code",
  "protected_digests",
  "source_digest",
  "patch_digest",
  "report_digest",
];
function boundedV2Receipt(raw) {
  a.ok(typeof raw === "string" && Buffer.byteLength(raw) <= 65536);
  const r = JSON.parse(raw);
  keys(r, [...baseKeys, ...optional.filter((k) => Object.hasOwn(r, k))]);
  a.equal(r.schema_version, 2);
  a.equal(r.kind, "foundry-native-vm-acceptance");
  a.equal(typeof r.passed, "boolean");
  a.equal(r.source, "embedded_vm_c4_control");
  a.equal(r.models, "local_protocol_fixture/not_real_provider");
  a.match(r.stage, /^[a-z_]{1,64}$/);
  if (r.failure_code !== undefined) a.match(r.failure_code, /^[a-z_]{1,64}$/);
  a.ok(["darwin/arm64", "windows/amd64"].includes(r.host_platform));
  a.ok(["", "linux/arm64", "linux/amd64"].includes(r.guest_platform));
  a.ok(["", "whpx", "virtualization_framework"].includes(r.acceleration));
  digest(r.manifest_digest, true);
  a.match(r.mission_id, /^[A-Za-z0-9_-]{0,128}$/);
  for (const k of ["source_digest", "patch_digest", "report_digest"])
    if (Object.hasOwn(r, k)) digest(r[k]);
  keys(r.v2, [
    "profile_id",
    "case",
    "provenance",
    "runtime_inventory_digest",
    "admission_digest",
    "inputs",
    "dependency_files",
    "expected_files",
  ]);
  selectedV2(r.v2.profile_id, r.v2.case);
  a.equal(r.v2.provenance, "repository_owned_local_fixture");
  digest(r.v2.runtime_inventory_digest, true);
  digest(r.v2.admission_digest, true);
  const f = expectedV2Files(r.v2.profile_id),
    expected = [f.source, f.test, f.helper, ...f.metadata];
  for (const [field, allowed] of [
    ["protected_digests", expected.filter((p) => p !== f.source)],
  ])
    if (r[field] !== undefined) {
      keys(r[field], allowed);
      for (const d of Object.values(r[field])) digest(d);
    }
  a.ok(
    r.v2.expected_files &&
      typeof r.v2.expected_files === "object" &&
      !Array.isArray(r.v2.expected_files),
  );
  for (const [p, d] of Object.entries(r.v2.expected_files)) {
    a.ok(expected.includes(p));
    digest(d);
  }
  a.ok(
    Array.isArray(r.v2.dependency_files) &&
      r.v2.dependency_files.length <= 10000,
  );
  if (r.v2.dependency_files.length)
    rows(r.v2.dependency_files, ["path", "size", "sha256"]);
  if (r.v2.inputs !== null) inputsShape(r.v2.inputs);
  for (const [k, v] of Object.entries(r.role_counts || {})) {
    a.ok(roles.includes(k));
    integer(v, 0, 16);
  }
  keys(r.checks, checks);
  for (const v of Object.values(r.checks)) a.equal(typeof v, "boolean");
  a.ok(Array.isArray(r.operations) && r.operations.length <= 3);
  for (const o of r.operations) {
    keys(o, [
      "kind",
      "class",
      "tests",
      "instance_id",
      "cleanup_confirmed",
      "candidate_digest",
      "native_v2",
    ]);
    a.ok(["baseline", "candidate"].includes(o.kind));
    a.ok(
      ["passed", "test_failed", "infrastructure_failed", "timeout"].includes(
        o.class,
      ),
    );
    integer(o.tests, 0, 4096);
    digest(o.instance_id);
    digest(o.candidate_digest);
    a.equal(typeof o.cleanup_confirmed, "boolean");
    factsShape(o.native_v2);
  }
  return r;
}
function assertNativeVMReceiptV2(
  receipt,
  manifest,
  profile,
  caseID,
  platform = process.platform,
) {
  selectedV2(profile, caseID);
  const r = boundedV2Receipt(JSON.stringify(receipt));
  a.ok(["darwin", "win32"].includes(platform));
  a.equal(r.passed, true);
  a.equal(r.failure_code, undefined);
  a.equal(r.stage, "complete");
  digest(manifest);
  a.equal(r.manifest_digest, manifest);
  a.equal(
    r.host_platform,
    platform === "win32" ? "windows/amd64" : "darwin/arm64",
  );
  a.equal(
    r.guest_platform,
    platform === "win32" ? "linux/amd64" : "linux/arm64",
  );
  a.equal(
    r.acceleration,
    platform === "win32" ? "whpx" : "virtualization_framework",
  );
  a.match(r.mission_id, /^[A-Za-z0-9_-]{1,128}$/);
  digest(r.source_digest);
  a.equal(r.v2.profile_id, profile);
  a.equal(r.v2.case, caseID);
  digest(r.v2.runtime_inventory_digest);
  digest(r.v2.admission_digest);
  inputsShape(r.v2.inputs);
  a.equal(r.v2.inputs.profile_id, profile);
  a.equal(r.v2.inputs.admission_digest, r.v2.admission_digest);
  const f = expectedV2Files(profile);
  keys(r.v2.expected_files, [f.source, f.test, f.helper, ...f.metadata]);
  keys(r.protected_digests, [f.test, f.helper, ...f.metadata]);
  for (const p of r.v2.inputs.protected) {
    a.equal(p.sha256, r.protected_digests[p.path]);
    a.equal(p.sha256, r.v2.expected_files[p.path]);
  }
  for (const p of r.v2.inputs.source_files)
    a.equal(p.sha256, r.v2.expected_files[p.path]);
  a.equal(r.v2.dependency_files.length, r.v2.inputs.dependencies.file_count);
  a.equal(
    r.v2.dependency_files.reduce((n, f) => n + f.size, 0),
    r.v2.inputs.dependencies.total_bytes,
  );
  const workflow = caseID === "workflow",
    classes = workflow
      ? ["passed", "test_failed", "passed"]
      : [
          caseID === "timeout"
            ? "timeout"
            : caseID === "descendant"
              ? "passed"
              : "infrastructure_failed",
        ];
  keys(r.role_counts, workflow ? roles : []);
  for (const v of Object.values(r.role_counts)) a.equal(v, 1);
  for (const check of checks)
    a.equal(
      r.checks[check],
      ["delivery_frozen", "qa_auditor_bound"].includes(check) ? workflow : true,
    );
  if (workflow) {
    digest(r.patch_digest);
    digest(r.report_digest);
  } else {
    a.equal(r.patch_digest, undefined);
    a.equal(r.report_digest, undefined);
  }
  a.equal(r.operations.length, classes.length);
  a.equal(new Set(r.operations.map((o) => o.instance_id)).size, classes.length);
  r.operations.forEach((o, i) => {
    a.equal(o.kind, i === 0 ? "baseline" : "candidate");
    a.equal(o.class, classes[i]);
    a.equal(o.tests, workflow ? 4 : caseID === "descendant" ? 1 : 0);
    a.equal(o.cleanup_confirmed, true);
    const n = o.native_v2;
    a.equal(n.session_id, o.instance_id);
    a.equal(n.mission_id, r.mission_id);
    a.equal(n.class, o.class);
    a.equal(n.tests, o.tests);
    a.equal(n.truncated, false);
    a.equal(n.target.profile_id, profile);
    a.equal(n.target.identity.manifest_digest, manifest);
    a.equal(n.target.identity.host_platform, r.host_platform);
    a.equal(n.target.runtime_inventory_digest, r.v2.runtime_inventory_digest);
    for (const v of Object.values(n.checks)) a.equal(v, true);
    const phases =
      profile === profiles[1]
        ? [
            "compile-typescript",
            ...(caseID === "typecheck" ? [] : ["test-node"]),
          ]
        : [profile === profiles[2] ? "test-python" : "test-node"];
    a.deepEqual(
      n.phases.map((p) => p.phase),
      phases,
    );
    for (const p of n.phases) {
      a.equal(p.exited, true);
      a.equal(p.canceled, false);
      a.equal(p.cleanup_confirmed, true);
      a.equal(p.deadline_exceeded, caseID === "timeout");
      a.equal(p.resource_limits_ok, !["memory", "pids"].includes(caseID));
      if (p.phase === "compile-typescript" && caseID !== "typecheck") {
        digest(p.compiled_output_digest);
        a.equal(p.exit_code, 0);
      } else a.equal(p.compiled_output_digest, "");
    }
    const last = n.phases.at(-1);
    a.equal(n.exit_code, last.exit_code);
    if (o.class === "passed") a.equal(n.exit_code, 0);
    if (o.class === "test_failed") integer(n.exit_code, 1, 124);
    if (["memory", "signal", "timeout"].includes(caseID))
      a.equal(last.exit_code, -1);
    if (["pids", "setup", "discovery"].includes(caseID))
      a.equal(last.exit_code, 1);
    if (caseID === "held_stream") a.equal(last.exit_code, 0);
    if (caseID === "typecheck") a.equal(last.exit_code, 2);
  });
  if (workflow)
    a.notEqual(
      r.operations[1].candidate_digest,
      r.operations[2].candidate_digest,
    );
  return r;
}
module.exports = {
  nativeV2Cases,
  selectedV2,
  boundedV2Receipt,
  assertNativeVMReceiptV2,
};
