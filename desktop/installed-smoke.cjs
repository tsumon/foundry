"use strict";
// Run against copied/installed resources, never a source-tree backend.
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const { startBackend } = require("./runtime.cjs");
const { installedFlow } = require("./installed-flow.cjs");
const { assertCodingMetadata } = require("./coding-smoke.cjs");
function hashFile(file) {
  return crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
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
    codingResponse = await authorized(child, "/api/coding-tasks", {
      headers: { Cookie: cookie },
    });
    assert.equal(codingResponse.status, 200);
    assertCodingMetadata(await codingResponse.json(), process.platform);
    await child.stop();
    assert.equal(
      hashFile(codingDB),
      codingHash,
    );
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
    assert.equal(
      hashFile(codingDB),
      codingHash,
    );
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
            "coding startup exposes retained metadata only; Windows execution unavailable",
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

module.exports = { hashFile };
