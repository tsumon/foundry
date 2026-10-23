"use strict";
const test = require("node:test"), assert = require("node:assert/strict");
const fs = require("node:fs"), path = require("node:path"), os = require("node:os"), crypto = require("node:crypto");
let build = {}; try { build = require("../sandbox/windows-source-build.cjs"); } catch (e) { if (e.code !== "MODULE_NOT_FOUND") throw e; }
test("minimal configuration keeps Q35 and WHPX but excludes TCG, GUI, audio and optional devices", () => {
  assert.equal(typeof build.minimalBuildPlan, "function", "Minimal source build is missing");
  const p = build.minimalBuildPlan();
  assert.ok(p.configure.includes("--disable-tcg")); assert.ok(p.configure.includes("--enable-whpx"));
  assert.ok(p.configure.includes("--without-default-features")); assert.ok(p.configure.includes("--without-default-devices"));
  assert.ok(p.configure.includes("--disable-download")); assert.ok(p.configure.includes("--audio-drv-list="));
  assert.equal(p.devices, "CONFIG_Q35=y\n"); assert.deepEqual(p.targets, ["qemu-system-x86_64.exe"]);
});
test("audits generated feature values and devices, not just requested configure flags", () => {
  assert.equal(typeof build.auditConfiguration, "function", "Generated configuration validation is missing");
  const options = ["whpx", "tcg", "gtk", "jack", "slirp", "virtfs", "libusb", "modules"].map(name => ({ name, type: "combo", choices: ["enabled", "disabled", "auto"], section: "user", value: name === "whpx" ? "enabled" : "disabled" }));
  options.push({ name: "default_devices", value: false }, { name: "plugins", value: false }, { name: "audio_drv_list", value: [] });
  const input = { options, devices: "CONFIG_Q35=y\nCONFIG_AHCI_ICH9=y\nCONFIG_SERIAL_ISA=y\nCONFIG_FW_CFG_DMA=y\n", host: "#define CONFIG_WHPX 1\n" };
  assert.equal(build.auditConfiguration(input), true);
  for (const name of ["tcg", "gtk", "jack", "slirp"]) { const bad = structuredClone(input); bad.options.find(x => x.name === name).value = "enabled"; assert.throws(() => build.auditConfiguration(bad), /feature/i); }
  assert.throws(() => build.auditConfiguration({ ...input, devices: input.devices + "CONFIG_USB_XHCI=y\n" }), /device/i);
  assert.throws(() => build.auditConfiguration({ ...input, host: input.host + "#define CONFIG_TCG 1\n" }), /TCG/);
  assert.throws(() => build.auditConfiguration({ ...input, options: options.filter(o => o.name !== "whpx") }), /WHPX/);
  assert.throws(() => build.auditConfiguration({ ...input, options: options.filter(o => o.name !== "jack") }), /feature/i);
});
test("minimal closure rejects a reachable UI/audio dependency even when all DLLs are present", () => {
  assert.equal(typeof build.assertMinimalClosure, "function", "Minimal closure policy is missing");
  const lock = { windows: { allowed_runtime_dlls: ["libglib-2.0-0.dll"] } };
  assert.equal(build.assertMinimalClosure([{ file: "qemu-system-x86_64.exe" }, { file: "libglib-2.0-0.dll" }], lock), true);
  assert.throws(() => build.assertMinimalClosure([{ file: "qemu-system-x86_64.exe" }, { file: "libjack64.dll" }], lock), /forbidden/i);
  assert.throws(() => build.assertMinimalClosure([{ file: "qemu-system-x86_64.exe" }, { file: "libdb-6.2.dll" }], lock), /forbidden/i);
});
test("source delivery includes verified bytes, installed notices and a source-kit identity, not just URLs", t => {
  assert.equal(typeof build.assembleLegal, "function", "Source delivery is missing");
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-source-kit-")); t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const source = Buffer.from("complete source fixture"), notice = Buffer.from("Copyright and GPL text fixture"), sha = b => crypto.createHash("sha256").update(b).digest("hex");
  fs.mkdirSync(path.join(root, "inputs")); fs.mkdirSync(path.join(root, "source")); fs.mkdirSync(path.join(root, "output"));
  fs.writeFileSync(path.join(root, "inputs", "source.tar"), source); fs.writeFileSync(path.join(root, "source", "COPYING"), notice);
  const lock = { assets: [{ file: "source.tar", bytes: source.length, sha256: sha(source), kind: "source", license: "GPL-2.0-or-later", provenance: "fixture", platforms: ["windows/amd64"], url: "https://repo.msys2.org/mingw/sources/source.tar" }], windows: { qemu_version: "11.1.2", source_archives: ["source.tar"], notices: [{ archive: "source.tar", member: "qemu.tar.xz", inner_member: "qemu-11.1.2/COPYING", path: "legal/licenses/qemu/COPYING", bytes: notice.length, sha256: sha(notice) }] } };
  const result = build.assembleLegal({ lock, inputsRoot: path.join(root, "inputs"), sourceRoot: path.join(root, "source"), outputRoot: path.join(root, "output") });
  assert.equal(result.files.length, 7); assert.match(result.digest, /^[a-f0-9]{64}$/);
  for (const name of ["windows-zlib.cjs", "zlib-regression.c"]) assert.ok(result.files.some(e => e.file === name), "Patched zlib recipe must accompany sources");
  assert.match(result.files.find(f => f.file === "windows-source-build.cjs").sha256, /^[a-f0-9]{64}$/);
  assert.deepEqual(fs.readFileSync(path.join(root, "output/source-kit/source.tar")), source);
  assert.deepEqual(fs.readFileSync(path.join(root, "output/legal/licenses/qemu/COPYING")), notice);
  assert.throws(() => build.assembleLegal({ lock, inputsRoot: path.join(root, "inputs"), sourceRoot: path.join(root, "source"), outputRoot: path.join(root, "output") }), /exist/i);
});
test("build bootstrap verifies exact regular tool bytes before package manager execution", t => {
  assert.equal(typeof build.verifyBootstrapRoot, "function", "Bootstrap verification is missing");
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-msys-bootstrap-")); t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.mkdirSync(path.join(root, "usr/bin"), { recursive: true }); const bytes = Buffer.from("pinned bootstrap tool fixture");
  const tools = ["bash.exe", "bsdtar.exe", "pacman.exe"].map(n => { fs.writeFileSync(path.join(root, "usr/bin", n), bytes); return { path: "usr/bin/" + n, bytes: bytes.length, sha256: crypto.createHash("sha256").update(bytes).digest("hex") }; });
  const lock = { windows: { bootstrap: { root: "msys64", tools } } };
  assert.equal(build.verifyBootstrapRoot({ lock, msysRoot: root }), true);
  fs.writeFileSync(path.join(root, "usr/bin/pacman.exe"), "changed");
  assert.throws(() => build.verifyBootstrapRoot({ lock, msysRoot: root }), /integrity/i);
});
