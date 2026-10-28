"use strict";
const fs = require("node:fs"),
  path = require("node:path"),
  crypto = require("node:crypto");
const { createRequire } = require("node:module");
const { compileFunction } = require("node:vm");

// Run platform-neutral contract tests against an explicit supported fixture
// host. This local process view never changes the real host, and the shipping
// modules retain their native platform checks and default resource verifier.
function loadDesktopFixtureModule(
  name,
  host = process.platform === "win32" ? "windows/amd64" : "darwin/arm64",
) {
  const platforms = {
    "darwin/arm64": { platform: "darwin", arch: "arm64" },
    "windows/amd64": { platform: "win32", arch: "x64" },
  };
  if (!Object.hasOwn(platforms, host)) throw Error("Unsupported fixture host");
  const directory = path.resolve(__dirname, ".."),
    files = new Set(["runtime.cjs", "native-vm-smoke.cjs"]),
    cache = new Map(),
    fixtureProcess = Object.create(process);
  Object.defineProperties(fixtureProcess, {
    platform: { value: platforms[host].platform },
    arch: { value: platforms[host].arch },
  });
  function load(file) {
    if (!files.has(path.basename(file)) || path.dirname(file) !== directory)
      throw Error("Unsupported fixture module");
    if (cache.has(file)) return cache.get(file).exports;
    const fixtureModule = { exports: {} },
      originalRequire = createRequire(file);
    cache.set(file, fixtureModule);
    function fixtureRequire(request) {
      const resolved = originalRequire.resolve(request);
      return path.dirname(resolved) === directory &&
        files.has(path.basename(resolved))
        ? load(resolved)
        : originalRequire(request);
    }
    fixtureRequire.main = require.main;
    compileFunction(
      fs.readFileSync(file, "utf8"),
      ["require", "module", "exports", "__filename", "__dirname", "process"],
      { filename: file },
    )(
      fixtureRequire,
      fixtureModule,
      fixtureModule.exports,
      file,
      directory,
      fixtureProcess,
    );
    return fixtureModule.exports;
  }
  return load(path.join(directory, name));
}
function sandboxFixture(
  root,
  host = process.platform === "win32" ? "windows/amd64" : "darwin/arm64",
) {
  fs.mkdirSync(root, { recursive: true });
  const manifest = {
    schema_version: 1,
    host_platform: host,
    guest_platform: host === "windows/amd64" ? "linux/amd64" : "linux/arm64",
    protocol: "foundry-vm-v1",
    policy_version: "go-vm-v1",
    runtime:
      host === "windows/amd64"
        ? "runtime/qemu-system-x86_64.exe"
        : "runtime/foundry-vm-helper",
    kernel: "kernel",
    rootfs: "initramfs.cpio.gz",
    verifier: "foundry-vm-guest",
    assets: [],
  };
  for (const name of [
    manifest.runtime,
    manifest.kernel,
    manifest.rootfs,
    manifest.verifier,
    "legal/NOTICE.md",
    "legal/GPL-2.0.txt",
  ]) {
    const body = Buffer.from("inert fixture " + name);
    fs.mkdirSync(path.dirname(path.join(root, name)), { recursive: true });
    fs.writeFileSync(path.join(root, name), body, {
      mode: name === manifest.runtime ? 0o755 : 0o644,
    });
    manifest.assets.push({
      path: name,
      size: body.length,
      sha256: crypto.createHash("sha256").update(body).digest("hex"),
    });
  }
  fs.writeFileSync(path.join(root, "manifest.json"), JSON.stringify(manifest));
  return manifest;
}
function v2Fixture(root,host='darwin/arm64') {
  const m=sandboxFixture(root,host);m.schema_version=2;m.protocol='foundry-vm-v2';m.policy_version='multilang-vm-v2';
  fs.renameSync(path.join(root,m.verifier),path.join(root,'foundry-vm-guest-v2'));m.assets.find(a=>a.path===m.verifier).path='foundry-vm-guest-v2';m.verifier='foundry-vm-guest-v2';
  const d=JSON.parse(fs.readFileSync(path.resolve(__dirname,'../../internal/vmruntime/testdata/runtime-inventory-v2.json')));d.guest_platform=m.guest_platform;
  const raw=fs.readFileSync(path.join(root,m.verifier));d.supervisor={path:'/init',size:raw.length,sha256:crypto.createHash('sha256').update(raw).digest('hex')};
  const descriptor=Buffer.from(require('../runtime-inventory-v2.cjs').canonicalInventory(d));
  m.runtime_inventory='runtime-inventory-v2.json';m.runtime_inventory_digest=crypto.createHash('sha256').update('foundry:runtime-inventory:v2\n').update(descriptor).digest('hex');
  fs.writeFileSync(path.join(root,m.runtime_inventory),descriptor);m.assets.push({path:m.runtime_inventory,size:descriptor.length,sha256:crypto.createHash('sha256').update(descriptor).digest('hex')});
  const notice=Buffer.from(JSON.stringify({schema_version:1,sidecar:'sandbox-v2-sources',files:[]}));fs.writeFileSync(path.join(root,'legal/sources.json'),notice);m.assets.push({path:'legal/sources.json',size:notice.length,sha256:crypto.createHash('sha256').update(notice).digest('hex')});
  const components=Buffer.from('{}');fs.writeFileSync(path.join(root,'legal/runtime-components.json'),components);m.assets.push({path:'legal/runtime-components.json',size:components.length,sha256:crypto.createHash('sha256').update(components).digest('hex')});
  for(const n of ['Node-LICENSE','TypeScript-LICENSE.txt','TypeScript-ThirdPartyNoticeText.txt','Go-LICENSE','Go-PATENTS']){const raw=Buffer.from('inert legal text');fs.writeFileSync(path.join(root,'legal',n),raw);m.assets.push({path:'legal/'+n,size:raw.length,sha256:crypto.createHash('sha256').update(raw).digest('hex')});}
  fs.writeFileSync(path.join(root,'manifest.json'),JSON.stringify(m));return m;
}

module.exports = { sandboxFixture, v2Fixture, loadDesktopFixtureModule };
