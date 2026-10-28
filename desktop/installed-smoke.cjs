"use strict";
// Run against copied/installed resources, never a source-tree backend.
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const { startBackend, verifySandboxBundle } = require("./runtime.cjs");
const { installedFlow } = require("./installed-flow.cjs");
const { assertCodingMetadata } = require("./coding-smoke.cjs");
function hashFile(file) {
  return crypto
    .createHash("sha256")
    .update(fs.readFileSync(file))
    .digest("hex");
}
async function authorized(child, route, options = {}) {
  const headers = {
    "X-Foundry-Desktop": child.capability,
    Origin: child.ready.url,
    ...options.headers,
  };
  return fetch(child.ready.url + route, { ...options, headers });
}
async function session(child) {
  const response = await authorized(child, "/api/session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
  assert.equal(response.status, 200);
  return response.headers.get("set-cookie").split(";")[0];
}
// Read-only account-feature composition checks. The sentinel account is never
// created; its preparation view must stay empty. Never invoke preparation here.
async function assertDesktopSubscriptionMetadata(
  child,
  cookie,
  platform,
  mode,
  request = authorized,
) {
  const diagnostics = path.join(
    child.ready.dataDir,
    "openai-desktop-diagnostics",
  );
  assert.equal(
    fs.existsSync(diagnostics),
    false,
    "metadata startup created diagnostic journals",
  );
  const options = { headers: { Cookie: cookie } };
  const response = await request(child, "/api/model-routes", options);
  assert.equal(response.status, 200);
  const { runtime } = await response.json();
  const enabled = ["darwin", "win32"].includes(platform) && mode === "coding";
  assert.equal(runtime.desktop_subscription, enabled);
  assert.equal(runtime.live_authorization, false);
  assert.equal(runtime.live_discovery, false);
  assert.equal(runtime.automatic_paid_fallback, false);
  const preparation = await request(
    child,
    "/api/model-accounts/installed-smoke-unconfigured/desktop-preparation",
    options,
  );
  assert.equal(preparation.status, enabled ? 200 : 403);
  if (enabled)
    assert.deepEqual(await preparation.json(), {
      preparation: {
        phase: "unprepared",
        connection_ready: false,
        catalog_ready: false,
        inference_ready: false,
      },
      catalog_choices: [],
    });
  // An absent sentinel registration is not manufactured by a metadata read.
  // The renewal path is desktop-only and never prepares/enables consent here.
  const renewal = await request(
    child,
    "/api/model-accounts/installed-smoke-unconfigured/session-renewal",
    options,
  );
  assert.equal(renewal.status, enabled ? 404 : 403);
  assert.equal(
    fs.existsSync(diagnostics),
    false,
    "metadata reads created diagnostic journals",
  );
}
async function main() {
  const resources = process.env.FOUNDRY_INSTALLED_RESOURCES;
  const dataRoot = process.env.FOUNDRY_SMOKE_DATA;
  const { extractFile } = await import("@electron/asar");
  const { manifestSHA256: integrity } = JSON.parse(
    extractFile(path.join(resources, "app.asar"), "resource-integrity.json"),
  );
  assert.ok(path.isAbsolute(resources));
  assert.ok(path.isAbsolute(dataRoot));
  assert.ok(!fs.existsSync(dataRoot), "smoke data must be fresh");
  let child;
  try {
    child = await startBackend({ resources, dataRoot, integrity });
    assert.equal((await fetch(child.ready.url + "/")).status, 403);
    assert.equal(
      (await fetch(child.ready.url + "/api/session", { method: "POST" }))
        .status,
      403,
    );
    const page = await authorized(child, "/");
    assert.equal(page.status, 200);
    assert.match(await page.text(), /type="module"/);
    let cookie = await session(child);
    assert.equal(
      (
        await authorized(child, "/api/missions", {
          headers: { Cookie: cookie },
        })
      ).status,
      200,
    );
    await assert.rejects(
      startBackend({ resources, dataRoot, timeout: 5000, integrity }),
    ); // real data lock
    const db = path.join(child.ready.dataDir, "foundry.db");
    const inode = fs.statSync(db).ino;
    await child.stop();
    assert.equal(child.process.exitCode, 0);
    child = await startBackend({ resources, dataRoot, integrity });
    assert.equal(fs.statSync(db).ino, inode);
    cookie = await session(child);
    assert.equal(
      (
        await authorized(child, "/api/missions", {
          headers: { Cookie: cookie },
        })
      ).status,
      200,
    );
    const disabledCodingResponse = await authorized(
      child,
      "/api/coding-tasks",
      { headers: { Cookie: cookie } },
    );
    assert.equal(disabledCodingResponse.status, 200);
    assertCodingMetadata(
      await disabledCodingResponse.json(),
      process.platform,
      false,
    );
    await assertDesktopSubscriptionMetadata(
      child,
      cookie,
      process.platform,
      "normal",
    );
    await child.stop();
    child = await startBackend({
      resources,
      dataRoot,
      mode: "coding",
      integrity,
    });
    cookie = await session(child);
    let codingResponse = await authorized(child, "/api/coding-tasks", {
      headers: { Cookie: cookie },
    });
    assert.equal(codingResponse.status, 200);
    const codingMetadata = await codingResponse.json();
    assertCodingMetadata(codingMetadata, process.platform);
    await assertDesktopSubscriptionMetadata(
      child,
      cookie,
      process.platform,
      "coding",
    );
    assert.equal(
      codingMetadata.profile.embedded_vm.manifest_digest,
      hashFile(path.join(resources, "sandbox/manifest.json")),
    );
    const retainedRuntime = path.join(
      child.ready.dataDir,
      "vm-bundles",
      codingMetadata.profile.embedded_vm.manifest_digest,
    );
    const installedManifest = verifySandboxBundle(
      path.join(resources, "sandbox"),
    );
    assert.deepEqual(verifySandboxBundle(retainedRuntime), installedManifest);
    assert.equal(
      hashFile(path.join(retainedRuntime, "manifest.json")),
      codingMetadata.profile.embedded_vm.manifest_digest,
    );
    const codingDB = path.join(child.ready.dataDir, "foundry.db");
    await child.stop();
    const codingHash = hashFile(codingDB);
    child = await startBackend({
      resources,
      dataRoot,
      mode: "coding",
      integrity,
    });
    cookie = await session(child);
    assert.deepEqual(verifySandboxBundle(retainedRuntime), installedManifest);
    codingResponse = await authorized(child, "/api/coding-tasks", {
      headers: { Cookie: cookie },
    });
    assert.equal(codingResponse.status, 200);
    assertCodingMetadata(await codingResponse.json(), process.platform);
    await assertDesktopSubscriptionMetadata(
      child,
      cookie,
      process.platform,
      "coding",
    );
    await child.stop();
    assert.equal(hashFile(codingDB), codingHash);
    child = await startBackend({
      resources,
      dataRoot,
      mode: "simulation",
      integrity,
    });
    cookie = await session(child);
    const response = await authorized(child, "/api/missions", {
      headers: { Cookie: cookie },
    });
    assert.equal(response.status, 200);
    const { missions } = await response.json();
    assert.equal(missions.length, 2);
    for (const summary of missions) {
      const detail = await authorized(child, "/api/missions/" + summary.id, {
        headers: { Cookie: cookie },
      });
      const { mission } = await detail.json();
      assert.equal(mission.c4_operations.length, 1);
      const baseline = mission.c4_operations[0];
      assert.equal(baseline.kind, "baseline");
      assert.equal(baseline.state, "prepared");
      for (const fact of [
        "approval",
        "dispatch",
        "created",
        "started",
        "result",
      ]) {
        assert.equal(baseline[fact], undefined, `unexpected baseline ${fact}`);
      }
      // Create records a snapshot seal, not permission to read context, run,
      // approve a plan or deliver. No action-grant approval may be present.
      assert.deepEqual(
        mission.approvals.map((approval) => approval.kind),
        ["snapshot"],
      );
      assert.equal(mission.approvals[0].revision, 0);
      assert.equal(mission.state, "draft");
      assert.equal((mission.evidence || []).length, 0);
    }
    const retained = child.ready.dataDir;
    const deliveryArtifacts = await installedFlow(
      child,
      cookie,
      authorized,
      missions,
    );
    await child.stop();
    assert.ok(fs.existsSync(path.join(retained, "foundry.db")));
    assert.equal(hashFile(codingDB), codingHash);
    console.log(
      JSON.stringify(
        {
          result: "PASS",
          resources,
          dataRoot,
          simulationEvidence: retained,
          codingEvidence: path.dirname(codingDB),
          deliveryArtifacts,
          codingMetadata,
          checks: [
            "real bundled SPA and Go",
            "all-route capability",
            "session bootstrap",
            "same-data lock rejects second controller",
            "EOF graceful exit",
            "persistent restart",
            "normal startup keeps coding metadata closed",
            "subscription metadata is desktop-coding-only and unprepared across restart; reads create no diagnostic journals",
            "coding startup exposes installed VM identity metadata only; hardware readiness and execution remain separately approved",
            "current runtime is retained byte-exact in the private archive and revalidated after restart; no VM execution",
            "isolated simulation baseline remains prepared without approval or execution facts",
            "evidence retained",
            "installed binaries: two separately approved baseline/plan/failure-repair/review/delivery flows",
            "stale and missing approvals rejected; source Git and protected base unchanged",
          ],
        },
        null,
        2,
      ),
    );
  } finally {
    if (child) await child.stop();
  }
}
if (require.main === module)
  main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
  });

module.exports = { hashFile, assertDesktopSubscriptionMetadata };
