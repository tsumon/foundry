"use strict";
// Release-only cross-version controller acceptance. Inputs are two installed
// resource trees and an independently seeded, synthetic old-version data root.
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const { DatabaseSync } = require("node:sqlite");
const asar = require("@electron/asar");

const sha = (value) => crypto.createHash("sha256").update(value).digest("hex");
const host = process.platform === "win32" ? "windows/amd64" : "darwin/arm64";
const hashJSON = (value) => sha(JSON.stringify(value));
// Exact additive DDL observed between pinned alpha.3 and the stable candidate.
// No prior object may change and no other object may appear.
const allowedMigration = new Map([
  ["table:model_acceptance_inference_budgets", "CREATE TABLE model_acceptance_inference_budgets(operation_id TEXT PRIMARY KEY REFERENCES model_acceptance_claims(operation_id), mission_id TEXT NOT NULL REFERENCES missions(id), revision INTEGER NOT NULL, binding_digest TEXT NOT NULL)"],
  ["table:model_acceptance_inference_sends", "CREATE TABLE model_acceptance_inference_sends(operation_id TEXT PRIMARY KEY REFERENCES model_acceptance_dispatches(operation_id),claim_sequence INTEGER NOT NULL UNIQUE REFERENCES model_acceptance_claims(sequence),started_at TEXT NOT NULL)"],
  ["trigger:inference_budget_no_delete", "CREATE TRIGGER inference_budget_no_delete BEFORE DELETE ON model_acceptance_inference_budgets BEGIN SELECT RAISE(ABORT,'immutable inference budget'); END"],
  ["trigger:inference_budget_no_update", "CREATE TRIGGER inference_budget_no_update BEFORE UPDATE ON model_acceptance_inference_budgets BEGIN SELECT RAISE(ABORT,'immutable inference budget'); END"],
  ["trigger:inference_send_no_delete", "CREATE TRIGGER inference_send_no_delete BEFORE DELETE ON model_acceptance_inference_sends BEGIN SELECT RAISE(ABORT,'immutable inference send'); END"],
  ["trigger:inference_send_no_update", "CREATE TRIGGER inference_send_no_update BEFORE UPDATE ON model_acceptance_inference_sends BEGIN SELECT RAISE(ABORT,'immutable inference send'); END"],
]);

function assertSchemaEvolution(before, after, candidate) {
  const key = (row) => row.type + ":" + row.name;
  const original = new Map(before.map((row) => [key(row), row.sql]));
  const actual = new Map(after.map((row) => [key(row), row.sql]));
  assert.equal(original.size, before.length, "Duplicate initial schema object");
  assert.equal(actual.size, after.length, "Duplicate resulting schema object");
  assert.equal(actual.size, original.size + (candidate ? allowedMigration.size : 0),
    "Unreviewed schema object added or removed");
  for (const [name, sql] of original) assert.equal(actual.get(name), sql, "Historical schema changed: " + name);
  for (const [name, sql] of allowedMigration) {
    assert.equal(original.has(name), false, "Migration already in old source: " + name);
    if (candidate) assert.equal(actual.get(name), sql, "Unexpected migration DDL: " + name);
  }
}

function packagedRuntime(resources, output) {
  assert(path.isAbsolute(resources) && path.isAbsolute(output));
  const archive = path.join(resources, "app.asar");
  const header = JSON.parse(asar.getRawHeader(archive).headerString);
  fs.mkdirSync(output, { recursive: false, mode: 0o700 });
  for (const name of ["runtime.cjs", "resource-integrity.json", ...(header.files?.["runtime-inventory-v2.cjs"] ? ["runtime-inventory-v2.cjs"] : [])]) {
    const entry = header.files?.[name];
    assert(entry && !entry.unpacked && !entry.link && entry.size < 1_000_000,
      `Missing sealed ${name}`);
    const bytes = asar.extractFile(archive, name);
    assert.equal(bytes.length, entry.size);
    assert.equal(sha(bytes), entry.integrity?.hash, `ASAR integrity ${name}`);
    fs.writeFileSync(path.join(output, name), bytes, { flag: "wx", mode: 0o600 });
  }
  const runtime = require(path.join(output, "runtime.cjs"));
  const { manifestSHA256 } = require(path.join(output, "resource-integrity.json"));
  runtime.verifyResources(resources, manifestSHA256);
  return { resources, runtime, integrity: manifestSHA256,
    manifest: sha(fs.readFileSync(path.join(resources, "resources-manifest.json"))) };
}

