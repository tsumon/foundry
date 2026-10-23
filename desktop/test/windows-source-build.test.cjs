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
function generatedConfiguration() {
  const options = ["whpx", "tcg", "gtk", "jack", "slirp", "virtfs", "libusb", "modules"].map(name => ({ name, type: "combo", choices: ["enabled", "disabled", "auto"], section: "user", value: name === "whpx" ? "enabled" : "disabled" }));
  options.push({ name: "auto_features", type: "combo", choices: ["enabled", "disabled", "auto"], section: "core", value: "disabled" });
  options.push({ name: "default_devices", value: false }, { name: "plugins", value: false }, { name: "audio_drv_list", value: [] });
  return { options, devices: "CONFIG_Q35=y\nCONFIG_AHCI_ICH9=y\nCONFIG_SERIAL_ISA=y\nCONFIG_FW_CFG_DMA=y\n", host: "#define CONFIG_WHPX 1\n" };
}
test("audits generated feature values and devices, not just requested configure flags", () => {
  assert.equal(typeof build.auditConfiguration, "function", "Generated configuration validation is missing");
  const input = generatedConfiguration(), { options } = input;
  assert.equal(build.auditConfiguration(input), true);
  for (const name of ["tcg", "gtk", "jack", "slirp"]) { const bad = structuredClone(input); bad.options.find(x => x.name === name).value = "enabled"; assert.throws(() => build.auditConfiguration(bad), /feature/i); }
  assert.throws(() => build.auditConfiguration({ ...input, devices: input.devices + "CONFIG_USB_XHCI=y\n" }), /device/i);
  assert.throws(() => build.auditConfiguration({ ...input, host: input.host + "#define CONFIG_TCG 1\n" }), /TCG/);
  assert.throws(() => build.auditConfiguration({ ...input, options: options.filter(o => o.name !== "whpx") }), /WHPX/);
  assert.throws(() => build.auditConfiguration({ ...input, options: options.filter(o => o.name !== "jack") }), /feature/i);
});
test("Meson raw auto features are disabled only by verified core auto_features=disabled", () => {
  const input = generatedConfiguration();
  // QEMU 11.1.2 attr is a feature defaulting to auto; Meson 1.12.1
  // introspection retains opt.value, not the effective get_option value.
  input.options.push({ name: "attr", type: "combo", choices: ["enabled", "disabled", "auto"], section: "user", value: "auto" });
  assert.equal(build.auditConfiguration(input), true);
  for (const value of ["auto", "enabled", "invalid", undefined]) {
    const bad = structuredClone(input); bad.options.find(o => o.name === "auto_features").value = value;
    assert.throws(() => build.auditConfiguration(bad), /auto_features/i);
  }
  for (const field of ["type", "choices", "section"]) {
    const bad = structuredClone(input); delete bad.options.find(o => o.name === "auto_features")[field];
    assert.throws(() => build.auditConfiguration(bad), /auto_features/i);
  }
  assert.throws(() => build.auditConfiguration({ ...input, options: input.options.filter(o => o.name !== "auto_features") }), /auto_features/i);
  const duplicate = structuredClone(input); duplicate.options.push({ ...duplicate.options.find(o => o.name === "auto_features") });
  assert.throws(() => build.auditConfiguration(duplicate), /duplicate/i);
});
test("effective auto handling does not permit explicit enabled, forbidden, or subproject features", () => {
  for (const option of [
    { name: "attr", section: "user", value: "enabled" },
    { name: "attr", section: "user", value: "invalid" },
    { name: "attr", section: "core", value: "auto" },
    { name: "subproject:attr", section: "user", value: "auto" },
  ]) {
    const input = generatedConfiguration(); input.options.push({ type: "combo", choices: ["enabled", "disabled", "auto"], ...option });
    assert.throws(() => build.auditConfiguration(input), /feature/i);
  }
  for (const name of ["tcg", "gtk", "jack", "slirp", "virtfs", "libusb", "modules"]) {
    const input = generatedConfiguration(); input.options.find(o => o.name === name).value = "auto";
    assert.throws(() => build.auditConfiguration(input), /feature/i);
  }
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
function keyringFixture(t) {
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-msys-keyring-")); t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const names = ["usr/bin/pacman-key", "usr/bin/gpg.exe", "usr/bin/gpgconf.exe", "usr/share/pacman/keyrings/msys2.gpg", "usr/share/pacman/keyrings/msys2-trusted", "usr/share/pacman/keyrings/msys2-revoked", "etc/pacman.conf"];
  const files = names.map(name => { const raw = Buffer.from("pinned fixture: " + name), target = path.join(root, name); fs.mkdirSync(path.dirname(target), { recursive: true }); fs.writeFileSync(target, raw); return { path: name, bytes: raw.length, sha256: crypto.createHash("sha256").update(raw).digest("hex") }; });
  return { msysRoot: root, lock: { windows: { bootstrap: { keyring: files } } } };
}
test("native build logging preserves successful output and never dispatches unknown steps", t => {
  assert.equal(typeof build.runLoggedBuildStep, "function");
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-build-log-")); t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  let calls = 0; const options = { outputRoot: root, step: "configure", program: "fixed-tool", args: ["fixed-argument"], cwd: root, run: (program, args, cwd) => { calls++; assert.equal(program, "fixed-tool"); assert.deepEqual(args, ["fixed-argument"]); assert.equal(cwd, root); return "configure complete\n"; }, report: () => assert.fail("Success must not report a failure") };
  assert.equal(build.runLoggedBuildStep(options), "configure complete\n");
  assert.equal(fs.readFileSync(path.join(root, "configure.log"), "utf8"), "configure complete\n");
  assert.throws(() => build.runLoggedBuildStep({ ...options, step: "../outside" }), /step/i); assert.equal(calls, 1);
});
test("native build failure retains both streams, reports bounded diagnostics and never retries", t => {
  assert.equal(typeof build.runLoggedBuildStep, "function");
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-build-log-")); t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const failure = Object.assign(Error("exit 1"), { stdout: Buffer.from("a".repeat(100000) + "\x1b[31mMeson: missing required input\x00\n##[add-mask]fixture\n::error::fixture\n"), stderr: "setup failed\n" });
  let calls = 0; const reports = [];
  assert.throws(() => build.runLoggedBuildStep({ outputRoot: root, step: "configure", run: () => { calls++; throw failure; }, report: s => reports.push(s) }), e => e === failure);
  assert.equal(calls, 1); assert.deepEqual(fs.readFileSync(path.join(root, "configure.log")), failure.stdout);
  assert.equal(fs.readFileSync(path.join(root, "configure.stderr.log"), "utf8"), failure.stderr);
  assert.equal(reports.length, 1); assert.ok(Buffer.byteLength(reports[0]) < 66000); assert.match(reports[0], /Meson: missing required input/); assert.doesNotMatch(reports[0], /[\x00\x1b]/);
  assert.doesNotMatch(reports[0], /##\[/); assert.doesNotMatch(reports[0], /^\s*::/m);
});
test("native build logging caps retained failure bytes and propagates an empty-output failure", t => {
  assert.equal(typeof build.runLoggedBuildStep, "function");
  for (const stdout of [undefined, Buffer.alloc(33 * 1024 * 1024, 97), Buffer.alloc(100000, 255)]) {
    const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-build-log-")); t.after(() => fs.rmSync(root, { recursive: true, force: true }));
    const failure = Object.assign(Error("exit 1"), { stdout }); let calls = 0;
    assert.throws(() => build.runLoggedBuildStep({ outputRoot: root, step: "ninja", run: () => { calls++; throw failure; }, report: value => assert.ok(Buffer.byteLength(value) < 66000) }), e => e === failure);
    assert.equal(calls, 1); assert.equal(fs.statSync(path.join(root, "ninja.log")).size, stdout ? Math.min(stdout.length, 32 * 1024 * 1024) : 0);
    assert.equal(fs.statSync(path.join(root, "ninja.stderr.log")).size, 0);
  }
});
test("offline keyring setup precedes package installation without login, network key refresh or weakened signatures", t => {
  assert.equal(typeof build.installBuildPackages, "function", "Offline keyring setup is missing");
  const fixture = keyringFixture(t), calls = [];
  build.installBuildPackages({ ...fixture, archives: [{ file: "compiler.pkg.tar.zst" }], inputs: path.join(fixture.msysRoot, "inputs"), run: (program, args) => { calls.push({ program, args }); return ""; } });
  const bin = name => path.join(fixture.msysRoot, "usr/bin", name);
  assert.deepEqual(calls, [
    { program: bin("bash.exe"), args: ["--noprofile", "--norc", "/usr/bin/pacman-key", "--gpgdir", "/etc/pacman.d/gnupg", "--init"] },
    { program: bin("bash.exe"), args: ["--noprofile", "--norc", "/usr/bin/pacman-key", "--gpgdir", "/etc/pacman.d/gnupg", "--populate", "msys2"] },
    { program: bin("gpgconf.exe"), args: ["--homedir", "/etc/pacman.d/gnupg", "--kill", "all"] },
    { program: bin("pacman.exe"), args: ["-U", "--noconfirm", path.join(fixture.msysRoot, "inputs/compiler.pkg.tar.zst")] },
  ]);
});
test("keyring setup refuses tampered, missing, symlinked or unpinned trust material before any command", t => {
  assert.equal(typeof build.installBuildPackages, "function", "Offline keyring setup is missing");
  for (const damage of ["tampered", "missing", "symlink", "unpinned"]) {
    const fixture = keyringFixture(t), target = path.join(fixture.msysRoot, "usr/share/pacman/keyrings/msys2.gpg"); let calls = 0;
    if (damage === "tampered") fs.writeFileSync(target, "changed");
    if (damage === "missing" || damage === "symlink") fs.unlinkSync(target);
    if (damage === "symlink") fs.symlinkSync(path.join(fixture.msysRoot, "usr/share/pacman/keyrings/msys2-trusted"), target);
    if (damage === "unpinned") fixture.lock.windows.bootstrap.keyring.pop();
    assert.throws(() => build.installBuildPackages({ ...fixture, archives: [], inputs: fixture.msysRoot, run: () => calls++ })); assert.equal(calls, 0);
  }
});
test("keyring command failure stops before installing packages and does not retry", t => {
  assert.equal(typeof build.installBuildPackages, "function", "Offline keyring setup is missing");
  for (const failedAt of [1, 2, 3]) {
    const fixture = keyringFixture(t); let calls = 0;
    assert.throws(() => build.installBuildPackages({ ...fixture, archives: [], inputs: fixture.msysRoot, run: () => { if (++calls === failedAt) throw Error("fixture keyring failure"); } }), /fixture keyring failure/);
    assert.equal(calls, failedAt);
  }
});
test("the local package transaction supplies QEMU offline Python tooling and pip's distlib dependency", t => {
  const fixture = keyringFixture(t), lock = require("../sandbox/inputs.lock.json");
  const archives = lock.windows.build_packages.map(name => lock.assets.find(e => e.package === name));
  let installed;
  build.installBuildPackages({ ...fixture, archives, inputs: fixture.msysRoot, run: (program, args) => {
    if (path.basename(program) === "pacman.exe") installed = args.slice(2).map(p => path.basename(p));
    return "";
  } });
  for (const name of ["setuptools", "wheel", "pip", "distlib", "packaging"]) {
    const prefix = "mingw-w64-ucrt-x86_64-python-" + name + "-";
    assert.equal(installed.filter(file => file.startsWith(prefix)).length, 1, "Missing local offline Python tooling: " + name);
  }
  assert.equal(installed.filter(file => file === "diffutils-3.12-1-x86_64.pkg.tar.zst").length, 1, "QAPI configure requires the pinned local diff tool");
});
function sourceFixture(t, entries) {
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-qemu-source-")); t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const { Header } = require("tar"), chunks = [];
  for (const e of entries) {
    const raw = Buffer.from(e.content || ""), h = new Header({ path: e.path, type: e.type || "File", linkpath: e.linkpath, size: raw.length, mode: 0o755 });
    h.encode(); chunks.push(h.block, raw, Buffer.alloc((512 - raw.length % 512) % 512));
  }
  const raw = Buffer.concat([...chunks, Buffer.alloc(1024)]), sourceTar = path.join(root, "source.tar"); fs.writeFileSync(sourceTar, raw);
  return { outputRoot: root, sourceTar, lock: { windows: { qemu_version: "1.2.3", source_sha256: crypto.createHash("sha256").update(raw).digest("hex") } } };
}
test("native source extraction preserves forward file/directory references and dangling links", t => {
  assert.equal(typeof build.extractQemuSource, "function", "Native source extraction is missing");
  const fixture = sourceFixture(t, [
    { path: "qemu-1.2.3/forward-file", type: "SymbolicLink", linkpath: "later/file" },
    { path: "qemu-1.2.3/forward-dir", type: "SymbolicLink", linkpath: "later" },
    { path: "qemu-1.2.3/dangling", type: "SymbolicLink", linkpath: "not-present" },
    { path: "qemu-1.2.3/later/", type: "Directory" },
    { path: "qemu-1.2.3/later/file", content: "exact source bytes" },
  ]);
  const source = build.extractQemuSource(fixture);
  assert.equal(source, path.join(fixture.outputRoot, "qemu-1.2.3"));
  for (const [name, target] of [["forward-file", "later/file"], ["forward-dir", "later"], ["dangling", "not-present"]]) {
    assert.equal(fs.lstatSync(path.join(source, name)).isSymbolicLink(), true, "Link must not become a deep copy");
    assert.equal(fs.readlinkSync(path.join(source, name)).replaceAll("\\", "/"), target);
  }
  assert.equal(fs.readFileSync(path.join(source, "forward-file"), "utf8"), "exact source bytes");
  assert.equal(fs.readFileSync(path.join(source, "forward-dir/file"), "utf8"), "exact source bytes");
  assert.equal(fs.existsSync(path.join(source, "dangling")), false);
  assert.throws(() => build.extractQemuSource(fixture), /fresh/i);
});
test("native source extraction refuses digest drift and unsafe members before extraction", t => {
  assert.equal(typeof build.extractQemuSource, "function", "Native source extraction is missing");
  const fixture = sourceFixture(t, [{ path: "qemu-1.2.3/safe", content: "source" }]); let calls = 0;
  fixture.lock.windows.source_sha256 = "0".repeat(64);
  assert.throws(() => build.extractQemuSource({ ...fixture, run: () => calls++ }), /integrity/i); assert.equal(calls, 0);
  for (const member of ["../outside", "qemu-1.2.3/../../outside", "other/file", "qemu-1.2.3/a\\b"]) {
    const unsafe = sourceFixture(t, [{ path: member, content: "unsafe" }]);
    assert.throws(() => build.extractQemuSource(unsafe), /unsafe/i);
    assert.equal(fs.existsSync(path.join(unsafe.outputRoot, "qemu-1.2.3")), false);
  }
});
test("source extraction uses an absolute native tool and never retries a failing extraction", t => {
  assert.equal(typeof build.extractQemuSource, "function", "Native source extraction is missing");
  const fixture = sourceFixture(t, [{ path: "qemu-1.2.3/file", content: "source" }]), calls = [];
  assert.throws(() => build.extractQemuSource({ ...fixture, run: (program, args) => {
    calls.push({ program, args }); if (calls.length === 1) return "qemu-1.2.3/file\r\n"; throw Error("fixture extraction error");
  } }), /fixture extraction error/);
  const tar = process.platform === "win32" ? path.join(process.env.SystemRoot, "System32/tar.exe") : "/usr/bin/tar";
  assert.deepEqual(calls, [
    { program: tar, args: ["-tf", fixture.sourceTar] },
    { program: tar, args: ["-xf", fixture.sourceTar, "-C", fixture.outputRoot, "--no-same-owner", "--no-same-permissions"] },
  ]);
});
