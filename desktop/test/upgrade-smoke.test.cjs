"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { finished } = require("node:stream/promises");
const { exactCopy, inventory, snapshot, allowedMigration, assertSchemaEvolution,priorRelease,assertFixture } = require("../upgrade-smoke.cjs");

test("only the reviewed additive inference schema migration is accepted", () => {
  const original = [{ type: "table", name: "missions", sql: "CREATE TABLE missions(id TEXT)" }];
  const migrated = [...original, ...[...allowedMigration].map(([key, sql]) => {
    const [type, name] = key.split(":");
    return { type, name, sql };
  })];
  assert.equal(allowedMigration.size, 7);
  assert.doesNotThrow(() => assertSchemaEvolution(original, original, false));
  assert.doesNotThrow(() => assertSchemaEvolution(original, migrated, true));
  assert.throws(() => assertSchemaEvolution(original, migrated, false), /schema/);
  assert.throws(() => assertSchemaEvolution(original, [...migrated, { type: "table", name: "unknown", sql: "CREATE TABLE unknown(id TEXT)" }], true), /schema/);
  assert.throws(() => assertSchemaEvolution(original, migrated.map((row) => row.name === "inference_budget_no_update" ? { ...row, sql: row.sql + " --changed" } : row), true), /migration DDL/);
  assert.throws(() => assertSchemaEvolution(original, [{ ...original[0], sql: "CREATE TABLE missions(id BLOB)" }, ...migrated.slice(1)], true), /Historical schema changed/);
});

test("consistent backup copies exact files and rejects links and reused destinations", (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-upgrade-copy-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const source = path.join(root, "source"), target = path.join(root, "target");
  if (process.platform === "win32") {
    const helper = process.env.FOUNDRY_UPGRADE_COPY;
    assert(helper && path.isAbsolute(helper), "Native Windows private fixture helper required");
    execFileSync(helper, ["--private-dir", source], { windowsHide: true, timeout: 120000 });
  } else {
    fs.mkdirSync(source, { mode: 0o700 });
  }
  fs.mkdirSync(path.join(source, "evidence"), { mode: 0o700 });
  fs.writeFileSync(path.join(source, "evidence", "sealed"), "synthetic evidence", { mode: 0o600 });
  exactCopy(source, target);
  assert.deepEqual(inventory(target), inventory(source));
  assert.throws(() => exactCopy(source, target), /fresh/);
  fs.symlinkSync(path.join(source, "evidence"), path.join(source, "linked-evidence"), "junction");
  assert.throws(() => exactCopy(source, path.join(root, "another")), /assert|symlink/i);
});

test("stopped synthetic ledger snapshot preserves sealed budget and rejects incomplete journal", (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-upgrade-snapshot-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const data = path.join(root, "coding");
  fs.mkdirSync(path.join(data, "evidence"), { recursive: true });
  fs.mkdirSync(path.join(data, "missions"));
  const { DatabaseSync } = require("node:sqlite");
  const dbFile = path.join(data, "foundry.db"), db = new DatabaseSync(dbFile);
  db.exec("CREATE TABLE missions(id TEXT, revision INTEGER, state TEXT, data TEXT); CREATE TABLE invocations(mission_id TEXT, ordinal INTEGER, status TEXT); CREATE TABLE model_accounts(id TEXT, name TEXT); CREATE TABLE model_credential_versions(account_id TEXT, version INTEGER, secret_ref TEXT); CREATE TABLE model_routes(id TEXT, account_id TEXT)");
  const mission = { budget: { max_calls: 7, used_calls: 2, usage_unknown: true }, model_snapshot: { digest: "sealed" }, c4_operations: [{ result: { class: "passed" }, cleanup: [{ state: "confirmed" }] }] };
  db.prepare("INSERT INTO missions VALUES(?,?,?,?)").run("synthetic", 4, "blocked", JSON.stringify(mission));
  db.prepare("INSERT INTO invocations VALUES(?,?,?)").run("synthetic", 1, "outcome_unknown");
  db.prepare("INSERT INTO invocations VALUES(?,?,?)").run("synthetic", 2, "outcome_unknown");
  db.prepare("INSERT INTO model_accounts VALUES(?,?)").run("synthetic-account", "unchanged");
  db.prepare("INSERT INTO model_credential_versions VALUES(?,?,?)").run("synthetic-account", 1, "inert-secret-reference");
  db.prepare("INSERT INTO model_routes VALUES(?,?)").run("synthetic-route", "synthetic-account");
  db.close();
  const fixture = { data_root: root, mission_id: "synthetic" };
  const first = snapshot(fixture);
  assert.deepEqual(snapshot(fixture), first);
  assert.doesNotMatch(JSON.stringify(first), /inert-secret-reference/);
  const changed = new DatabaseSync(dbFile);
  changed.prepare("UPDATE model_accounts SET name=? WHERE id=?").run("changed", "synthetic-account");
  changed.close();
  assert.notEqual(snapshot(fixture).model_accounts, first.model_accounts);
  const changedCredential = new DatabaseSync(dbFile);
  changedCredential.prepare("UPDATE model_credential_versions SET secret_ref=? WHERE account_id=?").run("other-inert-reference", "synthetic-account");
  changedCredential.close();
  assert.notEqual(snapshot(fixture).credential_versions, first.credential_versions);
  const migrationDb = new DatabaseSync(dbFile);
  migrationDb.exec("CREATE TABLE model_acceptance_inference_budgets(operation_id TEXT); CREATE TABLE model_acceptance_inference_sends(operation_id TEXT)");
  migrationDb.close();
  assert.deepEqual(snapshot(fixture).migration_table_rows,
    { model_acceptance_inference_budgets: 0, model_acceptance_inference_sends: 0, dependency_admissions:null });
  const writtenMigration = new DatabaseSync(dbFile);
  writtenMigration.prepare("INSERT INTO model_acceptance_inference_budgets VALUES(?)").run("unexpected");
  writtenMigration.close();
  assert.equal(snapshot(fixture).migration_table_rows.model_acceptance_inference_budgets, 1);
  fs.writeFileSync(dbFile + "-wal", "incomplete");
  assert.throws(() => snapshot(fixture), /closed, checkpointed/);
});

