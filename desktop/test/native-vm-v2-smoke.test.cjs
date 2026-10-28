"use strict";
const { test } = require("node:test"),
  assert = require("node:assert/strict"),
  fs = require("node:fs"),
  os = require("node:os"),
  path = require("node:path");
const {
  loadDesktopFixtureModule,
  sandboxFixture,
  v2Fixture,
} = require("./sandbox-fixture.cjs");
const { writeManifest } = require("../build-support.cjs");
const {
  nativeV2Cases,
  assertNativeVMReceiptV2,
} = require("../native-vm-v2.cjs");
const h = "a".repeat(64);
function success(profile, caseID, host, manifest = h) {
  const python = profile === "c4_python_model_v2",
    ts = profile === "c4_typescript_model_v2",
    workflow = caseID === "workflow";
  const meta = python
    ? ["foundry.python.lock.json"]
    : ["package-lock.json", "package.json", ...(ts ? ["tsconfig.json"] : [])];
  const source = python ? "src/triple.py" : `src/triple.${ts ? "ts" : "js"}`,
    test = python
      ? "tests/test_triple.py"
      : `tests/triple.test.${ts ? "ts" : "js"}`,
    helper = python ? "tests/guards.py" : "tests/guards.cjs";
  const protectedPaths = [...meta, helper, test].sort(),
    expected = Object.fromEntries(
      [...protectedPaths, source].map((p) => [p, h]),
    );
  const identity = {
    schema_version: 2,
    manifest_digest: manifest,
    host_platform: host,
    guest_platform: host === "windows/amd64" ? "linux/amd64" : "linux/arm64",
    runtime_digest: h,
    kernel_digest: h,
    rootfs_digest: h,
    verifier_digest: h,
    protocol: "foundry-vm-v2",
    policy_version: "multilang-vm-v2",
  };
  const dep = {
    schema_version: 1,
    ecosystem: python ? "python" : "node",
    bundle_digest: h,
    manifest_digest: h,
    file_count: 1,
    total_bytes: 1,
  };
  const inputs = {
    schema_version: 2,
    profile_id: profile,
    dependencies: dep,
    admission_digest: h,
    source_files: meta.map((p) => ({ path: p, bytes: 1, sha256: h })),
    protected: protectedPaths.map((p) => ({
      path: p,
      mode: 420,
      bytes: 1,
      sha256: h,
      role:
        p === test
          ? "test"
          : p === helper
            ? "helper"
            : p.endsWith("lock.json")
              ? "lock"
              : p === "tsconfig.json"
                ? "config"
                : "manifest",
    })),
    protected_prefixes: [
      ".foundry-build",
      ".venv",
      "node_modules",
      "site-packages",
      "tests",
    ],
    test_files: [test],
    source_roots: [python ? "src" : "."],
    harness_version: "foundry-test-v2",
    protected_inputs_digest: h,
    execution_contract_digest: h,
  };
  if (ts)
    inputs.typescript = {
      config_path: "tsconfig.json",
      config_digest: h,
      input_files: [source, test],
      output_root: "/foundry-build/compiled",
    };
  const checkNames = [
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
  const classes = workflow
    ? ["passed", "test_failed", "passed"]
    : [
        caseID === "timeout"
          ? "timeout"
          : caseID === "descendant"
            ? "passed"
            : "infrastructure_failed",
      ];
  const operations = classes.map((cl, i) => {
    const id = String(i + 1).repeat(64),
      count = workflow ? 4 : caseID === "descendant" ? 1 : 0;
    const phases = (
      ts
        ? [
            "compile-typescript",
            ...(caseID === "typecheck" ? [] : ["test-node"]),
          ]
        : [python ? "test-python" : "test-node"]
    ).map((p, j, all) => ({
      phase: p,
      exited: true,
      exit_code:
        j === all.length - 1
          ? cl === "passed"
            ? 0
            : ["memory", "signal", "timeout"].includes(caseID)
              ? -1
              : caseID === "held_stream"
                ? 0
                : caseID === "typecheck"
                  ? 2
                  : 1
          : 0,
      deadline_exceeded: caseID === "timeout",
      canceled: false,
      resource_limits_ok: !["memory", "pids"].includes(caseID),
      cleanup_confirmed: true,
      compiled_output_digest:
        p === "compile-typescript" && caseID !== "typecheck" ? h : "",
    }));
    const native = {
      session_id: id,
      operation_id: "op" + i,
      mission_id: "fixture-mission",
      owner: "owner",
      name: "owned-name",
      target: { profile_id: profile, identity, runtime_inventory_digest: h },
      request_digest: h,
      checks: Object.fromEntries(checkNames.map((k) => [k, true])),
      phases,
      class: cl,
      tests: count,
      exit_code: phases.at(-1).exit_code,
      truncated: false,
    };
    return {
      kind: i === 0 ? "baseline" : "candidate",
      class: cl,
      tests: count,
      instance_id: id,
      cleanup_confirmed: true,
      candidate_digest: id,
      native_v2: native,
    };
  });
  return {
    schema_version: 2,
    kind: "foundry-native-vm-acceptance",
    passed: true,
    source: "embedded_vm_c4_control",
    models: "local_protocol_fixture/not_real_provider",
    host_platform: host,
    guest_platform: identity.guest_platform,
    manifest_digest: manifest,
    acceleration:
      host === "windows/amd64" ? "whpx" : "virtualization_framework",
    mission_id: "fixture-mission",
    role_counts: workflow
      ? Object.fromEntries(
          ["researcher", "planner", "worker", "debugger", "qa", "auditor"].map(
            (k) => [k, 1],
          ),
        )
      : {},
    operations,
    checks: {
      approval_gates: true,
      protected_unchanged: true,
      source_unchanged: true,
      delivery_frozen: workflow,
      exact_instances: true,
      local_model_only: true,
      qa_auditor_bound: workflow,
    },
    stage: "complete",
    protected_digests: Object.fromEntries(protectedPaths.map((p) => [p, h])),
    source_digest: h,
    ...(workflow ? { patch_digest: h, report_digest: h } : {}),
    v2: {
      profile_id: profile,
      case: caseID,
      provenance: "repository_owned_local_fixture",
      runtime_inventory_digest: h,
      admission_digest: h,
      inputs,
      dependency_files: [
        {
          path: python
            ? "foundry_fixture_dep.py"
            : "foundry-fixture-dep/index.js",
          size: 1,
          sha256: h,
        },
      ],
      expected_files: expected,
    },
  };
}
test("closed v2 receipts reject swapped targets, fabricated counts and unobserved pressure", () => {
  const matrix = nativeV2Cases();
  assert.equal(matrix.length, 12);
  for (const row of matrix) {
    const r = success(row.profile, row.case, "windows/amd64");
    assert.doesNotThrow(() =>
      assertNativeVMReceiptV2(r, h, row.profile, row.case, "win32"),
    );
  }
  for (const mutate of [
    (r) => (r.operations[1].tests = 1),
    (r) => (r.operations[0].native_v2.phases = []),
    (r) =>
      (r.operations[0].native_v2.target.identity.manifest_digest = "b".repeat(
        64,
      )),
    (r) => (r.v2.admission_digest = "b".repeat(64)),
    (r) => (r.v2.expected_files["private-path"] = h),
    (r) => (r.operations[0].native_v2.checks.private_control = false),
    (r) => (r.operations[0].native_v2.secret = "PRIVATE"),
    (r) => (r.role_counts.debugger = 0),
    (r) => (r.operations[2].instance_id = r.operations[0].instance_id),
  ]) {
    const r = success("c4_python_model_v2", "workflow", "windows/amd64");
    mutate(r);
    assert.throws(() =>
      assertNativeVMReceiptV2(r, h, "c4_python_model_v2", "workflow", "win32"),
    );
  }
  for (const [profile, caseID] of [
    ["c4_typescript_model_v2", "typecheck"],
    ...["memory", "pids", "setup", "discovery", "held_stream"].map((c) => [
      "c4_python_model_v2",
      c,
    ]),
  ]) {
    const r = success(profile, caseID, "windows/amd64");
    r.operations[0].native_v2.exit_code = 80;
    r.operations[0].native_v2.phases.at(-1).exit_code = 80;
    assert.throws(() =>
      assertNativeVMReceiptV2(r, h, profile, caseID, "win32"),
    );
  }
  const r = success("c4_python_model_v2", "memory", "windows/amd64");
  r.operations[0].native_v2.phases[0].resource_limits_ok = true;
  assert.throws(() =>
    assertNativeVMReceiptV2(r, h, "c4_python_model_v2", "memory", "win32"),
  );
});
for (const host of ["darwin/arm64", "windows/amd64"])
  test(`installed matrix executes every closed case once against one v2 bundle: ${host}`, (t) => {
    const system = process.env.SystemRoot;
    if (host === "windows/amd64" && !system) {
      process.env.SystemRoot = "C:\\Windows";
      t.after(() => {
        delete process.env.SystemRoot;
      });
    }
    const driver = loadDesktopFixtureModule("native-vm-smoke.cjs", host),
      runtime = loadDesktopFixtureModule("runtime.cjs", host);
    const root = fs.realpathSync(
      fs.mkdtempSync(path.join(os.tmpdir(), "foundry-native-v2-")),
    );
    t.after(() => fs.rmSync(root, { recursive: true, force: true }));
    const resources = path.join(root, "resources");
    fs.mkdirSync(path.join(resources, "bin"), { recursive: true });
    fs.mkdirSync(path.join(resources, "web"));
    fs.writeFileSync(path.join(resources, "web/index.html"), "fixture");
    for (const n of ["foundry", "foundry-acceptance", "foundry-vm-acceptance"])
      fs.writeFileSync(
        path.join(resources, "bin", runtime.executableName(n)),
        "inert never executed",
      );
    sandboxFixture(path.join(resources, "sandbox"), host);
    v2Fixture(path.join(resources, "sandbox-v2"), host);
    writeManifest(resources);
    const options = {
        resources,
        dataDir: path.join(root, "case"),
        reportFile: path.join(root, "native-vm-v2-matrix.json"),
        integrity: runtime.resourceDigest(
          path.join(resources, "resources-manifest.json"),
        ),
      },
      calls = [];
    const execute = (binary, args, env) => {
      assert.equal(
        binary,
        path.join(
          resources,
          "bin",
          runtime.executableName("foundry-vm-acceptance"),
        ),
      );
      assert.equal(args[2], path.join(resources, "sandbox-v2"));
      assert.equal(env.env.OPENAI_API_KEY, undefined);
      assert.equal(env.maxBuffer, 65536);
      calls.push([args[6], args[8]]);
      return (
        JSON.stringify(
          success(
            args[6],
            args[8],
            host,
            runtime.resourceDigest(args[2] + "/manifest.json"),
          ),
        ) + "\n"
      );
    };
    const r = driver.runNativeV2Matrix(options, execute);
    assert.equal(r.passed, true);
    assert.deepEqual(
      calls,
      nativeV2Cases().map((r) => [r.profile, r.case]),
    );
    assert.equal(new Set(r.cases.map((r) => r.report)).size, 12);
    for (const row of r.cases)
      assert.equal(JSON.parse(fs.readFileSync(row.report)).passed, true);
    assert.throws(() =>
      driver.runNativeVMSmoke(
        {
          ...options,
          dataDir: options.dataDir + "bad",
          reportFile: options.reportFile + "bad",
          profile: "unknown",
          caseID: "workflow",
        },
        () => assert.fail("invalid selection executed"),
      ),
    );
    const failed = {
      ...options,
      dataDir: path.join(root, "failed"),
      reportFile: path.join(root, "failed-matrix.json"),
    };
    // Use a fresh output parent so an earlier passing case cannot mask failure.
    const failOutput = path.join(root, "failed-output");
    fs.mkdirSync(failOutput);
    failed.reportFile = path.join(failOutput, "matrix.json");
    let failures = 0;
    assert.throws(() =>
      driver.runNativeV2Matrix(failed, (binary, args) => {
        failures++;
        const receipt = success(
          args[6],
          args[8],
          host,
          runtime.resourceDigest(args[2] + "/manifest.json"),
        );
        receipt.passed = false;
        receipt.stage = "readiness";
        receipt.failure_code = "readiness_failed";
        const error = new Error("PRIVATE_HOST_ERROR");
        error.stdout = JSON.stringify(receipt) + "\n";
        throw error;
      }),
    );
    assert.equal(failures, 1);
    assert.equal(fs.existsSync(failed.reportFile), false);
    const retained = fs.readdirSync(failOutput);
    assert.deepEqual(retained, ["native-vm-v2-node-workflow.json"]);
    const failure = fs.readFileSync(path.join(failOutput, retained[0]), "utf8");
    assert.equal(JSON.parse(failure).passed, false);
    assert.doesNotMatch(failure, /PRIVATE_HOST_ERROR/);
  });
module.exports = { success };
