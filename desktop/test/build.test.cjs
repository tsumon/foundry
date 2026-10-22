const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { validateOutput, writeManifest } = require("../build-support.cjs");
const { verifyResources, executableName } = require("../runtime.cjs");
test("native CI explicitly installs the pinned Electron runtime before contract tests", () => {
  const workflow = fs.readFileSync(path.join(__dirname, "../../.github/workflows/desktop.yml"), "utf8");
  for (const name of ["macos-installer", "windows-installer"]) {
    const job = workflow.split(`  ${name}:\n`)[1].split(/^  [a-z-]+:\n/m)[0];
    const install = job.indexOf("node desktop/node_modules/electron/install.js");
    const testGate = job.search(/name: (Desktop boundaries|Native CGO commands)/);
    assert.ok(install >= 0 && install < testGate, `${name} must install Electron before testing`);
  }
  assert.doesNotMatch(workflow.split("  macos-installer:\n")[0], /node desktop\/node_modules\/electron\/install\.js/);
});
test("shared verification retains full uncached normal and race suites with finite budgets", () => {
  const script = fs.readFileSync(path.join(__dirname, "../../scripts/verify.sh"), "utf8");
  assert.match(script, /^go test -count=1 -timeout=600s \.\/\.\.\.$/m);
  assert.match(script, /^go test -race -count=1 -timeout=1200s \.\/\.\.\.$/m);
  assert.match(script, /^go vet \.\/\.\.\.$/m);
  assert.doesNotMatch(script, /go test[^\n]*(?:-skip|-run|-short|\|\|)/);
  const workflow = fs.readFileSync(path.join(__dirname, "../../.github/workflows/verify.yml"), "utf8");
  assert.match(workflow, /timeout-minutes: 45/);
});
test("Windows native CI uses an explicit long-form temporary root without relaxing aliases", () => {
  const workflow = fs.readFileSync(path.join(__dirname, "../../.github/workflows/desktop.yml"), "utf8");
  const job = workflow.split("  windows-installer:\n")[1];
  const setup = job.indexOf("name: Prepare long-form native temporary directory");
  assert.ok(setup > 0 && setup < job.indexOf("name: Install locked dependencies"));
  assert.match(job, /IsPathFullyQualified\(\$nativeTemp\)/);
  assert.match(job, /\$nativeTemp\.Contains\('~'\)/);
  assert.match(job, /"TEMP=\$nativeTemp", "TMP=\$nativeTemp"/);
  assert.match(job, /Out-File -FilePath \$env:GITHUB_ENV -Append -Encoding utf8/);
});
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