test('stable supplement admits only its pinned identity and exact one-table migration',()=>{
 const stable=priorRelease('v0.1.0');assert.equal(stable.commit,'ab89b5a88f73755e91aaefa5b73b1dfd5c73fdb0');assert.equal(stable.installerSHA256,'5a770a31021d3914ad1b30a5597e0ec17e256c50f7e65c7565cfd21603f6ac7d');assert.throws(()=>priorRelease('arbitrary-old-version'));
 const old=[{type:'table',name:'missions',sql:'CREATE TABLE missions(id TEXT)'},...[...allowedMigration].filter(([key])=>key!=='table:dependency_admissions').map(([key,sql])=>({type:key.split(':')[0],name:key.split(':')[1],sql}))];
 const table={type:'table',name:'dependency_admissions',sql:'CREATE TABLE dependency_admissions (digest TEXT PRIMARY KEY, data BLOB NOT NULL)'};
 assert.doesNotThrow(()=>assertSchemaEvolution(old,[...old,table],true,'v0.1.0'));
 assert.throws(()=>assertSchemaEvolution(old,[...old,table,{type:'table',name:'unexpected',sql:'CREATE TABLE unexpected(x)'}],true,'v0.1.0'));
 assert.throws(()=>assertSchemaEvolution(old,[...old,{...table,sql:'CREATE TABLE dependency_admissions (digest BLOB)'}],true,'v0.1.0'));
 assert.throws(()=>assertSchemaEvolution(old,[...old,table],true,'v0.1.0-alpha.3'));
 assert.throws(()=>assertSchemaEvolution(old,[...old,table],false,'v0.1.0'));
 const root=path.resolve(os.tmpdir(),'foundry-synthetic-prior'),file=path.join(root,'fixture.json'),data=path.join(root,'data');
 const fixture={source_version:'v0.1.0',source_commit:stable.commit,data_root:data,archive:path.join(data,'coding','vm-bundles', 'a'.repeat(64)),baseline:'synthetic journal only; no test execution',credential_reads:0,vm_operations:0};
 assert.doesNotThrow(()=>assertFixture(fixture,file,'v0.1.0'));
 for(const changed of [{...fixture,source_commit:'0'.repeat(64)},{...fixture,source_version:'v0.1.0-alpha.3'},{...fixture,credential_reads:1},{...fixture,archive:path.join(root,'foreign-runtime')}])assert.throws(()=>assertFixture(changed,file,'v0.1.0'));
});

