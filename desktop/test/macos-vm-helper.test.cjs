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

test("Mac helper accepts physical private/tmp inputs but rejects unsafe paths before VM creation", { skip: process.platform !== "darwin" }, async t => {
  // Each invocation has an invalid input so admission stops before VMOwner.
  // The regression is the first, real canonical file being rejected as an
  // argument merely because Foundation rewrites /private/tmp to /tmp.
  const root = fs.mkdtempSync("/private/tmp/foundry-helper-path-test-");
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  assert.equal(fs.realpathSync(root), root);
  const helper = path.join(root, "helper"), file = path.join(root, "regular input");
  cp.execFileSync("/usr/bin/xcrun", ["swiftc", "-framework", "Virtualization", path.join(__dirname, "../sandbox/macos/main.swift"), "-o", helper], { timeout: 120000 });
  fs.writeFileSync(file, "not a kernel; admission tests only", { flag: "wx", mode: 0o600 });
  fs.symlinkSync(file, path.join(root, "file-link"));
  fs.mkdirSync(path.join(root, "directory"));
  fs.symlinkSync(root, path.join(root, "parent-link"));
  const missing = path.join(root, "missing"), missingOther = path.join(root, "missing-other");
  const cases = [
    ["canonical regular file reaches missing input", file, missing, 65, "invalid_input"],
    ["relative path", "relative", missing, 64, "invalid_arguments"],
    ["dot component", root + "/./regular input", missing, 64, "invalid_arguments"],
    ["parent component", root + "/directory/../regular input", missing, 64, "invalid_arguments"],
    ["empty component", root + "//regular input", missing, 64, "invalid_arguments"],
    ["trailing separator", file + "/", missing, 64, "invalid_arguments"],
    ["missing file", missing, missingOther, 65, "invalid_input"],
    ["directory input", root + "/directory", missing, 65, "invalid_input"],
    ["leaf symlink", root + "/file-link", missing, 65, "invalid_input"],
    ["parent symlink", root + "/parent-link/regular input", missing, 64, "invalid_arguments"],
    ["system tmp alias", root.replace(/^\/private/, "") + "/regular input", missing, 64, "invalid_arguments"],
    ["initrd parent symlink", file, root + "/parent-link/regular input", 64, "invalid_arguments"],
    ["initrd leaf symlink", file, root + "/file-link", 65, "invalid_input"],
  ];
  for (const [name, kernel, initrd, status, stage] of cases) {
    await t.test(name, () => {
      const got = cp.spawnSync(helper, ["--kernel", kernel, "--initrd", initrd], { encoding: "utf8", timeout: 5000 });
      assert.equal(got.error, undefined);
      assert.equal(got.signal, null);
      assert.equal(got.status, status);
      assert.equal(got.stdout, "");
      assert.ok(got.stderr.length < 512);
      assert.deepEqual(JSON.parse(got.stderr), { schema_version: 1, stage, exit_code: status });
      assert.ok(!got.stderr.includes(root));
    });
  }
});
