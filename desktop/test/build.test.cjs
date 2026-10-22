const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { validateOutput, writeManifest } = require("../build-support.cjs");
const { verifyResources, executableName } = require("../runtime.cjs");
test("build rejects source-tree output and already occupied destinations", (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-build-test-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  assert.throws(() => validateOutput(path.join(root, "out"), root));
  assert.throws(() => validateOutput(root, root));
  assert.throws(() => validateOutput(root, "/unrelated-source"));
  assert.throws(() => validateOutput("relative", root));
  assert.equal(validateOutput(root + "-new", root), root + "-new");
});
test("manifest covers all static inputs and detects modification", (t) => {
  const root = fs.realpathSync(
    fs.mkdtempSync(path.join(os.tmpdir(), "foundry-manifest-test-")),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  for (const dir of ["bin", "web", "legal"]) fs.mkdirSync(path.join(root, dir));
  for (const file of [
    "bin/" + executableName("foundry"),
    "bin/" + executableName("foundry-acceptance"),
    "web/index.html",
    "web/app.js",
    "legal/LICENSE",
  ])
    fs.writeFileSync(path.join(root, file), file);
  writeManifest(root);
  verifyResources(root);
  fs.writeFileSync(path.join(root, "web/app.js"), "tampered");
  assert.throws(() => verifyResources(root), /integrity/);
});