test("native Windows workflow pins public source and installer, then over-installs the same target", () => {
  const workflow = fs.readFileSync(path.join(__dirname, "../../.github/workflows/desktop.yml"), "utf8");
  const smoke = fs.readFileSync(path.join(__dirname, "../windows-installed-smoke.ps1"), "utf8");
  assert.match(workflow, /ref: 5237fc49bafe4c5d33d3d6fa117ac59c467ace16/);
  assert.match(workflow, /persist-credentials: false/);
  assert.match(workflow, /TestReleaseUpgradeSeed/);
  assert.match(workflow, /023bf7e0c95b3ec90e76e429b145e9fbbe0b1d52103ec82e5ac4eb3968188327/);
  assert.match(workflow, /upgrade-acceptance\.json/);
  assert.match(smoke, /--probe-old \$resources \$UpgradeFixture/);
  assert.match(smoke, /\$oldInstall = Start-Process -FilePath \$OldInstaller -ArgumentList @\('\/S', "\/D=\$installed"\)/);
  assert.match(smoke, /\$install = Start-Process -FilePath \$installer -ArgumentList @\('\/S', "\/D=\$installed"\)/);
  assert.match(smoke, /Candidate did not replace old installed app bytes/);
  assert.match(smoke, /--verify-fresh \$freshResources/);
  assert.match(smoke, /Fresh candidate uninstall left executable/);
  assert.match(smoke, /Fresh install or uninstall changed retained evidence/);
  assert.match(workflow, /fresh-candidate-integrity\.json/);
  assert.match(workflow, /go test -count=1 -run '\^TestWindowsUpgrade' \.\/desktop/);
  assert.match(workflow, /FOUNDRY_UPGRADE_COPY/);
  assert.doesNotMatch(workflow, /New-Item -ItemType Directory -Path \$seed/);
  const parsed = require("../node_modules/js-yaml").load(workflow);
  const steps = parsed.jobs["windows-installer"].steps;
  const helper = steps.findIndex((step) => step.id === "upgrade-copy");
  const nativeTests = steps.findIndex((step) => step.run === "./desktop/windows-test.ps1");
  const installed = steps.findIndex((step) => step.id === "installed-contract");
  assert(helper >= 0 && helper < nativeTests && nativeTests < installed);
  assert.match(steps[helper].run, /FOUNDRY_UPGRADE_COPY=\$helper.*GITHUB_ENV/);
  assert.match(fs.readFileSync(path.join(__dirname, "../windows-test.ps1"), "utf8"),
    /desktop\/test\/upgrade-smoke\.test\.cjs/);
  const seed = fs.readFileSync(path.join(__dirname, "upgrade-seed.go.txt"), "utf8");
  assert.match(seed, /platformfs\.PrivateDirectory\(root\)/);
  assert.match(seed, /platformfs\.PrivateDirectory\(p\)/);
});

test("sealed upgrade resource verification loads the descriptor module for dual resources", async (t) => {
  const { sandboxFixture, v2Fixture, loadDesktopFixtureModule } = require("./sandbox-fixture.cjs");
  const nativeHost = process.platform === "win32" ? "windows/amd64" : `${process.platform}/${process.arch}`;
  for (const host of ["darwin/arm64", "windows/amd64"]) {
    await t.test(host, async (t) => {
      const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "foundry-upgrade-v2-")));
      t.after(() => fs.rmSync(root, { recursive: true, force: true }));
      const resources = path.join(root, "resources"), app = path.join(root, "app"), extracted = path.join(root, "extracted");
      fs.mkdirSync(resources); fs.mkdirSync(app);
      sandboxFixture(path.join(resources, "sandbox"), host);
      v2Fixture(path.join(resources, "sandbox-v2"), host);
      const suffix = host === "windows/amd64" ? ".exe" : "";
      for (const name of ["bin/foundry" + suffix, "bin/foundry-acceptance" + suffix, "bin/foundry-vm-acceptance" + suffix, "web/index.html"]) {
        fs.mkdirSync(path.dirname(path.join(resources, name)), { recursive: true });
        fs.writeFileSync(path.join(resources, name), "inert");
      }
      require("../build-support.cjs").writeManifest(resources);
      for (const name of ["runtime.cjs", "runtime-inventory-v2.cjs"]) fs.copyFileSync(path.join(__dirname, "..", name), path.join(app, name));
      fs.writeFileSync(path.join(app, "resource-integrity.json"), JSON.stringify({ manifestSHA256: require("../runtime.cjs").resourceDigest(path.join(resources, "resources-manifest.json")) }));
      const archive = await require("@electron/asar").createPackage(app, path.join(resources, "app.asar"));
      await finished(archive);
      assert.equal(archive.writableFinished, true);
      assert.equal(archive.closed, true);
      // Native identities use the shipping loader directly. Other fixture
      // identities use the existing scoped process view, including the exact
      // runtime bytes extracted and checked by packagedRuntime from the ASAR.
      const upgrade = host === nativeHost ? require("../upgrade-smoke.cjs") : loadDesktopFixtureModule("upgrade-smoke.cjs", host, extracted);
      const loaded = upgrade.packagedRuntime(resources, extracted);
      const catalog = loaded.runtime.verifyResources(resources, loaded.integrity);
      assert.equal(catalog.v1.schema_version, 1);
      assert.equal(catalog.v1.host_platform, host);
      assert.equal(catalog.v2.schema_version, 2);
      assert.equal(catalog.v2.host_platform, host);
      for (const name of ["runtime.cjs", "runtime-inventory-v2.cjs"]) assert.deepEqual(fs.readFileSync(path.join(extracted, name)), fs.readFileSync(path.join(app, name)));
    });
  }
});
