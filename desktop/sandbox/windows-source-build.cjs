"use strict";
// Run only on a disposable Windows build worker with a dedicated MSYS2 root.
// The installed application never invokes this build/acquisition code.
const fs = require("node:fs"), path = require("node:path"), cp = require("node:child_process"), crypto = require("node:crypto");
const { prepareAssets, verifyAsset } = require("./assets.cjs");
const { readPEImports, validateClosure, isSystemImport } = require("./windows-bundle.cjs");
const digest = raw => crypto.createHash("sha256").update(raw).digest("hex");
function minimalBuildPlan() {
  return { configure: ["--target-list=x86_64-softmmu", "--without-default-features", "--without-default-devices", "--with-devices-x86_64=foundry", "--disable-download", "--disable-tcg", "--enable-whpx", "--enable-stack-protector", "--disable-modules", "--disable-plugins", "--disable-gtk", "--disable-sdl", "--disable-vnc", "--disable-opengl", "--disable-curses", "--audio-drv-list=", "--disable-jack", "--disable-dsound", "--disable-spice", "--disable-slirp", "--disable-curl", "--disable-libssh", "--disable-libnfs", "--disable-virtfs", "--disable-libusb", "--disable-usb-redir", "--disable-smartcard", "--disable-gio", "--disable-pixman", "--disable-fdt", "--disable-guest-agent", "--disable-tools", "--disable-docs"], devices: "CONFIG_Q35=y\n", targets: ["qemu-system-x86_64.exe"] };
}
function auditConfiguration({ options, devices, host }) {
  if (!Array.isArray(options) || typeof devices !== "string" || typeof host !== "string") throw Error("Generated configuration evidence required");
  const map = new Map(options.map(o => [o.name, o]));
  if (map.get("whpx")?.value !== "enabled" || !/^#define CONFIG_WHPX 1$/m.test(host)) throw Error("WHPX must be compiled in");
  if (map.get("tcg")?.value !== "disabled" || /^#define CONFIG_TCG\b/m.test(host)) throw Error("TCG feature must be absent");
  if (map.size !== options.length) throw Error("Duplicate generated feature option");
  for (const name of ["gtk", "jack", "slirp", "virtfs", "libusb", "modules"]) if (map.get(name)?.value !== "disabled") throw Error("Missing or enabled forbidden feature: " + name);
  for (const o of options) if (o.type === "combo" && Array.isArray(o.choices) && ["enabled", "disabled", "auto"].every(v => o.choices.includes(v)) && !["whpx", "stack_protector"].includes(o.name) && o.value !== "disabled") throw Error("Unexpected enabled/automatic feature: " + o.name);
  for (const name of ["default_devices", "plugins"]) if (map.get(name)?.value !== false) throw Error("Unexpected default device/plugin feature");
  if (!Array.isArray(map.get("audio_drv_list")?.value) || map.get("audio_drv_list").value.length) throw Error("Audio feature must be empty");
  if (!/^CONFIG_Q35=y$/m.test(devices) || !/^CONFIG_SERIAL_ISA=y$/m.test(devices) || !/^CONFIG_FW_CFG_DMA=y$/m.test(devices)) throw Error("Required Q35 serial/firmware devices absent");
  if (/^CONFIG_(?:USB[^=]*|LSI[^=]*|E1000[^=]*|RTL[^=]*|NE2000[^=]*|VIRTIO_NET|VIRTIO_9P|VIRTFS|I440FX|ISAPC|MICROVM|NITRO_ENCLAVE|SOUND[^=]*|AC97|HDA|SB16|ES1370)=y$/m.test(devices)) throw Error("Forbidden device compiled in");
  // Q35 hard-selects AHCI_ICH9 upstream. The host's fixed sata=off,usb=off
  // invocation prevents instantiation; removing this requires another machine.
  return true;
}
function assertMinimalClosure(imports, lock) {
  const allowed = new Set(lock.windows.allowed_runtime_dlls);
  for (const e of imports) if (e.file !== "qemu-system-x86_64.exe" && !allowed.has(e.file)) throw Error("Forbidden nonminimal runtime import: " + e.file);
  return true;
}
function regularDirectory(p) {
  if (!path.isAbsolute(p)) throw Error("Absolute build directory required");
  let current = path.parse(p).root; for (const part of path.relative(current, p).split(path.sep).filter(Boolean)) { current = path.join(current, part); if (!fs.lstatSync(current).isDirectory()) throw Error("Regular non-symlink build directory required"); }
}
function verifyBootstrapRoot({ lock, msysRoot }) {
  regularDirectory(msysRoot);
  const bootstrap = lock.windows.bootstrap;
  if (bootstrap?.root !== "msys64" || !Array.isArray(bootstrap.tools) || bootstrap.tools.length !== 3 || new Set(bootstrap.tools.map(t => t.path)).size !== 3) throw Error("Pinned MSYS2 bootstrap tools required");
  for (const t of bootstrap.tools) {
    if (!/^usr\/bin\/(?:bash|bsdtar|pacman)\.exe$/.test(t.path)) throw Error("Unsafe bootstrap tool path");
    const file = path.join(msysRoot, t.path); regularDirectory(path.dirname(file)); if (!fs.lstatSync(file).isFile()) throw Error("Regular bootstrap tool required");
    const raw = fs.readFileSync(file); if (raw.length !== t.bytes || digest(raw) !== t.sha256) throw Error("Bootstrap tool integrity failure");
  }
  return true;
}
async function bootstrapWindowsBuildHost({ lock, outputRoot, cacheRoot }) {
  if (process.platform !== "win32" || process.arch !== "x64") throw Error("Native Windows x64 bootstrap worker required");
  if (!path.isAbsolute(outputRoot) || fs.existsSync(outputRoot)) throw Error("Fresh absolute bootstrap output required");
  regularDirectory(path.dirname(outputRoot)); const bootstrap = lock.windows.bootstrap, entry = lock.assets.find(e => e.file === bootstrap?.archive);
  if (!entry || bootstrap.root !== "msys64") throw Error("Pinned bootstrap archive required");
  fs.mkdirSync(outputRoot); const inputs = path.join(outputRoot, "inputs");
  await prepareAssets({ output: inputs, local: cacheRoot, entries: [entry], platform: "windows/amd64" });
  const archive = path.join(inputs, entry.file), tar = path.join(process.env.SystemRoot, "System32", "tar.exe");
  const members = cp.execFileSync(tar, ["-tf", archive], { encoding: "utf8", maxBuffer: 8 * 1024 * 1024, timeout: 120000 }).trim().split("\n");
  if (members.some(n => !n.startsWith("msys64/") || n.split("/").includes("..") || n.includes("\\"))) throw Error("Unsafe bootstrap archive path");
  cp.execFileSync(tar, ["-xf", archive, "-C", outputRoot, "--no-same-owner", "--no-same-permissions"], { timeout: 120000 });
  const msysRoot = path.join(outputRoot, "msys64"); verifyBootstrapRoot({ lock, msysRoot });
  fs.writeFileSync(path.join(outputRoot, "bootstrap-receipt.json"), JSON.stringify({ archive: entry, tools: bootstrap.tools }, null, 2) + "\n", { flag: "wx" });
  return { msysRoot };
}
function assembleLegal({ lock, inputsRoot, sourceRoot, outputRoot, tar = "tar" }) {
  for (const directory of [inputsRoot, sourceRoot, outputRoot]) regularDirectory(directory);
  if (!lock.windows.source_archives?.length || !lock.windows.notices?.length) throw Error("Corresponding source and license notices required");
  const legalRoot = path.join(outputRoot, "legal"), kitRoot = path.join(outputRoot, "source-kit");
  fs.mkdirSync(legalRoot); fs.mkdirSync(kitRoot);
  const files = [];
  for (const name of lock.windows.source_archives) {
    const e = lock.assets.find(e => e.file === name), file = path.join(inputsRoot, name);
    if (!e || e.kind !== "source" || !fs.lstatSync(file).isFile()) throw Error("Regular corresponding source required");
    const raw = fs.readFileSync(file); verifyAsset(e, raw); fs.writeFileSync(path.join(kitRoot, name), raw, { flag: "wx" });
    files.push({ file: name, bytes: raw.length, sha256: digest(raw), url: e.url, license: e.license });
  }
  for (const f of lock.windows.notices) {
    if (!/^legal\/licenses\/[a-zA-Z0-9._+/-]+$/.test(f.path) || f.path.split("/").includes("..") || !Number.isSafeInteger(f.bytes) || f.bytes < 1 || f.bytes > 4 * 1024 * 1024) throw Error("Unsafe license notice path");
    let raw;
    if (f.inner_member && f.inner_member.startsWith("qemu-" + lock.windows.qemu_version + "/")) {
      const prefix = "qemu-" + lock.windows.qemu_version + "/";
      if (!f.inner_member.startsWith(prefix) || f.inner_member.split("/").includes("..")) throw Error("Unsafe source notice path");
      const file = path.join(sourceRoot, f.inner_member.slice(prefix.length)); if (!fs.lstatSync(file).isFile()) throw Error("Regular source notice required"); raw = fs.readFileSync(file);
    } else {
      const e = lock.assets.find(e => e.file === f.archive); if (!e || f.member.startsWith("/") || f.member.includes("..") || f.member.includes("\\")) throw Error("Unknown notice archive");
      const archive = path.join(inputsRoot, f.archive); if (!fs.lstatSync(archive).isFile()) throw Error("Regular notice archive required"); verifyAsset(e, fs.readFileSync(archive));
      const listing = cp.execFileSync(tar, ["-tvf", archive, "--include", f.member], { encoding: "utf8", maxBuffer: 8192 }).trim().split("\n");
      if (listing.length !== 1 || !listing[0].startsWith("-")) throw Error("Regular license member required");
      raw = cp.execFileSync(tar, ["-xOf", archive, "--include", f.member], { maxBuffer: f.inner_member ? 8 * 1024 * 1024 : f.bytes + 1 });
      if (f.inner_member) {
        if (f.archive !== lock.windows.zlib?.source_archive || f.inner_member !== "zlib-1.3.2/LICENSE" || digest(raw) !== lock.windows.zlib.source_sha256) throw Error("Unknown nested source notice");
        const listing = cp.execFileSync(tar, ["-tvf", "-", "--include", f.inner_member], { input: raw, encoding: "utf8", maxBuffer: 8192 }).trim().split("\n");
        if (listing.length !== 1 || !listing[0].startsWith("-")) throw Error("Regular nested license required");
        raw = cp.execFileSync(tar, ["-xOf", "-", "--include", f.inner_member], { input: raw, maxBuffer: f.bytes + 1 });
      }
    }
    if (raw.length !== f.bytes || digest(raw) !== f.sha256) throw Error("License notice integrity failure");
    const target = path.join(outputRoot, f.path); fs.mkdirSync(path.dirname(target), { recursive: true }); fs.writeFileSync(target, raw, { flag: "wx" });
  }
  const recipes = [["windows-source-build.cjs", fs.readFileSync(__filename)], ...["windows-bundle.cjs", "assets.cjs", "windows-zlib.cjs", "zlib-regression.c"].map(n => [n, fs.readFileSync(path.join(__dirname, n))]), ["inputs.lock.json", Buffer.from(JSON.stringify(lock, null, 2) + "\n")]];
  for (const [name, raw] of recipes) { fs.writeFileSync(path.join(kitRoot, name), raw, { flag: "wx" }); files.push({ file: name, bytes: raw.length, sha256: digest(raw), kind: "local-build-recipe" }); }
  const result = { schema_version: 1, files, digest: digest(Buffer.from(JSON.stringify(files))), redistribution: lock.windows.redistribution || [] };
  const index = JSON.stringify(result, null, 2) + "\n";
  fs.writeFileSync(path.join(legalRoot, "source-kit-index.json"), index, { flag: "wx" }); fs.writeFileSync(path.join(kitRoot, "source-kit-index.json"), index, { flag: "wx" });
  return result;
}
async function buildMinimalWindowsRuntime({ lock, cacheRoot, outputRoot, msysRoot, installPinnedBuildPackages = false }) {
  if (process.platform !== "win32" || process.arch !== "x64") throw Error("Native Windows x64 source-build worker required; no cross/native acceptance claim");
  if (!path.isAbsolute(outputRoot) || fs.existsSync(outputRoot)) throw Error("Fresh absolute source-build output required");
  regularDirectory(path.dirname(outputRoot)); regularDirectory(msysRoot);
  verifyBootstrapRoot({ lock, msysRoot });
  if (lock?.windows?.mode !== "source-build") throw Error("Minimal source-build lock required");
  if (!installPinnedBuildPackages) throw Error("Explicit dedicated build-environment installation permission required");
  fs.mkdirSync(outputRoot, { mode: 0o700 });
  const inputs = path.join(outputRoot, "inputs");
  const names = new Set([...lock.windows.build_packages.map(n => lock.assets.find(e => e.package === n)?.file), ...lock.windows.source_archives]);
  if (names.has(undefined)) throw Error("Missing pinned compiler/dependency package");
  await prepareAssets({ output: inputs, local: cacheRoot, entries: lock.assets.filter(e => names.has(e.file)), platform: "windows/amd64" });
  const env = { SystemRoot: process.env.SystemRoot, WINDIR: process.env.WINDIR, TEMP: process.env.TEMP, TMP: process.env.TMP, MSYSTEM: "UCRT64", MINGW_PREFIX: "/ucrt64", MSYS2_PATH_TYPE: "strict", PATH: [path.join(msysRoot, "ucrt64", "bin"), path.join(msysRoot, "usr", "bin"), path.join(process.env.SystemRoot, "System32")].join(path.delimiter), LANG: "C", LC_ALL: "C", PYTHONNOUSERSITE: "1", PYTHONHASHSEED: "0", SOURCE_DATE_EPOCH: "1790718758" };
  const run = (program, args, cwd = outputRoot, options = {}) => cp.execFileSync(program, args, { cwd, env, encoding: "utf8", maxBuffer: 32 * 1024 * 1024, timeout: 30 * 60 * 1000, ...options });
  const bash = path.join(msysRoot, "usr", "bin", "bash.exe"), tar = path.join(msysRoot, "usr", "bin", "bsdtar.exe"), pacman = path.join(msysRoot, "usr", "bin", "pacman.exe");
  for (const file of [bash, tar, pacman]) if (!fs.lstatSync(file).isFile()) throw Error("Dedicated MSYS2 bootstrap is missing a regular build tool");
  const archives = lock.windows.build_packages.map(n => lock.assets.find(e => e.package === n));
  installBuildPackages({ lock, msysRoot, archives, inputs, run });
  for (const e of archives) { const actual = run(pacman, ["-Q", e.package]).trim(); if (actual !== e.package + " " + e.version) throw Error("Installed toolchain version mismatch: " + e.package); }
  const toolchainAudit = run(pacman, ["-Qkk", ...archives.map(e => e.package)]);
  fs.writeFileSync(path.join(outputRoot, "toolchain-file-audit.txt"), toolchainAudit, { flag: "wx" });
  const zlib = require("./windows-zlib.cjs").buildPatchedZlib({ lock, inputsRoot: inputs, outputRoot, msysRoot, run, tar });
  const sourceArchive = lock.assets.find(e => e.file === lock.windows.source_archive);
  verifyAsset(sourceArchive, fs.readFileSync(path.join(inputs, sourceArchive.file)));
  const sourceRaw = run(tar, ["-xOf", path.join(inputs, sourceArchive.file), "--include", lock.windows.source_member], outputRoot, { encoding: null, maxBuffer: 256 * 1024 * 1024 });
  if (digest(sourceRaw) !== lock.windows.source_sha256) throw Error("QEMU upstream source integrity failure");
  const sourceTar = path.join(outputRoot, "qemu-source.tar.xz"); fs.writeFileSync(sourceTar, sourceRaw, { flag: "wx" });
  const sourceRoot = extractQemuSource({ lock, sourceTar, outputRoot, run }), buildRoot = path.join(outputRoot, "build"), plan = minimalBuildPlan();
  fs.mkdirSync(buildRoot); fs.writeFileSync(path.join(sourceRoot, "configs", "devices", "x86_64-softmmu", "foundry.mak"), plan.devices, { flag: "wx" });
  const configureLog = run(bash, [path.join(sourceRoot, "configure").replaceAll("\\", "/"), ...plan.configure], buildRoot);
  fs.writeFileSync(path.join(outputRoot, "configure.log"), configureLog, { flag: "wx" });
  const options = JSON.parse(fs.readFileSync(path.join(buildRoot, "meson-info", "intro-buildoptions.json"))), devices = fs.readFileSync(path.join(buildRoot, "x86_64-softmmu-config-devices.mak"), "utf8"), host = fs.readFileSync(path.join(buildRoot, "config-host.h"), "utf8");
  auditConfiguration({ options, devices, host });
  fs.writeFileSync(path.join(outputRoot, "ninja.log"), run(path.join(msysRoot, "ucrt64", "bin", "ninja.exe"), ["-j2", ...plan.targets], buildRoot), { flag: "wx" });
  const runtime = path.join(outputRoot, "runtime"); fs.mkdirSync(runtime); const available = new Map();
  available.set("qemu-system-x86_64.exe", fs.readFileSync(path.join(buildRoot, "qemu-system-x86_64.exe")));
  for (const f of lock.windows.dependency_files) {
    if (path.basename(f.path).toLowerCase() === "zlib1.dll") throw Error("Prebuilt zlib runtime is forbidden");
    const archive = lock.assets.find(e => e.file === f.archive); if (!archive) throw Error("Dependency archive is not pinned");
    const raw = run(tar, ["-xOf", path.join(inputs, f.archive), "--include", f.member], outputRoot, { encoding: null, maxBuffer: f.bytes + 1 });
    if (raw.length !== f.bytes || digest(raw) !== f.sha256) throw Error("Dependency member integrity failure"); available.set(path.basename(f.path).toLowerCase(), raw);
  }
  require("./windows-zlib.cjs").addPatchedZlib(available, zlib, lock);
  const selected = new Map(), queue = ["qemu-system-x86_64.exe"];
  for (const name of queue) { if (selected.has(name)) continue; const raw = available.get(name); if (!raw) throw Error("Unresolved or forbidden minimal DLL: " + name); selected.set(name, raw); for (const dll of readPEImports(raw)) if (!isSystemImport(dll)) queue.push(dll); }
  const imports = validateClosure(selected); assertMinimalClosure(imports, lock);
  const files = []; for (const [name, raw] of selected) { fs.writeFileSync(path.join(runtime, name), raw, { flag: "wx" }); files.push({ path: "runtime/" + name, bytes: raw.length, sha256: digest(raw) }); }
  const firmware = path.join(runtime, "share", "qemu"); fs.mkdirSync(firmware, { recursive: true });
  for (const name of ["bios-256k.bin", "linuxboot_dma.bin"]) { const raw = fs.readFileSync(path.join(sourceRoot, "pc-bios", name)); fs.writeFileSync(path.join(firmware, name), raw, { flag: "wx" }); files.push({ path: "runtime/share/qemu/" + name, bytes: raw.length, sha256: digest(raw) }); }
  const sourceKit = assembleLegal({ lock, inputsRoot: inputs, sourceRoot, outputRoot, tar });
  const receipt = { schema_version: 1, kind: "source-built-minimal-qemu", version: lock.windows.qemu_version, source_sha256: lock.windows.source_sha256, source_kit: sourceKit, build_packages: archives, configure: plan.configure, devices, host, options, imports, files, zlib: zlib.receipt, native_acceptance: "not_performed" };
  fs.writeFileSync(path.join(outputRoot, "windows-runtime-build.json"), JSON.stringify(receipt, null, 2) + "\n", { flag: "wx" });
  fs.writeFileSync(path.join(outputRoot, "legal", "windows-runtime-build.json"), JSON.stringify(receipt, null, 2) + "\n", { flag: "wx" });
  return receipt;
}
// A freshly extracted portable MSYS2 has no trust database. Use only its pinned
// offline keyring, not the login-shell post-install hook (which refreshes keys).
function installBuildPackages({ lock, msysRoot, archives, inputs, run }) {
  regularDirectory(msysRoot);
  const required = new Set(["usr/bin/pacman-key", "usr/bin/gpg.exe", "usr/bin/gpgconf.exe", "usr/share/pacman/keyrings/msys2.gpg", "usr/share/pacman/keyrings/msys2-trusted", "usr/share/pacman/keyrings/msys2-revoked", "etc/pacman.conf"]);
  const files = lock.windows.bootstrap.keyring;
  if (!Array.isArray(files) || files.length !== required.size) throw Error("Pinned bootstrap keyring required");
  for (const entry of files) {
    if (!required.delete(entry.path)) throw Error("Unknown or duplicate bootstrap keyring path");
    const file = path.join(msysRoot, entry.path); regularDirectory(path.dirname(file));
    if (!fs.lstatSync(file).isFile()) throw Error("Regular bootstrap keyring file required");
    const raw = fs.readFileSync(file);
    if (raw.length !== entry.bytes || digest(raw) !== entry.sha256) throw Error("Bootstrap keyring integrity failure");
  }
  const bash = path.join(msysRoot, "usr/bin/bash.exe");
  const keyArgs = ["--noprofile", "--norc", "/usr/bin/pacman-key", "--gpgdir", "/etc/pacman.d/gnupg"];
  run(bash, [...keyArgs, "--init"]);
  run(bash, [...keyArgs, "--populate", "msys2"]);
  run(path.join(msysRoot, "usr/bin/gpgconf.exe"), ["--homedir", "/etc/pacman.d/gnupg", "--kill", "all"]);
  run(path.join(msysRoot, "usr/bin/pacman.exe"), ["-U", "--noconfirm", ...archives.map(e => path.join(inputs, e.file))]); // local archives only; no -S/-y, refresh or retry.
}
// MSYS2's default symlink deep-copy cannot extract forward/dangling links.
// Native tar preserves links. Native Windows also needs a directory-link type,
// which POSIX tar does not encode; derive it only from validated directory members.
function extractQemuSource({ lock, sourceTar, outputRoot, run = (program, args) => cp.execFileSync(program, args, { encoding: "utf8", maxBuffer: 32 * 1024 * 1024, timeout: 120000 }) }) {
  regularDirectory(outputRoot);
  if (!/^\d+\.\d+\.\d+$/.test(lock.windows.qemu_version)) throw Error("Unsafe QEMU source version");
  const prefix = "qemu-" + lock.windows.qemu_version + "/", sourceRoot = path.join(outputRoot, prefix.slice(0, -1));
  if (fs.lstatSync(sourceRoot, { throwIfNoEntry: false })) throw Error("Fresh QEMU source destination required");
  if (!path.isAbsolute(sourceTar) || !fs.lstatSync(sourceTar).isFile() || digest(fs.readFileSync(sourceTar)) !== lock.windows.source_sha256) throw Error("QEMU upstream source integrity failure");
  const tar = process.platform === "win32" ? path.join(process.env.SystemRoot, "System32", "tar.exe") : "/usr/bin/tar";
  const members = run(tar, ["-tf", sourceTar]).trim().split(/\r?\n/);
  if (members.some(n => !n.startsWith(prefix) || n.split("/").includes("..") || /[\\\x00-\x1f]/.test(n))) throw Error("Unsafe QEMU source path");
  run(tar, ["-xf", sourceTar, "-C", outputRoot, "--no-same-owner", "--no-same-permissions"]);
  regularDirectory(sourceRoot);
  if (process.platform === "win32") {
    const directories = new Set(members.filter(n => n.endsWith("/")).map(n => n.slice(0, -1)));
    const visit = directory => {
      for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
        const file = path.join(directory, entry.name);
        if (entry.isSymbolicLink()) {
          const target = fs.readlinkSync(file), member = path.relative(outputRoot, file).replaceAll("\\", "/");
          const resolved = path.posix.normalize(path.posix.join(path.posix.dirname(member), target.replaceAll("\\", "/")));
          if (!path.win32.isAbsolute(target) && directories.has(resolved)) {
            fs.unlinkSync(file); fs.symlinkSync(target, file, "dir");
          }
        } else if (entry.isDirectory()) visit(file);
      }
    };
    visit(sourceRoot); // never follow archive links or inspect their external targets.
  }
  return sourceRoot;
}
module.exports = { minimalBuildPlan, auditConfiguration, assertMinimalClosure, assembleLegal, verifyBootstrapRoot, bootstrapWindowsBuildHost, buildMinimalWindowsRuntime, installBuildPackages, extractQemuSource };
if (require.main === module) {
  const lock = require("./inputs.lock.json"), cacheRoot = process.env.FOUNDRY_SANDBOX_INPUTS;
  const action = process.argv[2] === "--bootstrap" ? bootstrapWindowsBuildHost({ lock, outputRoot: process.argv[3] || "", cacheRoot }) : buildMinimalWindowsRuntime({ lock, outputRoot: process.argv[2] || "", msysRoot: process.argv[3] || "", cacheRoot, installPinnedBuildPackages: process.argv[4] === "--install-pinned-build-packages" });
  action.then(r => console.log(JSON.stringify(r.msysRoot ? r : { runtime_files: r.files.length, native_acceptance: r.native_acceptance }))).catch(e => { console.error(e.message); process.exitCode = 1; });
}