function inventory(root) {
  const rows = [];
  function walk(file) {
    const stat = fs.lstatSync(file);
    assert(!stat.isSymbolicLink() && (stat.isDirectory() || stat.isFile()));
    if (stat.isFile()) assert.equal(stat.nlink, 1);
    const row = { path: path.relative(root, file), type: stat.isDirectory() ? "dir" : "file" };
    if (stat.isFile()) row.sha256 = sha(fs.readFileSync(file));
    if (process.platform !== "win32") row.mode = stat.mode & 0o777;
    rows.push(row);
    if (stat.isDirectory())
      for (const name of fs.readdirSync(file).sort()) walk(path.join(file, name));
  }
  walk(root);
  return rows;
}

function exactCopy(source, target) {
  const before = inventory(source);
  assert(!fs.existsSync(target), "Backup destination must be fresh");
  if (process.platform === "win32") {
    const helper = process.env.FOUNDRY_UPGRADE_COPY;
    assert(helper && path.isAbsolute(helper) && fs.statSync(helper).isFile(),
      "Native Windows owner/DACL copy helper required");
    const receipt = JSON.parse(execFileSync(helper, [source, target], {
      encoding: "utf8", windowsHide: true, timeout: 120000,
    }));
    assert.equal(receipt.result, "PASS");
    assert.equal(receipt.owner_dacl_exact, true);
  } else {
    for (const row of before) {
      const destination = path.join(target, row.path);
      if (row.type === "dir") fs.mkdirSync(destination, { mode: row.mode });
      else fs.copyFileSync(path.join(source, row.path), destination, fs.constants.COPYFILE_EXCL);
      fs.chmodSync(destination, row.mode);
    }
  }
  assert.deepEqual(inventory(source), before, "Backup source changed");
  assert.deepEqual(inventory(target), before, "Backup copy differs");
}

function snapshot(fixture) {
  const data = path.join(fixture.data_root, "coding");
  const dbFile = path.join(data, "foundry.db");
  for (const ext of ["-wal", "-shm", "-journal"])
    assert(!fs.existsSync(dbFile + ext), "Snapshot requires a closed, checkpointed database");
  // SQLite's read-only WAL open can create -shm/-wal beside the source. Query
  // a private copy so acceptance never changes the frozen data it inspects.
  const scratch = fs.mkdtempSync(path.join(path.dirname(fixture.data_root), "upgrade-snapshot-"));
  const copy = path.join(scratch, "foundry.db");
  fs.copyFileSync(dbFile, copy, fs.constants.COPYFILE_EXCL);
  const db = new DatabaseSync(copy, { readOnly: true });
  try {
    assert.equal(db.prepare("PRAGMA integrity_check").get().integrity_check, "ok");
    assert.deepEqual(db.prepare("PRAGMA foreign_key_check").all(), []);
    const row = db.prepare("SELECT id,revision,state,CAST(data AS TEXT) AS data FROM missions WHERE id=?").get(fixture.mission_id);
    assert(row, "Synthetic mission missing");
    const mission = JSON.parse(row.data);
    const invocations = db.prepare("SELECT * FROM invocations WHERE mission_id=? ORDER BY ordinal").all(fixture.mission_id);
    assert.equal(mission.budget.used_calls, 2);
    assert.equal(mission.budget.max_calls, 7);
    assert.equal(mission.budget.usage_unknown, true);
    assert.equal(invocations.length, 2);
    assert(invocations.every((entry) => entry.status === "outcome_unknown"));
    assert.equal(mission.c4_operations[0].result.class, "passed");
    assert.equal(mission.c4_operations[0].cleanup.at(-1).state, "confirmed");
    return {
      mission: hashJSON(row), invocations: hashJSON(invocations),
      snapshot: hashJSON(mission.model_snapshot), baseline: hashJSON(mission.c4_operations),
      budget: mission.budget,
      model_accounts: hashJSON(db.prepare("SELECT * FROM model_accounts ORDER BY id").all()),
      credential_versions: hashJSON(db.prepare("SELECT * FROM model_credential_versions ORDER BY account_id,version").all()),
      model_routes: hashJSON(db.prepare("SELECT * FROM model_routes ORDER BY id").all()),
      evidence: inventory(path.join(data, "evidence")),
      missions: inventory(path.join(data, "missions")),
      schema: db.prepare("SELECT type,name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name").all(),
      migration_table_rows: Object.fromEntries(
        ["model_acceptance_inference_budgets", "model_acceptance_inference_sends"].map((name) => [name,
          db.prepare("SELECT count(*) AS n FROM sqlite_master WHERE type='table' AND name=?").get(name).n === 1
            ? db.prepare("SELECT count(*) AS n FROM " + name).get().n : null]),
      ),
    };
  } finally { db.close(); fs.rmSync(scratch, { recursive: true }); }
}

