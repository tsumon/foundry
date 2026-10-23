"use strict";
const test = require("node:test"), assert = require("node:assert/strict");
const fs = require("node:fs"), os = require("node:os"), path = require("node:path"), cp = require("node:child_process");
test("Mac helper invalid invocations emit bounded typed diagnostics, never raw paths", { skip: process.platform !== "darwin" }, t => {
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-helper-test-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const helper = path.join(root, "helper");
  cp.execFileSync("/usr/bin/xcrun", ["swiftc", "-framework", "Virtualization", path.join(__dirname, "../sandbox/macos/main.swift"), "-o", helper], { timeout: 120000 });
  for (const [args, status, stage] of [[[], 64, "invalid_arguments"], [["--kernel", root + "/private-name", "--initrd", root + "/missing"], 65, "invalid_input"]]) {
    const got = cp.spawnSync(helper, args, { encoding: "utf8", timeout: 5000 });
    assert.equal(got.status, status);
    assert.equal(got.stdout, "");
    assert.ok(got.stderr.length < 512);
    const d = JSON.parse(got.stderr);
    assert.deepEqual(d, { schema_version: 1, stage, exit_code: status });
    assert.ok(!got.stderr.includes(root));
  }
});
