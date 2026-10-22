"use strict";
// Test driver only. Explicitly approves fresh, process-owned synthetic tasks;
// never imported into the installed application's ASAR or normal startup.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
function assertFlowEvidence(m) {
  assert.equal(m.state, "awaiting_delivery");
  assert.equal(m.delivery, null);
  assert.equal(m.c4_operations.length, 3);
  assert.equal(m.c4_operations[1].result.class, "test_failed");
  assert.equal(m.c4_operations[2].result.class, "passed");
  assert.notEqual(
    m.c4_operations[1].candidate_digest,
    m.c4_operations[2].candidate_digest,
  );
  assert.deepEqual(
    m.evidence.map((e) => [e.passed, e.source_kind]),
    [
      [false, "synthetic_stub"],
      [true, "synthetic_stub"],
    ],
  );
  assert.equal(m.reviews.length, 2);
  for (const r of m.reviews) {
    assert.equal(r.passed, true);
    assert.ok(r.binding_digest && r.checks.length);
  }
}
function treeDigest(root) {
  const hash = crypto.createHash("sha256");
  function visit(dir) {
    for (const item of fs
      .readdirSync(dir, { withFileTypes: true })
      .sort((a, b) => a.name.localeCompare(b.name))) {
      const filename = path.join(dir, item.name);
      assert.equal(item.isSymbolicLink(), false, "unexpected fixture link");
      if (item.isDirectory()) visit(filename);
      else {
        assert.ok(item.isFile());
        const raw = fs.readFileSync(filename);
        hash.update(path.relative(root, filename) + "\0" + raw.length + "\0");
        hash.update(raw);
      }
    }
  }
  visit(root);
  return hash.digest("hex");
}
async function installedFlow(child, cookie, authorized, missions) {
  const receipt = [];
  for (const summary of missions) {
    const base = "/api/missions/" + summary.id;
    async function api(route, body, expected = 200) {
      const response = await authorized(child, base + route, {
        method: body === undefined ? "GET" : "POST",
        headers: {
          Cookie: cookie,
          ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        signal: AbortSignal.timeout(30000),
      });
      assert.equal(response.status, expected, route + " status");
      return expected === 200 ? response : null;
    }
    let m = (await (await api("")).json()).mission;
    const owned = path.relative(child.ready.dataDir, m.repo_path);
    assert.ok(
      owned && !owned.startsWith("..") && !path.isAbsolute(owned),
      "fixture must be inside owned data root",
    );
    const source = treeDigest(m.repo_path);
    const immutableBase = path.join(
      child.ready.dataDir,
      "managed",
      "missions",
      m.id,
      "base",
    );
    const baseDigest = treeDigest(immutableBase);
    async function action(route, body, expected = 200) {
      const response = await api(route, body, expected);
      if (response) m = (await response.json()).mission;
    }
    async function baseline(actionName, expected = 200) {
      const op = m.c4_operations[0];
      await action(
        "/c4",
        {
          action: actionName,
          revision: m.revision,
          operation_id: op.id,
          material_digest: op.material_digest,
        },
        expected,
      );
    }
    async function waitFor(state) {
      const deadline = Date.now() + 30000;
      while (Date.now() < deadline) {
        m = (await (await api("")).json()).mission;
        if (m.state === state) return;
        assert.notEqual(m.state, "blocked", m.error || "unexpected block");
        await new Promise((resolve) => setTimeout(resolve, 100));
      }
      throw Error("Synthetic task did not reach " + state);
    }
    for (const route of ["/plan", "/approve", "/deliver"])
      await action(route, { revision: m.revision }, 409);
    await baseline("execute", 409);
    await baseline("approve");
    assert.equal(m.c4_operations[0].dispatch, undefined);
    await baseline("execute");
    assert.equal(m.c4_operations[0].result.class, "passed");
    assert.equal((m.evidence || []).length, 0);
    await action("/plan", { revision: m.revision });
    await waitFor("awaiting_approval");
    const oldRevision = m.revision;
    await action("/plan-revisions", {
      expected_mission_revision: m.revision,
      expected_plan_revision: m.plan.revision,
      reason: "Installed-package synthetic acceptance review",
      plan: {
        summary: "Reviewed local simulation",
        steps: m.plan.steps.map((s) => ({
          id: s.id,
          title: "Reviewed: " + s.title,
          description: s.description,
          files: s.files,
          depends_on: s.depends_on,
        })),
      },
    });
    await action("/approve", { revision: oldRevision }, 409);
    await action("/deliver", { revision: m.revision }, 409);
    await action("/approve", { revision: m.revision });
    await waitFor("awaiting_delivery");
    assertFlowEvidence(m);
    await action("/deliver", { revision: m.revision - 1 }, 409);
    await action("/deliver", { revision: m.revision });
    assert.equal(m.state, "delivered");
    for (const route of ["patch", "report"]) {
      const response = await api("/" + route);
      assert.match(response.headers.get("content-disposition"), /attachment/);
      const bytes = Buffer.from(await response.arrayBuffer());
      assert.ok(bytes.length > 100);
      if (route === "report") assert.match(bytes.toString(), /synthetic_stub/);
      receipt.push({
        task: m.id,
        artifact: route,
        bytes: bytes.length,
        sha256: crypto.createHash("sha256").update(bytes).digest("hex"),
      });
    }
    assert.equal(treeDigest(m.repo_path), source, "source Git tree changed");
    assert.equal(
      treeDigest(immutableBase),
      baseDigest,
      "protected base changed",
    );
  }
  return receipt;
}
module.exports = { installedFlow, assertFlowEvidence };