function assertFixture(fixture, fixtureFile) {
  assert(path.isAbsolute(fixture.data_root) && path.isAbsolute(fixture.archive));
  assert.equal(path.dirname(fixture.data_root), path.dirname(fixtureFile), "Fixture must own its data root");
  assert(fixture.archive.startsWith(path.join(fixture.data_root, "coding", "vm-bundles") + path.sep),
    "Retained archive must belong to synthetic coding data");
  assert.equal(fixture.baseline, "synthetic journal only; no test execution");
  assert.equal(fixture.credential_reads, 0);
  assert.equal(fixture.vm_operations, 0);
}

async function inspect(child, fixture, expected, resources) {
  const request = (url, options = {}) => fetch(child.ready.url + url, {
    ...options, signal: AbortSignal.timeout(10000),
    headers: { "X-Foundry-Desktop": child.capability, Origin: child.ready.url, ...options.headers },
  });
  assert.equal((await fetch(child.ready.url + "/api/health", { signal: AbortSignal.timeout(5000) })).status, 403);
  const session = await request("/api/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
  assert.equal(session.status, 200);
  const Cookie = session.headers.get("set-cookie").split(";")[0];
  const get = async (url) => {
    const response = await request(url, { headers: { Cookie } });
    assert.equal(response.status, 200, url);
    return response.json();
  };
  const health = await get("/api/health");
  assert.equal(health.status, expected === "ok" ? "ok" : "recovery_limited");
  if (expected !== "ok") {
    assert.equal(health.read_only, true);
    assert.equal(health.recovery_error, expected);
    for (const name of ["planning", "execution", "provider_login", "account_inference", "model_directory_probe", "automatic_paid_fallback", "c4_real_execution"])
      assert.equal(health.capabilities[name], false, name);
  } else for (const name of ["provider_login", "account_inference", "automatic_paid_fallback"])
    assert.equal(health.capabilities[name], false, name);
  const { mission } = await get("/api/missions/" + fixture.mission_id);
  assert.deepEqual(mission.c4_profile_binding?.profile?.embedded_vm, fixture.identity,
    "Mission must expose its exact frozen VM identity");
  const catalog = await get("/api/coding-tasks");
  assert.equal(catalog.profile?.embedded_vm?.manifest_digest,
    sha(fs.readFileSync(path.join(resources, "sandbox", "manifest.json"))),
    "Default VM identity must match installed package bytes");
  assert.notEqual(catalog.profile?.embedded_vm?.manifest_digest, fixture.identity.manifest_digest,
    "Installed candidate default must differ from the retained fixture runtime");
  assert(await get("/api/missions/" + fixture.mission_id + "/invocations"));
  const page = await request("/", { headers: { Cookie } });
  assert.equal(page.status, 200);
  assert((await page.text()).includes("<html"));
  if (expected !== "ok") for (const route of ["/api/missions/" + fixture.mission_id + "/plan", "/api/model-authorizations/start", "/api/future-mutation"]) {
    const response = await request(route, { method: "POST", headers: { Cookie, "Content-Type": "application/json" }, body: "{}" });
    assert.equal(response.status, 503);
    assert.equal((await response.json()).error.code, "recovery_read_only");
  }
  assert.deepEqual(fs.readdirSync(path.join(fixture.data_root, "coding", "vm-instances")), []);
  return { status: health.status, recovery_error: health.recovery_error ?? null,
    retained_manifest_digest: fixture.identity.manifest_digest,
    installed_default_manifest_digest: catalog.profile.embedded_vm.manifest_digest };
}

async function run(oldResources, newResources, fixtureFile, reportFile) {
  assert(host === "darwin/arm64" || host === "windows/amd64", "Native supported host required");
  for (const file of [oldResources, newResources, fixtureFile, reportFile]) assert(path.isAbsolute(file));
  assert.notEqual(path.resolve(oldResources), path.resolve(newResources));
  assert(!fs.existsSync(reportFile), "Fresh report required");
  const fixture = JSON.parse(fs.readFileSync(fixtureFile, "utf8"));
  assertFixture(fixture, fixtureFile);
  const work = reportFile + ".work";
  fs.mkdirSync(work, { mode: 0o700 });
  const old = packagedRuntime(oldResources, path.join(work, "upgrade-old-module"));
  const current = packagedRuntime(newResources, path.join(work, "upgrade-current-module"));
  const steps = [];
  const initial = snapshot(fixture);
  function retained(candidate) {
    const actual = snapshot(fixture);
    const { schema: beforeSchema, migration_table_rows: beforeTables, ...before } = initial;
    const { schema: afterSchema, migration_table_rows: afterTables, ...after } = actual;
    assert.deepEqual(after, before, "Frozen task/account/credential history changed");
    assertSchemaEvolution(beforeSchema, afterSchema, candidate);
    for (const name of Object.keys(beforeTables))
      assert.equal(afterTables[name], candidate ? 0 : null, "Migration table contains data");
  }
  async function start(version, name, expected = "ok") {
    let child;
    try {
      child = await version.runtime.startBackend({ resources: version.resources, dataRoot: fixture.data_root, mode: "coding", integrity: version.integrity });
      const result = await inspect(child, fixture, expected, version.resources);
      await child.stop();
      assert.equal(child.process.exitCode, 0);
      retained(version === current);
      steps.push({ name, ...result });
    } finally { if (child) await child.stop(); }
  }
  await start(old, "old-start");
  const checkpoint = path.join(work, "upgrade-consistent-checkpoint");
  exactCopy(fixture.data_root, checkpoint);
  await start(current, "new-start");
  await start(current, "new-restart");
  const absent = path.join(work, "upgrade-archive-held-out");
  fs.renameSync(fixture.archive, absent);
  try { await start(current, "missing-retained-runtime", "c4_runtime_mismatch"); }
  finally { fs.renameSync(absent, fixture.archive); }
  await start(current, "exact-retained-runtime-restored");
  const kernel = path.join(fixture.archive, "kernel");
  fs.writeFileSync(kernel, "TAMPERED SYNTHETIC FIXTURE\n");
  try {
    await assert.rejects(() => current.runtime.startBackend({ resources: current.resources, dataRoot: fixture.data_root, mode: "coding", integrity: current.integrity }), /Controller exited before readiness/);
    retained(true);
    steps.push({ name: "tampered-retained-runtime", startup_rejected: true });
  } finally {
    fs.renameSync(fixture.data_root, path.join(work, "upgrade-failed-data-preserved"));
    exactCopy(checkpoint, fixture.data_root);
  }
  retained(false);
  await start(old, "rollback-from-consistent-checkpoint");
  await start(current, "upgrade-after-restore");
  const report = { result: "PASS", source_version: "v0.1.0-alpha.3", scope: "packaged controller and synthetic journal; no provider or VM execution", synthetic: true, host,
    old_manifest_sha256: old.manifest, current_manifest_sha256: current.manifest,
    frozen_history_sha256: hashJSON(initial), steps, requested_provider_calls: 0, requested_vm_calls: 0, requested_model_calls: 0 };
  fs.writeFileSync(reportFile, JSON.stringify(report, null, 2) + "\n", { flag: "wx", mode: 0o600 });
  return report;
}

async function probeOld(resources, fixtureFile, output) {
  assert(path.isAbsolute(resources) && path.isAbsolute(fixtureFile) && path.isAbsolute(output));
  assert(!fs.existsSync(output));
  const fixture = JSON.parse(fs.readFileSync(fixtureFile, "utf8"));
  assertFixture(fixture, fixtureFile);
  const before = snapshot(fixture);
  const work = output + ".work";
  fs.mkdirSync(work, { mode: 0o700 });
  const old = packagedRuntime(resources, path.join(work, "upgrade-installed-old-module"));
  let child;
  try {
    child = await old.runtime.startBackend({ resources, dataRoot: fixture.data_root, mode: "coding", integrity: old.integrity });
    await inspect(child, fixture, "ok", resources);
    await child.stop();
    assert.equal(child.process.exitCode, 0);
    assert.deepEqual(snapshot(fixture), before);
  } finally { if (child) await child.stop(); }
  const receipt = { result: "PASS", source_version: "v0.1.0-alpha.3", host,
    old_manifest_sha256: old.manifest, frozen_history_sha256: hashJSON(before) };
  fs.writeFileSync(output, JSON.stringify(receipt, null, 2) + "\n", { flag: "wx", mode: 0o600 });
  return receipt;
}

function verifyFresh(resources, output) {
  assert(path.isAbsolute(resources) && path.isAbsolute(output));
  assert(!fs.existsSync(output));
  const work = output + ".work";
  fs.mkdirSync(work, { mode: 0o700 });
  const installed = packagedRuntime(resources, path.join(work, "upgrade-fresh-module"));
  const receipt = { result: "PASS", scope: "fresh candidate install resource integrity; no VM execution",
    host, manifest_sha256: installed.manifest };
  fs.writeFileSync(output, JSON.stringify(receipt, null, 2) + "\n", { flag: "wx", mode: 0o600 });
  return receipt;
}

if (require.main === module) {
  const command = process.argv[2] === "--probe-old"
    ? probeOld(...process.argv.slice(3))
    : process.argv[2] === "--verify-fresh"
      ? Promise.resolve(verifyFresh(...process.argv.slice(3)))
    : run(...process.argv.slice(2));
  command.then((report) => console.log(JSON.stringify(report)), (error) => {
    console.error(error);
    process.exitCode = 1;
  });
}
module.exports = { packagedRuntime, inventory, exactCopy, snapshot, allowedMigration, assertSchemaEvolution, probeOld, verifyFresh, run };
