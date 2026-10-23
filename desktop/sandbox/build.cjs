"use strict";
// Build-only assembler. Installed Foundry never downloads or invokes a host
// compiler: it receives the verified bundle built here as immutable resources.
const fs = require("node:fs"), path = require("node:path"), crypto = require("node:crypto"), cp = require("node:child_process");
const { prepareAssets } = require("./assets.cjs");
const { arm64Kernel } = require("./kernel.cjs");
const { buildInitramfs, readTar } = require("./build-guest.cjs");
const zlib = require("node:zlib");
const sha256 = b => crypto.createHash("sha256").update(b).digest("hex");
function sourceInventory(root) {
  if (!path.isAbsolute(root) || !fs.lstatSync(root).isDirectory() || fs.lstatSync(root).isSymbolicLink()) throw Error("Regular absolute inventory root required");
  const files = [];
  function visit(dir) {
    for (const name of fs.readdirSync(path.join(root, dir)).sort()) {
      if (!name || /[\\:\x00-\x1f]/.test(name) || name === "." || name === "..") throw Error("Unsafe inventory path");
      const relative = dir ? dir + "/" + name : name, file = path.join(root, relative), s = fs.lstatSync(file);
      if (s.isSymbolicLink()) throw Error("Non-regular inventory item");
      if (s.isDirectory()) visit(relative);
      else if (s.isFile() && s.size > 0 && s.size <= 512 * 1024 * 1024) files.push({ path: relative, sha256: sha256(fs.readFileSync(file)), size: s.size });
      else throw Error("Non-regular or oversized inventory item");
    }
  }
  visit(""); return files;
}
function writeVMManifest(root, host) {
  if (!["darwin/arm64", "windows/amd64"].includes(host)) throw Error("Unsupported sandbox platform");
  if (fs.existsSync(path.join(root, "manifest.json"))) throw Error("Refusing existing manifest");
  const runtime = host === "darwin/arm64" ? "runtime/foundry-vm-helper" : "runtime/qemu-system-x86_64.exe";
  const assets = sourceInventory(root), names = new Set(assets.map(e => e.path));
  for (const name of [runtime, "kernel", "initramfs.cpio.gz", "foundry-vm-guest", "legal/NOTICE.md", "legal/GPL-2.0.txt"]) if (!names.has(name)) throw Error("Missing required bundle item: " + name);
  const manifest = { schema_version: 1, host_platform: host, guest_platform: host === "darwin/arm64" ? "linux/arm64" : "linux/amd64", protocol: "foundry-vm-v1", policy_version: "go-vm-v1", runtime, kernel: "kernel", rootfs: "initramfs.cpio.gz", verifier: "foundry-vm-guest", assets };
  fs.writeFileSync(path.join(root, "manifest.json"), JSON.stringify(manifest, null, 2) + "\n", { flag: "wx", mode: 0o644 }); return manifest;
}
function verifyBuiltWindowsRuntime(root, proof, lock) {
  const inventory = sourceInventory(root), byPath = new Map();
  if (!Array.isArray(proof.files) || !Array.isArray(proof.imports) || proof.files.length !== inventory.length) throw Error("Runtime inventory mismatch");
  for (const e of proof.files) {
    if (byPath.has(e.path)) throw Error("Duplicate runtime inventory"); byPath.set(e.path, e);
  }
  const pe = new Map();
  for (const e of inventory) {
    const expected = byPath.get("runtime/" + e.path);
    if (!expected || expected.bytes !== e.size || expected.sha256 !== e.sha256) throw Error("Built runtime integrity mismatch");
    if (/^[a-z0-9][a-z0-9._+-]*\.(?:exe|dll)$/.test(e.path)) pe.set(e.path, fs.readFileSync(path.join(root, e.path)));
    else if (!["share/qemu/bios-256k.bin", "share/qemu/linuxboot_dma.bin"].includes(e.path)) throw Error("Unexpected runtime file");
  }
  for (const name of ["runtime/qemu-system-x86_64.exe", "runtime/share/qemu/bios-256k.bin", "runtime/share/qemu/linuxboot_dma.bin"]) if (!byPath.has(name)) throw Error("Required runtime file missing");
  const imports = require("./windows-bundle.cjs").validateClosure(pe);
  require("./windows-source-build.cjs").assertMinimalClosure(imports, lock);
  const canonical = entries => JSON.stringify(entries.map(e => ({ file: e.file, imports: [...e.imports].sort() })).sort((a, b) => a.file.localeCompare(b.file)));
  if (canonical(imports) !== canonical(proof.imports)) throw Error("Runtime imports disagree with actual PE files");
  if (pe.has("zlib1.dll")) require("./windows-zlib.cjs").verifyZlibArtifact(pe.get("zlib1.dll"), proof.zlib, lock);
  return imports;
}
function verifyWindowsRedistribution(root, proof, lock) {
  if (!path.isAbsolute(root) || fs.realpathSync(root) !== root || !fs.lstatSync(root).isDirectory()) throw Error("Regular canonical redistribution root required");
  if (!lock.windows.source_archives?.length || !lock.windows.notices?.length) throw Error("Pinned redistribution inventory required");
  // Reconstruct expectations from this checkout and its lock, never from the
  // supplied receipt: a modified source plus a newly hashed receipt is not proof.
  const files = lock.windows.source_archives.map(name => {
    const matches = lock.assets.filter(e => e.file === name);
    if (matches.length !== 1 || matches[0].kind !== "source") throw Error("Pinned redistribution source required");
    const e = matches[0]; require("./assets.cjs").validateAsset(e);
    return { file: e.file, bytes: e.bytes, sha256: e.sha256, url: e.url, license: e.license };
  });
  for (const name of ["windows-source-build.cjs", "windows-bundle.cjs", "assets.cjs", "windows-zlib.cjs", "zlib-regression.c", "inputs.lock.json"]) {
    const raw = name === "inputs.lock.json" ? Buffer.from(JSON.stringify(lock, null, 2) + "\n") : fs.readFileSync(path.join(__dirname, name));
    files.push({ file: name, bytes: raw.length, sha256: sha256(raw), kind: "local-build-recipe" });
  }
  const index = { schema_version: 1, files, digest: sha256(Buffer.from(JSON.stringify(files))), redistribution: lock.windows.redistribution || [] };
  if (!require("node:util").isDeepStrictEqual(proof.source_kit, index)) throw Error("Redistribution source-kit proof disagrees with pinned inventory");
  const jsonBytes = value => Buffer.from(JSON.stringify(value, null, 2) + "\n");
  const metadata = (name, raw) => ({ path: name, size: raw.length, sha256: sha256(raw) });
  const proofBytes = jsonBytes(proof), indexBytes = jsonBytes(index);
  const sourceFiles = [...files.map(e => ({ path: e.file, size: e.bytes, sha256: e.sha256 })), metadata("source-kit-index.json", indexBytes)];
  const legalFiles = lock.windows.notices.map(e => {
    if (!e.path.startsWith("legal/")) throw Error("Invalid redistribution notice path");
    return { path: e.path.slice(6), size: e.bytes, sha256: e.sha256 };
  });
  legalFiles.push(metadata("source-kit-index.json", indexBytes), metadata("windows-runtime-build.json", proofBytes));
  function verifyTree(directory, expected) {
    const byPath = new Map(), directories = new Set();
    for (const e of expected) {
      if (!/^[a-zA-Z0-9._+~/-]+$/.test(e.path) || e.path.startsWith("/") || e.path.split("/").some(p => !p || p === "." || p === "..") || byPath.has(e.path) || !Number.isSafeInteger(e.size) || e.size < 1 || !/^[a-f0-9]{64}$/.test(e.sha256)) throw Error("Invalid redistribution inventory");
      byPath.set(e.path, e);
      for (let dir = path.posix.dirname(e.path); dir !== "."; dir = path.posix.dirname(dir)) directories.add(dir);
    }
    const actual = sourceInventory(directory);
    if (actual.length !== byPath.size) throw Error("Redistribution inventory mismatch");
    for (const e of actual) {
      const wanted = byPath.get(e.path);
      if (!wanted || e.size !== wanted.size || e.sha256 !== wanted.sha256) throw Error("Redistribution file integrity mismatch: " + e.path);
    }
    function checkDirectories(relative = "") {
      for (const name of fs.readdirSync(path.join(directory, relative))) {
        const next = relative ? relative + "/" + name : name;
        if (fs.lstatSync(path.join(directory, next)).isDirectory()) {
          if (!directories.has(next)) throw Error("Unexpected redistribution directory");
          checkDirectories(next);
        }
      }
    }
    checkDirectories();
  }
  verifyTree(path.join(root, "source-kit"), sourceFiles);
  verifyTree(path.join(root, "legal"), legalFiles);
  const proofFile = path.join(root, "windows-runtime-build.json");
  if (!fs.lstatSync(proofFile).isFile() || !fs.readFileSync(proofFile).equals(proofBytes)) throw Error("Redistribution runtime proof mismatch");
  return true;
}
function extractKernelRecipe(archive, output, tar) {
  const prefix = "aports-e9951ece7106a5c898b0812cfa7b47d56379b361/main/linux-lts/";
  const run = args => cp.execFileSync(tar, args, { timeout: 30000, maxBuffer: 4 * 1024 * 1024 });
  // Native Windows tar emits CRLF text records; member content remains binary.
  const names = run(["-tf", archive]).toString().trim().split(/\r?\n/).filter(n => n.startsWith(prefix) && n !== prefix);
  if (!names.length || names.length > 128 || new Set(names).size !== names.length || names.some(n => !/^[a-zA-Z0-9][a-zA-Z0-9._+-]*$/.test(n.slice(prefix.length)))) throw Error("Invalid kernel recipe inventory");
  for (const name of ["APKBUILD", "virt.aarch64.config", "virt.x86_64.config"]) if (!names.includes(prefix + name)) throw Error("Missing kernel build recipe");
  fs.mkdirSync(output);
  for (const name of names) {
    const listing = run(["-tvf", archive, name]).toString().trim().split(/\r?\n/);
    if (listing.length !== 1 || !listing[0].startsWith("-")) throw Error("Regular kernel recipe member required");
    const bytes = run(["-xOf", archive, name]);
    if (!bytes.length) throw Error("Empty kernel recipe member");
    fs.writeFileSync(path.join(output, name.slice(prefix.length)), bytes, { flag: "wx" });
  }
}
const commonFiles = ["go1.27.3.linux-arm64.tar.gz", "go1.27.3.linux-amd64.tar.gz", "vmlinuz-virt-aarch64-3.24.3", "vmlinuz-virt-x86_64-3.24.3", "linux-6.18.tar.xz", "patch-6.18.58.xz", "aports-e9951ece7106a5c898b0812cfa7b47d56379b361.tar.gz"];
async function buildBundle({ output, local, windowsRuntime, go = process.env.FOUNDRY_GO || "go" }) {
  const host = process.platform === "win32" && process.arch === "x64" ? "windows/amd64" : process.platform + "/" + process.arch;
  if (!["darwin/arm64", "windows/amd64"].includes(host)) throw Error("Native macOS arm64 or Windows x64 builder required");
  const source = path.resolve(__dirname, "../.."), lock = require("./inputs.lock.json");
  require("../build-support.cjs").validateOutput(output, source);
  if (fs.realpathSync(path.dirname(output)) !== path.dirname(output)) throw Error("Canonical build parent required");
  fs.mkdirSync(output, { mode: 0o700 });
  const input = path.join(output, "inputs");
  const receipt = await prepareAssets({ output: input, local, entries: lock.assets.filter(e => commonFiles.includes(e.file)), platform: host });
  const bundle = path.join(output, "bundle"), legal = path.join(bundle, "legal"), sourceKit = path.join(output, "sandbox-sources");
  fs.mkdirSync(legal, { recursive: true }); fs.mkdirSync(sourceKit);
  const arch = host === "darwin/arm64" ? "arm64" : "amd64";
  const env = { ...process.env, GOTOOLCHAIN: "local", GOENV: "off", GOPROXY: "off", GOSUMDB: "off", GOTELEMETRY: "off", CGO_ENABLED: "0", GOOS: "linux", GOARCH: arch };
  const run = (program, args, opts = {}) => cp.execFileSync(program, args, { cwd: source, env, timeout: 180000, maxBuffer: 1024 * 1024, ...opts });
  if (!run(go, ["version"], { encoding: "utf8" }).startsWith("go version go1.27.3 ")) throw Error("Pinned Go 1.27.3 required");
  const guest = path.join(bundle, "foundry-vm-guest");
  run(go, ["build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", guest, "./cmd/foundry-vm-guest"]);
  const goRaw = fs.readFileSync(path.join(input, "go1.27.3.linux-" + arch + ".tar.gz"));
  fs.writeFileSync(path.join(bundle, "initramfs.cpio.gz"), buildInitramfs({ guest: fs.readFileSync(guest), goArchive: goRaw, arch }), { flag: "wx" });
  const kernelRaw = fs.readFileSync(path.join(input, "vmlinuz-virt-" + (arch === "arm64" ? "aarch64" : "x86_64") + "-3.24.3"));
  const kernel = arch === "arm64" ? arm64Kernel(kernelRaw) : kernelRaw;
  if (arch === "arm64" && sha256(kernel) !== "256d5fff4bb892edcb9218c1e6353e388bc3646e65f76c2bc9d958b635d1b00e") throw Error("Converted kernel digest mismatch");
  fs.writeFileSync(path.join(bundle, "kernel"), kernel, { flag: "wx" });
  if (host === "darwin/arm64") {
    fs.mkdirSync(path.join(bundle, "runtime")); const helper = path.join(bundle, "runtime/foundry-vm-helper");
    run("/usr/bin/xcrun", ["swiftc", "-target", "arm64-apple-macos12.0", "-framework", "Virtualization", path.join(__dirname, "macos/main.swift"), "-o", helper]);
    run("/usr/bin/codesign", ["--force", "--sign", "-", "--timestamp=none", "--entitlements", path.join(__dirname, "macos/entitlements.plist"), helper]);
    run("/usr/bin/codesign", ["--verify", "--strict", helper]);
  } else {
    if (!windowsRuntime || !path.isAbsolute(windowsRuntime)) throw Error("Explicit source-built Windows runtime required");
    const proof = JSON.parse(fs.readFileSync(path.join(windowsRuntime, "windows-runtime-build.json")));
    if (proof.kind !== "source-built-minimal-qemu" || proof.source_sha256 !== lock.windows.source_sha256) throw Error("Minimal source-build proof required");
    require("./windows-source-build.cjs").auditConfiguration({ options: proof.options, devices: proof.devices, host: proof.host, target: proof.target });
    verifyBuiltWindowsRuntime(path.join(windowsRuntime, "runtime"), proof, lock);
    verifyWindowsRedistribution(windowsRuntime, proof, lock);
    fs.cpSync(path.join(windowsRuntime, "runtime"), path.join(bundle, "runtime"), { recursive: true });
    fs.cpSync(path.join(windowsRuntime, "legal"), path.join(legal, "qemu"), { recursive: true });
    fs.cpSync(path.join(windowsRuntime, "source-kit"), path.join(sourceKit, "qemu"), { recursive: true });
    fs.copyFileSync(path.join(windowsRuntime, "windows-runtime-build.json"), path.join(legal, "qemu-build.json"));
  }
  const tar = host === "darwin/arm64" ? "/usr/bin/tar" : path.join(process.env.SystemRoot, "System32", "tar.exe");
  const gpl = run(tar, ["-xOf", path.join(input, "linux-6.18.tar.xz"), "linux-6.18/LICENSES/preferred/GPL-2.0"]);
  fs.writeFileSync(path.join(legal, "GPL-2.0.txt"), gpl, { flag: "wx" });
  const goMembers = readTar(zlib.gunzipSync(goRaw, { maxOutputLength: 768 * 1024 * 1024 }));
  for (const name of ["LICENSE", "PATENTS"]) fs.writeFileSync(path.join(legal, "Go-" + name), goMembers.get("go/" + name).data, { flag: "wx" });
  for (const e of receipt.assets.filter(e => e.kind === "source")) fs.copyFileSync(path.join(input, e.file), path.join(sourceKit, e.file), fs.constants.COPYFILE_EXCL);
  extractKernelRecipe(path.join(input, "aports-e9951ece7106a5c898b0812cfa7b47d56379b361.tar.gz"), path.join(sourceKit, "kernel-recipe"), tar);
  fs.writeFileSync(path.join(sourceKit, "inputs.lock.json"), JSON.stringify(lock, null, 2) + "\n", { flag: "wx" });
  const recipes = path.join(sourceKit, "foundry-recipes"); fs.mkdirSync(recipes);
  for (const name of ["build.cjs", "build-guest.cjs", "kernel.cjs", "assets.cjs"]) fs.copyFileSync(path.join(__dirname, name), path.join(recipes, name));
  fs.writeFileSync(path.join(sourceKit, "README.md"), "# Foundry sandbox corresponding sources\n\nLinux 6.18 plus patch-6.18.58 and Alpine aports commit e9951ece7106a5c898b0812cfa7b47d56379b361 reproduce the shipped linux-virt kernel. kernel-recipe/ is that commit's unmodified main/linux-lts subtree, including APKBUILD, configurations and distribution patches; unrelated Alpine package recipes are not redistributed. Foundry's VM guest and bundle recipes are Apache-2.0 in the matching Foundry source commit; foundry-recipes/ records the exact assembler inputs, used within that checkout. Windows QEMU sources and exact minimal-build recipes are in qemu/ when present. This source sidecar must accompany redistribution of the installer; URLs alone are not a replacement.\n", { flag: "wx" });
  const sources = sourceInventory(sourceKit);
  fs.writeFileSync(path.join(legal, "sources.json"), JSON.stringify({ schema_version: 1, sidecar: "sandbox-sources", files: sources }, null, 2) + "\n", { flag: "wx" });
  fs.writeFileSync(path.join(legal, "NOTICE.md"), "# Bundled sandbox components\n\nFoundry guest and macOS helper: Apache-2.0. Linux 6.18.58-0-virt from Alpine 3.24.3: GPL-2.0-only (see GPL-2.0.txt). Go 1.27.3 guest toolchain: BSD-3-Clause (see Go-LICENSE and Go-PATENTS). Windows minimal QEMU and linked runtime notices, when present: qemu/. Corresponding-source sidecar: sandbox-sources, file hashes in sources.json. The guest has no network interface, host share, or account credentials.\n", { flag: "wx" });
  const manifest = writeVMManifest(bundle, host);
  fs.writeFileSync(path.join(output, "sandbox-build.json"), JSON.stringify({ schema_version: 1, host, inputs: receipt.assets, kernel_sha256: sha256(kernel), manifest_sha256: sha256(fs.readFileSync(path.join(bundle, "manifest.json"))), native_acceptance: "not_performed" }, null, 2) + "\n", { flag: "wx" });
  return { bundle, sourceKit, manifest };
}
module.exports = { sourceInventory, writeVMManifest, verifyBuiltWindowsRuntime, verifyWindowsRedistribution, extractKernelRecipe, buildBundle };
if (require.main === module) buildBundle({ output: process.argv[2] || "", local: process.env.FOUNDRY_SANDBOX_INPUTS, windowsRuntime: process.env.FOUNDRY_WINDOWS_VM_RUNTIME }).then(r => console.log(JSON.stringify({ bundle: r.bundle, sourceKit: r.sourceKit, assets: r.manifest.assets.length }))).catch(e => { console.error(e.message); process.exitCode = 1; });
