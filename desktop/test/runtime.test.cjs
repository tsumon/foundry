"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");
const {
  sandboxFixture,
  loadDesktopFixtureModule,
} = require("./sandbox-fixture.cjs");
const { writeManifest } = require("../build-support.cjs");
const {
  parseReadiness,
  childEnvironment,
  requestDecision,
  startBackend,
  verifyResources,
  verifySandboxBundle,
} = loadDesktopFixtureModule("runtime.cjs");

test("fixture host does not enable the shipping runtime on an unsupported host", (t) => {
  const root = fs.realpathSync(
    fs.mkdtempSync(path.join(os.tmpdir(), "foundry-unsupported-host-")),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  sandboxFixture(root, "darwin/arm64");
  assert.throws(
    () => require("../runtime.cjs").verifySandboxBundle(root, "linux/amd64"),
    /platform|contract/i,
  );
  if (process.platform === "linux")
    assert.throws(
      () => require("../runtime.cjs").verifySandboxBundle(root),
      /platform|contract/i,
    );
});

test("sandbox inventory validates exact manifest, legal files, hashes, sizes and platform without executing", (t) => {
  const root = fs.realpathSync(
    fs.mkdtempSync(path.join(os.tmpdir(), "foundry-bundle-check-")),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const manifest = sandboxFixture(root, "darwin/arm64");
  assert.equal(
    verifySandboxBundle(root, "darwin/arm64").host_platform,
    "darwin/arm64",
  );
  assert.throws(
    () => verifySandboxBundle(root, "windows/amd64"),
    /platform|contract/i,
  );
  for (const mutation of [
    (m) => m.assets[0].size++,
    (m) => (m.assets[0].sha256 = "0".repeat(64)),
    (m) => m.assets.push({ ...m.assets[0] }),
    (m) => (m.assets[0].path = "../outside"),
    (m) => (m.assets[0].extra = true),
    (m) => (m.extra = true),
    (m) => (m.protocol = "future"),
    (m) => (m.assets = m.assets.filter((a) => a.path !== "legal/GPL-2.0.txt")),
  ]) {
    const changed = structuredClone(manifest);
    mutation(changed);
    fs.writeFileSync(path.join(root, "manifest.json"), JSON.stringify(changed));
    assert.throws(() => verifySandboxBundle(root, "darwin/arm64"));
  }
  fs.writeFileSync(
    path.join(root, "manifest.json"),
    JSON.stringify(manifest).replace(
      '"schema_version":1',
      '"schema_version":1,"schema_version":1',
    ),
  );
  assert.throws(() => verifySandboxBundle(root, "darwin/arm64"), /duplicate/i);
  fs.writeFileSync(path.join(root, "manifest.json"), JSON.stringify(manifest));
  fs.writeFileSync(path.join(root, "runtime", "extra"), "unlisted executable");
  assert.throws(() => verifySandboxBundle(root, "darwin/arm64"), /unlisted/i);
  fs.unlinkSync(path.join(root, "runtime", "extra"));
  fs.renameSync(path.join(root, "kernel"), path.join(root, "kernel-original"));
  fs.symlinkSync(path.join(root, "kernel-original"), path.join(root, "kernel"));
  assert.throws(
    () => verifySandboxBundle(root, "darwin/arm64"),
    /link|regular/i,
  );
});

test("sandbox paths reject sensitive metadata and Windows aliases even when hashes match", (t) => {
  const root = fs.realpathSync(
    fs.mkdtempSync(path.join(os.tmpdir(), "foundry-bundle-paths-")),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const manifest = sandboxFixture(root, "darwin/arm64");
  for (const name of [
    "legal/.env.production",
    "legal/credentials.txt",
    "legal/secret.txt",
    "legal/token.txt",
    "legal/.git/config",
    "legal/alias.",
  ]) {
    const file = path.join(root, name);
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, "fixture");
    const changed = structuredClone(manifest);
    changed.assets.push({
      path: name,
      size: 7,
      sha256: crypto.createHash("sha256").update("fixture").digest("hex"),
    });
    fs.writeFileSync(path.join(root, "manifest.json"), JSON.stringify(changed));
    assert.throws(
      () => verifySandboxBundle(root, "darwin/arm64"),
      /inventory/i,
      name,
    );
    fs.unlinkSync(file);
  }
});

// Wrong host / unexpected identity must never become the trusted origin.
test("readiness admits only the exact child loopback origin and identity", () => {
  const good = {
    protocol: 1,
    url: "http://127.0.0.1:12345",
    pid: 42,
    dataDir: "/data",
  };
  assert.equal(parseReadiness(JSON.stringify(good), 42).url, good.url);
  for (const patch of [
    { url: "http://localhost:12345" },
    { url: "http://127.0.0.1:0" },
    { url: "http://127.0.0.1:12345/?secret=x" },
    { url: "https://evil.test" },
    { url: "http://a@127.0.0.1:12345" },
    { pid: 43 },
    { protocol: 2 },
    { dataDir: "relative" },
  ])
    assert.throws(() =>
      parseReadiness(JSON.stringify({ ...good, ...patch }), 42),
    );
  assert.throws(() => parseReadiness("x".repeat(9000), 42));
  assert.throws(() => parseReadiness("{bad", 42));
});

test("child environment cannot inherit operator settings or credentials", () => {
  const env = childEnvironment(
    {
      HOME: "/user",
      TMPDIR: "/tmp",
      OPENAI_API_KEY: "secret",
      FOUNDRY_DOCKER: "/evil",
      NODE_OPTIONS: "--inspect",
      PATH: "/evil",
      FOUNDRY_VM_BUNDLE: "/evil-bundle",
    },
    "/data",
    "/resources/web",
  );
  assert.equal(env.HOME, "/user");
  assert.equal(env.FOUNDRY_ADDR, "127.0.0.1:0");
  for (const key of ["OPENAI_API_KEY", "NODE_OPTIONS"])
    assert.equal(env[key], undefined);
  assert.equal(env.PATH, "/usr/bin:/bin:/usr/sbin:/sbin");
  assert.notEqual(env.FOUNDRY_DOCKER, "/evil");
  assert.equal(env.FOUNDRY_VM_BUNDLE, undefined);
});

test("capability attaches only to same-origin traffic; cross-origin is cancelled", () => {
  const origin = "http://127.0.0.1:12345";
  assert.deepEqual(
    requestDecision(origin, "secret", origin + "/api/session", {
      "X-Foundry-Desktop": "spoof",
    }),
    { cancel: false, requestHeaders: { "X-Foundry-Desktop": "secret" } },
  );
  for (const target of [
    "https://example.com",
    "http://127.0.0.1:54321",
    "file:///etc/passwd",
    origin + ".evil/",
  ])
    assert.deepEqual(requestDecision(origin, "secret", target, {}), {
      cancel: true,
    });
});

function fixture(t, body) {
  const root = fs.realpathSync(
    fs.mkdtempSync(path.join(os.tmpdir(), "foundry-desktop-test-")),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.mkdirSync(path.join(root, "bin"));
  fs.mkdirSync(path.join(root, "web"));
  fs.writeFileSync(path.join(root, "web/index.html"), "SPA");
  for (const name of [
    "foundry",
    "foundry-acceptance",
    "foundry-vm-acceptance",
  ]) {
    fs.writeFileSync(
      path.join(root, "bin", name),
      "#!" + process.execPath + "\n" + body,
      { mode: 0o755 },
    );
    fs.chmodSync(path.join(root, "bin", name), 0o755);
  }
  sandboxFixture(path.join(root, "sandbox"));
  writeManifest(root);
  return root;
}

test("missing or altered installed inputs fail before spawn", (t) => {
  const root = fixture(t, "");
  verifyResources(root);
  fs.writeFileSync(path.join(root, "web/index.html"), "tampered");
  assert.throws(() => verifyResources(root), /integrity/);
  fs.unlinkSync(path.join(root, "bin/foundry"));
  assert.throws(() => verifyResources(root));
});

test("ASAR-bound manifest hash rejects tampering with both resource and manifest", (t) => {
  const root = fixture(t, "");
  const expected = crypto
    .createHash("sha256")
    .update(fs.readFileSync(path.join(root, "resources-manifest.json")))
    .digest("hex");
  verifyResources(root, expected);
  fs.writeFileSync(path.join(root, "web/index.html"), "tampered");
  const manifest = JSON.parse(
    fs.readFileSync(path.join(root, "resources-manifest.json")),
  );
  manifest.files["web/index.html"] = crypto
    .createHash("sha256")
    .update("tampered")
    .digest("hex");
  fs.writeFileSync(
    path.join(root, "resources-manifest.json"),
    JSON.stringify(manifest),
  );
  assert.throws(() => verifyResources(root, expected), /manifest integrity/);
});

test("installed web root is a closed manifest set, including nested files", (t) => {
  const root = fixture(t, "");
  fs.mkdirSync(path.join(root, "web", "added"));
  fs.writeFileSync(
    path.join(root, "web", "added", "unlisted.html"),
    "<script>active</script>",
  );
  assert.throws(() => verifyResources(root), /unlisted/i);
});
test("installed input directories cannot hide symlink ancestors", (t) => {
  const root = fixture(t, "");
  fs.renameSync(path.join(root, "web"), path.join(root, "moved-web"));
  fs.symlinkSync(path.join(root, "moved-web"), path.join(root, "web"));
  assert.throws(() => verifyResources(root), /symlink/i);
});
test("existing data roots and workbench symlinks fail closed without chmod", async (t) => {
  const body = `process.stdin.once('data',()=>console.log(JSON.stringify({protocol:1,url:'http://127.0.0.1:12345',pid:process.pid,dataDir:process.env.FOUNDRY_DATA_DIR})));process.stdin.on('end',()=>process.exit(0));`;
  for (const kind of [
    "insecure-root",
    "workbench-symlink",
    "root-symlink",
    "ancestor-symlink",
  ]) {
    const root = fixture(t, body);
    let dataRoot = path.join(root, "state");
    fs.mkdirSync(dataRoot, { mode: 0o700 });
    const target = path.join(root, "public-target");
    fs.mkdirSync(target, { mode: 0o755 });
    fs.chmodSync(target, 0o755);
    if (kind === "insecure-root") fs.chmodSync(dataRoot, 0o755);
    if (kind === "workbench-symlink")
      fs.symlinkSync(target, path.join(dataRoot, "workbench"));
    if (kind === "root-symlink") {
      dataRoot = path.join(root, "linked-state");
      fs.symlinkSync(target, dataRoot);
    }
    if (kind === "ancestor-symlink") {
      fs.symlinkSync(target, path.join(root, "link"));
      dataRoot = path.join(root, "link", "state");
    }
    let child;
    try {
      await assert.rejects(async () => {
        child = await startBackend({ resources: root, dataRoot });
      }, /private|symlink/i);
    } finally {
      if (child) await child.stop();
    }
    assert.equal(fs.statSync(target).mode & 0o777, 0o755);
  }
});

test("real pipe lifetime starts once, never exposes capability, and exits on EOF", async (t) => {
  const root = fixture(
    t,
    `let line='';process.stdin.on('data',x=>{line+=x;if(line.includes('\\n'))console.log(JSON.stringify({protocol:1,url:'http://127.0.0.1:12345',pid:process.pid,dataDir:process.env.FOUNDRY_DATA_DIR}));});process.stdin.on('end',()=>process.exit(0));`,
  );
  const child = await startBackend({
    resources: root,
    dataRoot: path.join(root, "state"),
  });
  assert.equal(child.ready.url, "http://127.0.0.1:12345");
  assert.equal(JSON.stringify(child.ready).includes(child.capability), false);
  await child.stop();
  assert.equal(child.process.exitCode, 0);
});

test("coding workbench uses exact closed argv, environment, and retained data directory", async (t) => {
  const root = fixture(
    t,
    `const fs=require('node:fs');fs.writeFileSync(require('node:path').join(process.cwd(),'observed.json'),JSON.stringify({argv:process.argv.slice(2),env:process.env,dataDir:process.env.FOUNDRY_DATA_DIR}));process.stdin.once('data',()=>console.log(JSON.stringify({protocol:1,url:'http://127.0.0.1:12345',pid:process.pid,dataDir:process.env.FOUNDRY_DATA_DIR})));process.stdin.on('end',()=>process.exit(0));`,
  );
  const dataRoot = path.join(root, "state");
  process.env.FOUNDRY_TEST_INJECTED = "must-not-cross-boundary";
  const previousBundle = process.env.FOUNDRY_VM_BUNDLE;
  process.env.FOUNDRY_VM_BUNDLE = "/ambient/untrusted-bundle";
  let child;
  try {
    child = await startBackend({
      resources: root,
      dataRoot,
      mode: "coding",
      args: ["--arbitrary-argument"],
      environment: { FOUNDRY_TEST_INJECTED: "injected" },
    });
    const observed = JSON.parse(
      fs.readFileSync(path.join(dataRoot, "observed.json")),
    );
    assert.deepEqual(observed.argv, ["--desktop", "--coding-workbench"]);
    assert.equal(observed.dataDir, path.join(dataRoot, "coding"));
    assert.equal(child.ready.dataDir, path.join(dataRoot, "coding"));
    assert.equal(observed.env.FOUNDRY_TEST_INJECTED, undefined);
    assert.equal(observed.env.FOUNDRY_DOCKER.includes("disabled"), true);
    assert.equal(observed.env.FOUNDRY_VM_BUNDLE, path.join(root, "sandbox"));
  } finally {
    delete process.env.FOUNDRY_TEST_INJECTED;
    if (previousBundle === undefined) delete process.env.FOUNDRY_VM_BUNDLE;
    else process.env.FOUNDRY_VM_BUNDLE = previousBundle;
    if (child) await child.stop();
  }
});

test("normal launch stays closed and coding mode cannot consume repository arguments", async (t) => {
  const root = fixture(
    t,
    `const fs=require('node:fs');fs.writeFileSync(require('node:path').join(process.cwd(),'normal.json'),JSON.stringify(process.argv.slice(2)));process.stdin.once('data',()=>console.log(JSON.stringify({protocol:1,url:'http://127.0.0.1:12345',pid:process.pid,dataDir:process.env.FOUNDRY_DATA_DIR})));process.stdin.on('end',()=>process.exit(0));`,
  );
  const dataRoot = path.join(root, "state");
  const child = await startBackend({ resources: root, dataRoot });
  assert.deepEqual(
    JSON.parse(fs.readFileSync(path.join(dataRoot, "normal.json"))),
    ["--desktop"],
  );
  assert.equal(child.ready.dataDir, path.join(dataRoot, "workbench"));
  await child.stop();
  await assert.rejects(
    startBackend({
      resources: root,
      dataRoot,
      mode: "coding",
      repository: path.join(root, "repo"),
    }),
    /repository/i,
  );
});

test("timeout, exit, malformed and oversized readiness stop without retry", async (t) => {
  for (const body of [
    "setInterval(()=>{},1000);",
    "process.exit(2);",
    'console.log("not json");setInterval(()=>{},1000);',
    'console.log("x".repeat(9000));setInterval(()=>{},1000);',
  ]) {
    const root = fixture(t, body);
    await assert.rejects(
      startBackend({
        resources: root,
        dataRoot: path.join(root, "state"),
        timeout: 150,
      }),
    );
  }
});
