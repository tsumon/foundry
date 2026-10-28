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
const {nativeV2Cases,selectedV2,boundedV2Receipt,assertNativeVMReceiptV2}=require("./native-vm-v2.cjs");
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
  if(receipt.schema_version===2)return boundedV2Receipt(raw);
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
  { resources, dataDir, integrity, reportFile, onFailure, profile="", caseID="" },
  execute = execFileSync,
) {
  if(profile!==""||caseID!=="")selectedV2(profile,caseID);
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
  const bundle = path.join(resources, profile?"sandbox-v2":"sandbox");
  let output,
    executionFailed = false;
  try {
    output = execute(
      path.join(resources, "bin", executableName("foundry-vm-acceptance")),
      ["--native-vm-acceptance", "--bundle", bundle, "--data-dir", dataDir,...(profile?["--profile",profile,"--case",caseID]:[])],
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
  const manifestDigest = resourceDigest(path.join(bundle, "manifest.json"));
  if ((executionFailed || !receipt.passed) && onFailure) {
    onFailure(failureDiagnostic(receipt, dataDir, manifestDigest));
  }
  assert.equal(executionFailed, false, "native harness returned nonzero");
  if(profile)assertNativeVMReceiptV2(receipt,manifestDigest,profile,caseID);
  else assertNativeVMReceipt(receipt, manifestDigest);
  return receipt;
}
// Each attempt owns a distinct directory and receipt. Stop at the first failed
// native attempt; never retry, replace its evidence or run this at app startup.
function runNativeV2Matrix(options,execute=execFileSync){
  assert.ok(!fs.existsSync(options.reportFile),"fresh matrix receipt required");
  const cases=[];
  for(const row of nativeV2Cases()){
    const name=row.profile.replace(/^c4_/,"").replace(/_model_v2$/,"")+"-"+row.case;
    const report=path.join(path.dirname(options.reportFile),"native-vm-v2-"+name+".json");
    const receipt=runNativeVMSmoke({...options,profile:row.profile,caseID:row.case,dataDir:options.dataDir+"-"+name,reportFile:report},execute);
    cases.push({...row,report,manifest_digest:receipt.manifest_digest});
  }
  assert.equal(new Set(cases.map(r=>r.manifest_digest)).size,1,"matrix runtime changed");
  const result={schema_version:2,kind:"foundry-native-vm-v2-matrix",passed:true,models:"local_protocol_fixture/not_real_provider",cases};
  fs.writeFileSync(options.reportFile,JSON.stringify(result)+"\n",{flag:"wx",mode:0o600});return result;
}
// Diagnostic-only projection. Never used for admission, evidence or cleanup.
function failureDiagnostic(receipt, dataDir, manifestDigest) {
  const stages = [
    "admission",
    "reserve",
    "bundle",
    "repository",
    "setup",
    "dependencies",
    "inspection",
    "create",
    "readiness",
    "baseline",
    "plan",
    "execute",
    "verify",
    "deliver",
    "complete",
  ];
  const stage = stages.includes(receipt.stage) ? receipt.stage : "unknown";
  const diagnostic = {
    kind: "foundry-native-vm-failure",
    stage,
    failure_code:
      receipt.failure_code === stage + "_failed"
        ? receipt.failure_code
        : "other_failure",
    terminal_status: "absent",
    terminals: [],
  };
  try {
    if (!fs.existsSync(dataDir)) return diagnostic;
    const root = path.join(dataDir, "instances");
    for (const dir of [dataDir, root]) {
      const stat = fs.lstatSync(dir);
      assert.ok(stat.isDirectory() && !stat.isSymbolicLink());
    }
    const entries = fs.readdirSync(root, { withFileTypes: true });
    assert.ok(entries.length <= 4);
    const terminals = [];
    for (const entry of entries) {
      assert.match(entry.name, hash);
      assert.ok(entry.isDirectory() && !entry.isSymbolicLink());
      const dir = path.join(root, entry.name);
      assert.ok(!fs.lstatSync(dir).isSymbolicLink());
      const file = path.join(dir, "terminal.json");
      let stat;
      try {
        stat = fs.lstatSync(file);
      } catch (e) {
        if (e.code === "ENOENT") continue;
        throw e;
      }
      assert.ok(stat.isFile() && !stat.isSymbolicLink() && stat.size <= 8192);
      const fd = fs.openSync(
        file,
        fs.constants.O_RDONLY | (fs.constants.O_NOFOLLOW || 0),
      );
      let terminal;
      try {
        const opened = fs.fstatSync(fd);
        assert.ok(
          opened.isFile() && opened.ino === stat.ino && opened.dev === stat.dev,
        );
        const buffer = Buffer.alloc(8193);
        const length = fs.readSync(fd, buffer, 0, buffer.length, 0);
        assert.ok(length <= 8192);
        terminal = JSON.parse(buffer.subarray(0, length).toString("utf8"));
      } finally {
        fs.closeSync(fd);
      }
      if(receipt.schema_version===2){
        keys(terminal,["schema_version","id","stage","operation_id","mission_id","owner","name","target","profile_digest","policy_digest","input_digest","dependency_digest","candidate_digest","request_digest",...(Object.hasOwn(terminal,"native")?["native"]:[])]);
        assert.equal(terminal.schema_version,2);assert.equal(terminal.id,entry.name);assert.equal(terminal.stage,"terminal");
        keys(terminal.target,["profile_id","identity","runtime_inventory_digest"]);
        assert.equal(terminal.target.profile_id,receipt.v2.profile_id);assert.equal(terminal.target.identity.manifest_digest,manifestDigest);
        for(const k of ["profile_digest","policy_digest","input_digest","dependency_digest","candidate_digest","request_digest"])assert.match(terminal[k],/^(?:[a-f0-9]{64})?$/);
      } else {
        assert.equal(terminal.schema_version, 1);
        assert.equal(terminal.ID, entry.name);
        assert.equal(terminal.Stage, "terminal");
        assert.equal(terminal.ManifestDigest, manifestDigest);
      }
      assert.equal(receipt.manifest_digest, manifestDigest);
      const native = terminal.native;
      const optional = [
        "exit_code",
        "signal",
        "admission_stage",
        "native_error_code",
        "helper",
        "qemu",
        "stop_requested",
        "readiness",
        "readiness_prefix",
      ];
      keys(native, [
        "termination_confirmed",
        "diagnostic_status",
        ...optional.filter((k) => Object.hasOwn(native, k)),
      ]);
      assert.equal(typeof native.termination_confirmed, "boolean");
      assert.ok(
        [
          "absent",
          "valid",
          "rejected",
          "overflow",
          "unrecognized",
          "read_failed",
          "incomplete",
        ].includes(native.diagnostic_status),
      );
      const facts = {
        termination_confirmed: native.termination_confirmed,
        diagnostic_status: native.diagnostic_status,
      };
      if (Object.hasOwn(native, "stop_requested")) {
        assert.equal(typeof native.stop_requested, "boolean");
        facts.stop_requested = native.stop_requested;
      }
      if (Object.hasOwn(native, "readiness")) {
        assert.ok(
          [
            "matched",
            "eof",
            "short_read",
            "read_failed",
            "deadline",
            "canceled",
            "marker_mismatch",
          ].includes(native.readiness),
        );
        facts.readiness = native.readiness;
      }
      if (Object.hasOwn(native, "readiness_prefix")) {
        assert.equal(native.readiness, "marker_mismatch");
        assert.ok(
          ["unknown", "whpx_clock_query_prefix", "ready_crlf"].includes(
            native.readiness_prefix,
          ),
        );
        facts.readiness_prefix = native.readiness_prefix;
      }
      for (const key of ["exit_code", "native_error_code", "signal"]) {
        if (!Object.hasOwn(native, key)) continue;
        const n = native[key];
        assert.ok(
          Number.isInteger(n) &&
            n >= (key === "signal" ? 1 : 0) &&
            n <= (key === "signal" ? 127 : 4294967295),
        );
        facts[key] = n;
      }
      assert.ok(
        !(Object.hasOwn(facts, "exit_code") && Object.hasOwn(facts, "signal")),
      );
      if (Object.hasOwn(native, "admission_stage")) {
        assert.ok(
          [
            "helper_spawn",
            "running",
            "create_job",
            "job_limits",
            "stdio",
            "handle_list",
            "process_arguments",
            "create_process",
            "created_suspended",
            "assign_job",
            "context_admission",
            "resume_thread",
          ].includes(native.admission_stage),
        );
        facts.admission_stage = native.admission_stage;
      }
      if (Object.hasOwn(native, "qemu")) {
        assert.equal(native.termination_confirmed, true);
        assert.ok(native.exit_code > 0);
        assert.equal(native.diagnostic_status, "valid");
        keys(native.qemu, ["codes"]);
        const codes = native.qemu.codes;
        assert.ok(
          Array.isArray(codes) && codes.length > 0 && codes.length <= 8,
        );
        assert.equal(new Set(codes).size, codes.length);
        assert.ok(
          codes.every((code) =>
            [
              "machine_property",
              "whpx_partition",
              "whpx_initialization",
              "firmware",
              "character_device",
              "cpu_model",
              "memory_allocation",
              "option",
              "kernel_load",
              "initrd_load",
              "rom_load",
              "device_configuration",
              "event_loop",
              "memory_mapping",
              "host_socket",
              "resource_access",
              "crypto_initialization",
            ].includes(code),
          ),
        );
        facts.qemu = { codes: [...codes] };
      }
      terminals.push(facts);
    }
    diagnostic.terminals = terminals;
    diagnostic.terminal_status = terminals.length ? "available" : "absent";
  } catch {
    diagnostic.terminal_status = "rejected";
    diagnostic.terminals = [];
  }
  return diagnostic;
}
// Build/acceptance diagnostic only. Query the system API; do not create a
// partition, enable a feature, reboot, or substitute for the actual guest gate.
function probeWindowsHypervisor(probe, systemRoot, execute = execFileSync) {
  const base = {
    kind: "foundry-windows-hypervisor-preflight",
    available: false,
  };
  let output;
  try {
    assert.ok(
      typeof probe === "string" &&
        /^[A-Za-z]:\\/.test(probe) &&
        !/[\x00\r\n]/.test(probe) &&
        path.win32.basename(probe) === "foundry-whpx-probe.exe",
    );
    assert.ok(
      typeof systemRoot === "string" &&
        /^[A-Za-z]:\\/.test(systemRoot) &&
        !/[\x00\r\n]/.test(systemRoot),
    );
    const system32 = path.win32.join(systemRoot, "System32");
    output = execute(probe, [], {
      encoding: "utf8",
      timeout: 15000,
      maxBuffer: 4096,
      windowsHide: true,
      env: {
        SystemRoot: systemRoot,
        WINDIR: systemRoot,
        PATH: system32,
      },
      stdio: ["ignore", "pipe", "pipe"],
    });
  } catch (error) {
    let stage = "unknown";
    const progress = Buffer.isBuffer(error.stderr)
      ? error.stderr.toString("utf8")
      : error.stderr;
    if (typeof progress === "string" && Buffer.byteLength(progress) <= 256) {
      const records = progress.trim().split(/\r?\n/);
      const stages = ["started", "queried"];
      if (
        records.length >= 1 &&
        records.length <= stages.length &&
        records.every((line, i) => line === "foundry-whpx:" + stages[i])
      ) {
        stage = stages[records.length - 1];
      }
    }
    const failure =
      error.code === "ETIMEDOUT"
        ? "timeout"
        : Number.isInteger(error.status) && error.status !== 0
          ? "exit_nonzero"
          : "launch_failed";
    return { ...base, status: "probe_failed", failure, stage };
  }
  try {
    assert.ok(typeof output === "string" && Buffer.byteLength(output) <= 4096);
    const values = JSON.parse(output.replace(/^\uFEFF/, ""));
    assert.ok(
      Array.isArray(values) &&
        values.length === 4 &&
        values.every(Number.isSafeInteger),
    );
    const [api, hr, bytes, present] = values;
    assert.ok(
      [0, 1].includes(api) &&
        hr >= 0 &&
        hr <= 4294967295 &&
        bytes >= 0 &&
        bytes <= 8 &&
        present >= 0,
    );
    if (api === 0) {
      assert.deepEqual(values, [0, 0, 0, 0]);
      return { ...base, status: "api_unavailable" };
    }
    if (hr !== 0) return { ...base, status: "query_failed", hresult: hr };
    assert.ok(bytes === 4 && [0, 1].includes(present));
    return {
      ...base,
      available: present === 1,
      status: present === 1 ? "available" : "hypervisor_absent",
    };
  } catch {
    return { ...base, status: "invalid_response" };
  }
}
module.exports = {
  assertNativeVMReceipt,
  runNativeVMSmoke,
  runNativeV2Matrix,
  probeWindowsHypervisor,
};
if (require.main === module)
  (async () => {
    if (
      process.argv.length === 4 &&
      process.argv[2] === "--windows-hypervisor-preflight"
    ) {
      const result =
        process.platform === "win32" && process.arch === "x64"
          ? probeWindowsHypervisor(process.argv[3], process.env.SystemRoot)
          : {
              kind: "foundry-windows-hypervisor-preflight",
              available: false,
              status: "unsupported_host",
            };
      process.stdout.write(JSON.stringify(result) + "\n");
      process.exitCode = result.available ? 0 : 1;
      return;
    }
    const args=process.argv.slice(2);
    const matrix=args.length===1&&args[0]==="--v2-matrix";
    let profile="",caseID="";
    if(!matrix&&args.length){assert.equal(args.length,4);assert.equal(args[0],"--profile");assert.equal(args[2],"--case");profile=args[1];caseID=args[3];selectedV2(profile,caseID)}
    const resources = process.env.FOUNDRY_INSTALLED_RESOURCES;
    const { extractFile } = await import("@electron/asar");
    const { manifestSHA256: integrity } = JSON.parse(
      extractFile(path.join(resources, "app.asar"), "resource-integrity.json"),
    );
    const receipt = (matrix?runNativeV2Matrix:runNativeVMSmoke)({
      profile,caseID,
      resources,
      dataDir: process.env.FOUNDRY_VM_ACCEPTANCE_DATA,
      reportFile: process.env.FOUNDRY_VM_ACCEPTANCE_REPORT,
      integrity,
      onFailure: (diagnostic) =>
        process.stderr.write(JSON.stringify(diagnostic) + "\n"),
    });
    process.stdout.write(JSON.stringify(receipt) + "\n");
  })().catch(() => {
    process.stderr.write(
      "Native VM acceptance failed; no artifact may be published.\n",
    );
    process.exitCode = 1;
  });
