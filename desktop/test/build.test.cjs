const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { validateOutput, writeManifest } = require("../build-support.cjs");
const {
  sandboxFixture,
  loadDesktopFixtureModule,
} = require("./sandbox-fixture.cjs");
const { verifyResources, executableName } =
  loadDesktopFixtureModule("runtime.cjs");

test("sandbox packaging requires explicit absolute verified input and preserves signed helper bytes", (t) => {
  const root = fs.realpathSync(
    fs.mkdtempSync(path.join(os.tmpdir(), "foundry-sandbox-copy-")),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const bundle = path.join(root, "bundle"),
    target = path.join(root, "resources", "sandbox");
  const manifest = sandboxFixture(bundle, "darwin/arm64");
  if (process.platform !== "win32")
    fs.chmodSync(path.join(bundle, manifest.runtime), 0o755);
  const { copySandboxBundle } = require("../build-support.cjs");
  assert.throws(
    () => copySandboxBundle(undefined, target, "darwin/arm64"),
    /sandbox|absolute/i,
  );
  assert.throws(
    () => copySandboxBundle("relative", target, "darwin/arm64"),
    /sandbox|absolute/i,
  );
  const receipt = copySandboxBundle(bundle, target, "darwin/arm64");
  assert.equal(receipt.host_platform, "darwin/arm64");
  assert.deepEqual(
    fs.readFileSync(path.join(target, manifest.runtime)),
    fs.readFileSync(path.join(bundle, manifest.runtime)),
  );
  if (process.platform !== "win32")
    assert.equal(
      fs.statSync(path.join(target, manifest.runtime)).mode & 0o777,
      0o755,
    );
  assert.throws(
    () => copySandboxBundle(bundle, target, "darwin/arm64"),
    /exists|fresh/i,
  );
  assert.throws(() => copySandboxBundle(bundle, target + "-unknown", "darwin/arm64", 3), /schema/i);
});

test("copy rejects a manifest changed after initial verification", (t) => {
  const root = fs.realpathSync(
    fs.mkdtempSync(path.join(os.tmpdir(), "foundry-copy-change-")),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const bundle = path.join(root, "bundle"),
    target = path.join(root, "copy");
  sandboxFixture(bundle, "darwin/arm64");
  const original = fs.cpSync;
  fs.cpSync = (...args) => {
    fs.appendFileSync(path.join(bundle, "manifest.json"), "\n");
    return original(...args);
  };
  try {
    assert.throws(
      () =>
        require("../build-support.cjs").copySandboxBundle(
          bundle,
          target,
          "darwin/arm64",
        ),
      /changed/i,
    );
  } finally {
    fs.cpSync = original;
  }
});

test("Mac packaging verifies virtualization entitlement without re-signing sealed sandbox content", () => {
  const { verifyMacSandboxSignature } = require("../build-support.cjs");
  const calls = [];
  const execute = (command, args) => {
    calls.push([command, args]);
    return args.includes("--entitlements")
      ? '<?xml version="1.0"?><plist><dict><key>com.apple.security.virtualization</key><true/></dict></plist>'
      : "";
  };
  verifyMacSandboxSignature("/verified/sandbox", execute);
  assert.equal(calls.length, 2);
  assert.equal(
    calls.every(
      ([command, args]) =>
        command === "/usr/bin/codesign" && !args.includes("--sign"),
    ),
    true,
  );
  assert.throws(
    () =>
      verifyMacSandboxSignature(
        "/verified/sandbox",
        () =>
          "<plist><dict><key>com.apple.security.virtualization</key><false/></dict></plist>",
      ),
    /entitlement/i,
  );
});

test("nested signing never changes sealed sandbox helper or libraries", (t) => {
  const root = fs.realpathSync(
    fs.mkdtempSync(path.join(os.tmpdir(), "foundry-sign-plan-")),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const app = path.join(root, "Foundry.app");
  for (const file of [
    "Contents/Frameworks/Library.dylib",
    "Contents/Resources/sandbox/runtime/extra.dylib",
    "Contents/Resources/sandbox/runtime/foundry-vm-helper",
  ]) {
    fs.mkdirSync(path.dirname(path.join(app, file)), { recursive: true });
    fs.writeFileSync(path.join(app, file), "signed bytes");
  }
  const { macSigningTargets } = require("../build-support.cjs");
  assert.deepEqual(macSigningTargets(app), [
    path.join(app, "Contents/Frameworks/Library.dylib"),
    app,
  ]);
});
function workflowJob(workflow, name) {
  return workflow
    .replace(/\r\n/g, "\n")
    .split(`  ${name}:\n`)[1]
    .split(/^  [a-z-]+:\n/m)[0];
}
test("desktop jobs build the verified sandbox before packaging and publish its source sidecar only after gates", () => {
  const workflow = require("../node_modules/js-yaml").load(
    fs.readFileSync(
      path.join(__dirname, "../../.github/workflows/desktop.yml"),
      "utf8",
    ),
  );
  for (const name of ["macos-installer", "windows-installer"]) {
    const job = workflow.jobs[name],
      steps = job.steps;
    const sandbox = steps.findIndex((s) => s.id === "sandbox-bundle");
    const packaging = steps.findIndex((s) => s.id === "desktop-package");
    const installed = steps.findIndex((s) => s.id === "installed-contract");
    const artifact = name === "macos-installer" ? "foundry-macos-arm64" : "foundry-windows-x64";
    const upload = steps.findIndex((s) =>
      s.uses?.startsWith("actions/upload-artifact@") && s.with?.name === artifact,
    );
    assert.ok(
      sandbox >= 0 &&
        packaging > sandbox &&
        installed > packaging &&
        upload > installed,
      name,
    );
    assert.match(steps[sandbox].run, /node desktop\/sandbox\/build\.cjs/);
    assert.match(steps[sandbox].run, /node desktop\/sandbox\/build-bundle-v2\.cjs/);
    assert.match(steps[packaging].run, /FOUNDRY_SANDBOX_BUNDLE/);
    assert.match(steps[packaging].run, /FOUNDRY_SANDBOX_V2_BUNDLE/);
    const installerOutput =
      name === "macos-installer" ? "macos_installer" : "windows_installer";
    assert.ok(
      steps[upload].with.path.includes(
        "${{ needs.runtime-sources.outputs." + installerOutput + " }}",
      ),
    );
    assert.match(steps[upload].with.path, /sandbox-sources\//);
    assert.match(steps[upload].with.path, /sandbox-v2-sources\//);
    assert.match(steps[upload].with.path, /distribution-manifest\.json/);
    for (const step of [
      steps[sandbox],
      steps[packaging],
      steps[installed],
      steps[upload],
    ]) {
      assert.equal(step["continue-on-error"], undefined);
      assert.equal(step.if, undefined);
    }
    if (name === "windows-installer") {
      const bootstrap = steps.findIndex((s) => s.id === "windows-vm-runtime");
      assert.ok(bootstrap >= 0 && bootstrap < sandbox);
      assert.match(steps[bootstrap].run, /--bootstrap/);
      assert.match(steps[bootstrap].run, /--install-pinned-build-packages/);
      assert.match(steps[sandbox].run, /FOUNDRY_WINDOWS_VM_RUNTIME/);
      assert.match(steps[installed].name, /mandatory WHPX guest/);
      assert.match(steps[upload].with.path, /native-vm-acceptance\.json/);
      assert.match(
        steps[upload].with.path,
        /foundry-vm-acceptance-pe-imports\.txt/,
      );
    } else assert.match(job.name, /not VM execution/);
  }
});
test("installer acceptance and uploads follow prerelease and stable package versions", (t) => {
  const workflow = require("../node_modules/js-yaml").load(
    fs.readFileSync(
      path.join(__dirname, "../../.github/workflows/desktop.yml"),
      "utf8",
    ),
  );
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-version-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.mkdirSync(path.join(root, "desktop"));
  const sourceJob = workflow.jobs["runtime-sources"];
  const metadataStep = sourceJob.steps.find((s) => s.id === "package-metadata");
  assert.ok(metadataStep);
  // Run the workflow's actual node program with a changed package manifest.
  const program = metadataStep.run.match(/^\s*node -e '([\s\S]*)'\s*$/)[1];
  for (const version of ["0.2.0-rc.1", "1.0.0", "1.0.0+build.7"]) {
    const output = path.join(root, version + ".output");
    fs.writeFileSync(
      path.join(root, "desktop/package.json"),
      JSON.stringify({ version }),
    );
    require("node:child_process").execFileSync(
      process.execPath,
      ["-e", program],
      {
        cwd: root,
        env: { ...process.env, GITHUB_OUTPUT: output },
      },
    );
    assert.equal(
      fs.readFileSync(output, "utf8"),
      `macos_installer=Foundry-${version}-arm64.dmg\nwindows_installer=Foundry-${version}-x64.exe\n`,
    );
  }
  for (const version of [
    null,
    "../installer",
    "1.0.0\nunexpected=value",
    "1.0.0\n",
  ]) {
    const output = path.join(root, "invalid.output");
    fs.writeFileSync(
      path.join(root, "desktop/package.json"),
      JSON.stringify({ version }),
    );
    const result = require("node:child_process").spawnSync(
      process.execPath,
      ["-e", program],
      {
        cwd: root,
        env: { ...process.env, GITHUB_OUTPUT: output },
        encoding: "utf8",
      },
    );
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /Invalid desktop package version/);
    assert.equal(fs.existsSync(output), false);
  }
  for (const output of ["macos_installer", "windows_installer"])
    assert.equal(
      sourceJob.outputs[output],
      "${{ steps.package-metadata.outputs." + output + " }}",
    );
  const installed = workflow.jobs["macos-installer"].steps.find(
    (s) => s.id === "installed-contract",
  );
  assert.equal(
    installed.env.FOUNDRY_MAC_INSTALLER,
    "${{ needs.runtime-sources.outputs.macos_installer }}",
  );
  assert.match(installed.run, /"\$output\/\$FOUNDRY_MAC_INSTALLER"/);
  const windowsSmoke = fs.readFileSync(
    path.join(__dirname, "../windows-installed-smoke.ps1"),
    "utf8",
  );
  assert.match(windowsSmoke, /Join-Path \$PSScriptRoot 'package\.json'/);
  assert.match(
    windowsSmoke,
    /\$packageVersion = .*ConvertFrom-Json\)\.version/,
  );
  assert.match(
    windowsSmoke,
    /Join-Path \$Output "Foundry-\$packageVersion-x64\.exe"/,
  );
  assert.doesNotMatch(windowsSmoke, /Foundry-\d+\.\d+\.\d+/);
});
test("workflow boundary checks parse LF and native Windows CRLF identically", () => {
  const workflow = fs.readFileSync(
    path.join(__dirname, "../../.github/workflows/desktop.yml"),
    "utf8",
  );
  const lf = workflow.replace(/\r\n/g, "\n");
  const crlf = lf.replace(/\n/g, "\r\n");
  for (const name of [
    "runtime-sources",
    "macos-installer",
    "windows-installer",
  ]) {
    assert.equal(workflowJob(lf, name), workflowJob(crlf, name));
  }
});
test("native CI explicitly installs the pinned Electron runtime before contract tests", () => {
  const workflow = fs.readFileSync(
    path.join(__dirname, "../../.github/workflows/desktop.yml"),
    "utf8",
  );
  for (const name of ["macos-installer", "windows-installer"]) {
    const job = workflowJob(workflow, name);
    const install = job.indexOf(
      "node desktop/node_modules/electron/install.js",
    );
    const testGate = job.search(
      /name: (Desktop boundaries|Native CGO commands)/,
    );
    assert.ok(
      install >= 0 && install < testGate,
      `${name} must install Electron before testing`,
    );
  }
  assert.doesNotMatch(
    workflowJob(workflow, "runtime-sources"),
    /node desktop\/node_modules\/electron\/install\.js/,
  );
});
test("shared verification retains full uncached normal and race suites with finite budgets", () => {
  const script = fs.readFileSync(
    path.join(__dirname, "../../scripts/verify.sh"),
    "utf8",
  );
  assert.match(script, /^go test -count=1 -timeout=600s \.\/\.\.\.$/m);
  assert.match(script, /^go test -race -count=1 -timeout=1200s \.\/\.\.\.$/m);
  assert.match(script, /^go vet \.\/\.\.\.$/m);
  assert.doesNotMatch(script, /go test[^\n]*(?:-skip|-run|-short|\|\|)/);
  const workflow = fs.readFileSync(
    path.join(__dirname, "../../.github/workflows/verify.yml"),
    "utf8",
  );
  assert.match(workflow, /timeout-minutes: 45/);
});
test("Windows native CI uses an explicit long-form temporary root without relaxing aliases", () => {
  const workflow = fs.readFileSync(
    path.join(__dirname, "../../.github/workflows/desktop.yml"),
    "utf8",
  );
  const job = workflowJob(workflow, "windows-installer");
  const setup = job.indexOf(
    "name: Prepare long-form native temporary directory",
  );
  assert.ok(
    setup > 0 && setup < job.indexOf("name: Install locked dependencies"),
  );
  assert.match(job, /IsPathFullyQualified\(\$nativeTemp\)/);
  assert.match(job, /\$nativeTemp\.Contains\('~'\)/);
  assert.match(job, /"TEMP=\$nativeTemp", "TMP=\$nativeTemp"/);
  assert.match(
    job,
    /Out-File -FilePath \$env:GITHUB_ENV -Append -Encoding utf8/,
  );
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
    "bin/" + executableName("foundry-vm-acceptance"),
    "web/index.html",
    "web/app.js",
    "legal/LICENSE",
  ])
    fs.writeFileSync(path.join(root, file), file);
  sandboxFixture(path.join(root, "sandbox"));
  writeManifest(root);
  verifyResources(root);
  fs.writeFileSync(path.join(root, "web/app.js"), "tampered");
  assert.throws(() => verifyResources(root), /integrity/);
});
