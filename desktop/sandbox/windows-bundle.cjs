"use strict";
// Build-time assembly only. No Windows binary is executed, no global MSYS2 or
// ambient DLL/PATH directory participates in resolving the dependency graph.
const fs = require("node:fs"), path = require("node:path"), crypto = require("node:crypto"), cp = require("node:child_process");
const { validateAsset, verifyAsset, fetchAsset } = require("./assets.cjs");
const SYSTEM_DLLS = new Set(("advapi32 authz avrt bcrypt cfgmgr32 comctl32 comdlg32 crypt32 cryptbase d2d1 d3d11 d3d9 d3dcompiler_47 dcomp dbghelp dnsapi dsound dwmapi dwrite dxgi gdi32 gdiplus glu32 hid imm32 iphlpapi kernel32 ksuser mf mfplat mfreadwrite mfuuid mpr msimg32 msvcrt ncrypt netapi32 normaliz ntdll ole32 oleaut32 opengl32 powrprof propsys psapi rpcrt4 secur32 setupapi shell32 shlwapi ucrtbase user32 userenv usp10 uxtheme version windowscodecs winhttp winhvplatform wininet winmm winspool wldap32 ws2_32 wtsapi32").split(" ").map(n => n + ".dll"));
function readPEImports(raw) {
  const fail = message => { throw Error("Invalid AMD64 PE: " + message); };
  const bound = (at, size) => { if (!Number.isSafeInteger(at) || at < 0 || at + size > raw.length) fail("truncated structure"); };
  bound(0, 64); if (raw.toString("ascii", 0, 2) !== "MZ") fail("signature");
  const header = raw.readUInt32LE(60); bound(header, 24);
  if (raw.toString("ascii", header, header + 4) !== "PE\0\0" || raw.readUInt16LE(header + 4) !== 0x8664) fail("AMD64 machine required");
  const count = raw.readUInt16LE(header + 6), optionalSize = raw.readUInt16LE(header + 20), opt = header + 24;
  bound(opt, optionalSize); if (optionalSize < 240 || raw.readUInt16LE(opt) !== 0x20b || raw.readUInt32LE(opt + 108) < 14 || count < 1 || count > 96) fail("optional header");
  const sections = [];
  for (let i = 0; i < count; i++) { const at = opt + optionalSize + i * 40; bound(at, 40); sections.push({ rva: raw.readUInt32LE(at + 12), size: raw.readUInt32LE(at + 16), offset: raw.readUInt32LE(at + 20) }); }
  const offset = (rva, size = 1) => {
    const matches = sections.filter(s => rva >= s.rva && rva + size <= s.rva + s.size);
    if (matches.length !== 1) fail("unmapped or ambiguous RVA");
    const at = matches[0].offset + rva - matches[0].rva; bound(at, size); return at;
  };
  const imports = new Set();
  for (const [index, stride, nameField] of [[1, 20, 12], [13, 32, 4]]) {
    const rva = raw.readUInt32LE(opt + 112 + index * 8), size = raw.readUInt32LE(opt + 116 + index * 8);
    if (!rva && !size) continue;
    if (!rva || size < stride || size > 1024 * 1024) fail("import directory");
    let terminated = false;
    for (let step = 0; step + stride <= size; step += stride) {
      const at = offset(rva + step, stride);
      if (raw.subarray(at, at + stride).every(n => n === 0)) { terminated = true; break; }
      if (index === 13 && raw.readUInt32LE(at) !== 1) fail("unsupported delay import address mode");
      const nameRva = raw.readUInt32LE(at + nameField); let name = "", nul = false;
      for (let j = 0; j < 160; j++) { const c = raw[offset(nameRva + j)]; if (!c) { nul = true; break; } if (c > 127) fail("non-ASCII import"); name += String.fromCharCode(c); }
      if (!nul || !/^[a-zA-Z0-9][a-zA-Z0-9._+-]*\.(?:dll|drv)$/i.test(name)) fail("unsafe import name");
      imports.add(name.toLowerCase());
    }
    if (!terminated) fail("unterminated import directory");
  }
  return [...imports].sort();
}
SYSTEM_DLLS.add("winspool.drv");
SYSTEM_DLLS.add("mswsock.dll");
// Exact Windows 10+ contracts observed in the pinned closure; no api-* wildcard.
for (const name of ["api-ms-win-core-path-l1-1-0", "api-ms-win-core-synch-l1-2-0", ...["conio", "convert", "environment", "filesystem", "heap", "locale", "math", "multibyte", "private", "process", "runtime", "stdio", "string", "time", "utility"].map(n => "api-ms-win-crt-" + n + "-l1-1-0")]) SYSTEM_DLLS.add(name + ".dll");
function isSystemImport(name) { return SYSTEM_DLLS.has(name.toLowerCase()); }
function validateClosure(files) {
  const normalized = new Map();
  for (const [name, bytes] of files) {
    const lower = name.toLowerCase();
    if (!/^[a-z0-9][a-z0-9._+-]*\.(exe|dll)$/.test(lower) || normalized.has(lower) || SYSTEM_DLLS.has(lower)) throw Error("Invalid or duplicate runtime DLL name");
    normalized.set(lower, bytes);
  }
  const seen = new Set(), queue = ["qemu-system-x86_64.exe"], result = [];
  for (const name of queue) {
    if (seen.has(name)) continue; seen.add(name);
    const bytes = normalized.get(name); if (!bytes) throw Error("Unresolved runtime import: " + name);
    const imports = readPEImports(bytes); result.push({ file: name, imports });
    for (const imported of imports) if (!SYSTEM_DLLS.has(imported)) queue.push(imported);
  }
  if (seen.size !== normalized.size) throw Error("Unreachable runtime DLL in bundle");
  return result;
}
function safeRelative(name) { return typeof name === "string" && name.length < 240 && name.split("/").every(p => /^[a-zA-Z0-9][a-zA-Z0-9._+-]*$/.test(p) && !p.endsWith(".") && !/^(con|prn|aux|nul|com[0-9]|lpt[0-9])(?:\.|$)/i.test(p)); }
function directory(name) {
  if (!path.isAbsolute(name)) throw Error("Absolute regular directory required");
  let p = path.parse(name).root;
  for (const part of path.relative(p, name).split(path.sep).filter(Boolean)) { p = path.join(p, part); if (!fs.lstatSync(p).isDirectory()) throw Error("Regular non-symlink directory required"); }
}
async function acquireWindowsRuntime({ lock, cacheRoot, outputRoot, fetchImpl }) {
  if (!path.isAbsolute(outputRoot) || fs.existsSync(outputRoot)) throw Error("Fresh absolute Windows output required");
  directory(path.dirname(outputRoot)); if (cacheRoot) directory(cacheRoot);
  if (lock?.schema_version !== 1 || !Array.isArray(lock.assets) || !Array.isArray(lock.windows?.files) || !lock.windows.files.length || lock.windows.files.length > 160 || !Array.isArray(lock.windows.source_archives) || !lock.windows.source_archives.length) throw Error("Windows runtime and corresponding source inventory required");
  const byName = new Map();
  for (const e of lock.assets) { validateAsset(e); if (byName.has(e.file.toLowerCase())) throw Error("Duplicate input asset"); byName.set(e.file.toLowerCase(), e); }
  if (!Array.isArray(lock.windows.notices) || !lock.windows.notices.length || !Array.isArray(lock.windows.redistribution)) throw Error("License notice and source mapping required");
  const selected = new Set(lock.windows.files.map(e => e.archive));
  for (const name of lock.windows.source_archives) { const e = byName.get(name.toLowerCase()); if (!e || !["source", "recipe", "notice"].includes(e.kind)) throw Error("Missing corresponding source metadata"); selected.add(name); }
  const names = new Set();
  for (const f of lock.windows.files) {
    if (!safeRelative(f.member) || !safeRelative(f.path) || !/^runtime\/(?:[^/]+\.(?:dll|exe)|share\/qemu\/(?:bios-256k\.bin|linuxboot_dma\.bin))$/i.test(f.path) || names.has(f.path.toLowerCase()) || !Number.isSafeInteger(f.bytes) || f.bytes < 1 || f.bytes > 256 * 1024 * 1024 || !/^[a-f0-9]{64}$/.test(f.sha256)) throw Error("Invalid or duplicate runtime member");
    const e = byName.get(f.archive?.toLowerCase()); if (!e || !["runtime", "dependency"].includes(e.kind) || !e.platforms.includes("windows/amd64")) throw Error("Unknown runtime archive"); names.add(f.path.toLowerCase());
  }
  for (const f of lock.windows.notices) {
    if (!safeRelative(f.member) || (f.inner_member && !safeRelative(f.inner_member)) || !safeRelative(f.path) || !f.path.startsWith("legal/licenses/") || names.has(f.path.toLowerCase()) || !Number.isSafeInteger(f.bytes) || f.bytes < 1 || f.bytes > 4 * 1024 * 1024 || !/^[a-f0-9]{64}$/.test(f.sha256) || !byName.has(f.archive?.toLowerCase())) throw Error("Invalid license notice member");
    names.add(f.path.toLowerCase()); selected.add(f.archive);
  }
  for (const archive of new Set(lock.windows.files.map(f => f.archive))) {
    const entries = lock.windows.redistribution.filter(e => e.archive === archive);
    if (entries.length !== 1) throw Error("Missing corresponding source/notice mapping");
    const e = entries[0];
    if (!Array.isArray(e.notices) || !e.notices.length || e.notices.some(n => !lock.windows.notices.some(f => f.path === n))) throw Error("Missing license notice for runtime");
    if (e.source_archive ? !lock.windows.source_archives.includes(e.source_archive) : typeof e.source_not_required_reason !== "string" || e.source_not_required_reason.length < 20) throw Error("Missing corresponding source for runtime");
  }
  // Output is fresh and private; any failure leaves evidence in place, with no retry.
  fs.mkdirSync(outputRoot, { mode: 0o700 }); const inputRoot = path.join(outputRoot, "inputs"); fs.mkdirSync(inputRoot, { mode: 0o700 });
  for (const name of selected) {
    const e = byName.get(name.toLowerCase()); if (!e) throw Error("Missing input metadata"); let raw;
    if (cacheRoot) { const file = path.join(cacheRoot, e.file), stat = fs.lstatSync(file); if (!stat.isFile() || stat.size !== e.bytes) throw Error("Regular verified input required"); raw = fs.readFileSync(file); }
    else raw = await fetchAsset(e, fetchImpl);
    verifyAsset(e, raw); fs.writeFileSync(path.join(inputRoot, e.file), raw, { flag: "wx", mode: 0o600 });
  }
  const peFiles = new Map(), extracted = [];
  for (const f of [...lock.windows.files, ...lock.windows.notices]) {
    const archive = path.join(inputRoot, f.archive);
    const listing = cp.execFileSync("tar", ["-tvf", archive, "--include", f.member], { encoding: "utf8", maxBuffer: 8192, timeout: 30000 }).trim().split("\n");
    if (listing.length !== 1 || !listing[0].startsWith("-")) throw Error("Regular archive member required");
    let raw = cp.execFileSync("tar", ["-xOf", archive, "--include", f.member], { maxBuffer: f.inner_member ? 512 * 1024 * 1024 : f.bytes + 1, timeout: 30000 });
    if (f.inner_member) {
      const innerListing = cp.execFileSync("tar", ["-tvf", "-", "--include", f.inner_member], { input: raw, encoding: "utf8", maxBuffer: 8192, timeout: 30000 }).trim().split("\n");
      if (innerListing.length !== 1 || !innerListing[0].startsWith("-")) throw Error("Regular source notice member required");
      raw = cp.execFileSync("tar", ["-xOf", "-", "--include", f.inner_member], { input: raw, maxBuffer: f.bytes + 1, timeout: 30000 });
    }
    if (raw.length !== f.bytes || crypto.createHash("sha256").update(raw).digest("hex") !== f.sha256) throw Error("Runtime member integrity failure");
    extracted.push({ f, raw }); if (/\.(exe|dll)$/i.test(f.path)) peFiles.set(path.basename(f.path), raw);
  }
  const imports = validateClosure(peFiles);
  for (const { f, raw } of extracted) { const target = path.join(outputRoot, f.path); fs.mkdirSync(path.dirname(target), { recursive: true, mode: 0o700 }); fs.writeFileSync(target, raw, { flag: "wx", mode: 0o644 }); }
  const legal = path.join(outputRoot, "legal"); fs.mkdirSync(legal, { recursive: true, mode: 0o700 });
  const sourceKit = path.join(outputRoot, "source-kit"); fs.mkdirSync(sourceKit, { mode: 0o700 });
  for (const name of lock.windows.source_archives) fs.copyFileSync(path.join(inputRoot, name), path.join(sourceKit, name), fs.constants.COPYFILE_EXCL);
  const receipt = { schema_version: 1, runtime_files: lock.windows.files.length, files: lock.windows.files, notices: lock.windows.notices, redistribution: lock.windows.redistribution, sources: lock.windows.source_archives.map(n => byName.get(n.toLowerCase())), imports };
  fs.writeFileSync(path.join(legal, "windows-runtime-inventory.json"), JSON.stringify(receipt, null, 2) + "\n", { flag: "wx", mode: 0o644 });
  return receipt;
}
module.exports = { readPEImports, validateClosure, isSystemImport, acquireWindowsRuntime };
