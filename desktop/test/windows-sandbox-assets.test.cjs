"use strict";
const test = require("node:test"), assert = require("node:assert/strict");
const fs = require("node:fs"), path = require("node:path"), os = require("node:os"), crypto = require("node:crypto"), cp = require("node:child_process");
let implementation = {};
try { implementation = require("../sandbox/windows-bundle.cjs"); } catch (e) { if (e.code !== "MODULE_NOT_FOUND") throw e; }
// Minimal PE32+ with distinct import and delay-import descriptors. Deleting
// delay-directory traversal or relaxing the machine check must fail these tests.
function pe(normal = ["KERNEL32.dll"], delay = ["libsecond.dll"]) {
  const b = Buffer.alloc(4096); b.write("MZ"); b.writeUInt32LE(128, 60); b.write("PE\0\0", 128);
  b.writeUInt16LE(0x8664, 132); b.writeUInt16LE(1, 134); b.writeUInt16LE(240, 148);
  const opt = 152; b.writeUInt16LE(0x20b, opt); b.writeUInt32LE(16, opt + 108);
  const sec = opt + 240; b.write(".rdata", sec); b.writeUInt32LE(3584, sec + 8); b.writeUInt32LE(4096, sec + 12); b.writeUInt32LE(3584, sec + 16); b.writeUInt32LE(512, sec + 20);
  const rva = n => n + 3584;
  if (normal.length) { b.writeUInt32LE(rva(512), opt + 120); b.writeUInt32LE((normal.length + 1) * 20, opt + 124); }
  if (delay.length) { b.writeUInt32LE(rva(768), opt + 216); b.writeUInt32LE((delay.length + 1) * 32, opt + 220); }
  let name = 1024;
  normal.forEach((s, i) => { b.writeUInt32LE(rva(name), 512 + i * 20 + 12); b.write(s + "\0", name); name += 128; });
  delay.forEach((s, i) => { b.writeUInt32LE(1, 768 + i * 32); b.writeUInt32LE(rva(name), 768 + i * 32 + 4); b.write(s + "\0", name); name += 128; });
  return b;
}
test("reads both ordinary and delay imports of AMD64 PE, rejecting wrong machines and malformed RVAs", () => {
  assert.equal(typeof implementation.readPEImports, "function", "PE import validation is not implemented");
  assert.deepEqual(implementation.readPEImports(pe()), ["kernel32.dll", "libsecond.dll"]);
  assert.deepEqual(implementation.readPEImports(pe(["WINSPOOL.DRV"], [])), ["winspool.drv"]);
  const wrong = pe(); wrong.writeUInt16LE(0x14c, 132); assert.throws(() => implementation.readPEImports(wrong), /AMD64/);
  const bad = pe(); bad.writeUInt32LE(0xfffffff0, 512 + 12); assert.throws(() => implementation.readPEImports(bad), /RVA/);
  const va = pe(); va.writeUInt32LE(0, 768); assert.throws(() => implementation.readPEImports(va), /delay/i);
  assert.throws(() => implementation.readPEImports(pe(["../inject.dll"], [])), /import/i);
  assert.throws(() => implementation.readPEImports(Buffer.alloc(20)), /PE/);
});
test("closure rejects missing transitive DLLs, arbitrary API-set names, duplicate casing and unreachable DLLs", () => {
  assert.equal(typeof implementation.validateClosure, "function", "Dependency closure validation is not implemented");
  const files = new Map([["qemu-system-x86_64.exe", pe()], ["libsecond.dll", pe(["ucrtbase.dll"], [])]]);
  assert.equal(implementation.validateClosure(files).length, 2);
  assert.equal(implementation.validateClosure(new Map([["qemu-system-x86_64.exe", pe(["api-ms-win-core-path-l1-1-0.dll"], [])]])).length, 1);
  assert.throws(() => implementation.validateClosure(new Map([["qemu-system-x86_64.exe", pe()]])), /unresolved/i);
  assert.throws(() => implementation.validateClosure(new Map([["qemu-system-x86_64.exe", pe(["api-ms-win-evil-l1-1-0.dll"], [])]])), /unresolved/i);
  assert.throws(() => implementation.validateClosure(new Map([...files, ["LIBSECOND.DLL", pe()]])), /duplicate/i);
  assert.throws(() => implementation.validateClosure(new Map([...files, ["unneeded.dll", pe([], [])]])), /unreachable/i);
});
const sha = b => crypto.createHash("sha256").update(b).digest("hex");
test("bundles only pinned regular members, verifies bytes before output, and refuses missing corresponding sources", async t => {
  assert.equal(typeof implementation.acquireWindowsRuntime, "function", "Verified Windows assembly is not implemented");
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-win-inputs-")); t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.mkdirSync(path.join(root, "input")); const exe = pe(["kernel32.dll"], []); fs.writeFileSync(path.join(root, "input", "qemu-system-x86_64.exe"), exe);
  const notice = Buffer.from("Copyright fixture; GPL-2.0-or-later"); fs.writeFileSync(path.join(root, "input", "COPYING"), notice);
  cp.execFileSync("tar", ["-cf", path.join(root, "test.tar"), "-C", path.join(root, "input"), "qemu-system-x86_64.exe", "COPYING"]);
  const archive = fs.readFileSync(path.join(root, "test.tar")); const source = Buffer.from("source with recipe and license fixture"); fs.writeFileSync(path.join(root, "source.tar"), source);
  const entry = (file, b, kind) => ({ file, bytes: b.length, sha256: sha(b), url: "https://repo.msys2.org/mingw/sources/" + file, kind, platforms: ["windows/amd64"], license: "GPL-2.0-or-later", provenance: "test fixture" });
  const lock = { schema_version: 1, assets: [entry("test.tar", archive, "runtime"), entry("source.tar", source, "source")], windows: { source_archives: ["source.tar"], files: [{ archive: "test.tar", member: "qemu-system-x86_64.exe", path: "runtime/qemu-system-x86_64.exe", bytes: exe.length, sha256: sha(exe) }] } };
  lock.windows.notices = [{ archive: "test.tar", member: "COPYING", path: "legal/licenses/test/COPYING", bytes: notice.length, sha256: sha(notice) }];
  lock.windows.redistribution = [{ archive: "test.tar", source_archive: "source.tar", notices: ["legal/licenses/test/COPYING"] }];
  const outputRoot = path.join(root, "output"); const receipt = await implementation.acquireWindowsRuntime({ lock, cacheRoot: root, outputRoot });
  assert.equal(receipt.runtime_files, 1); assert.deepEqual(fs.readFileSync(path.join(outputRoot, "runtime/qemu-system-x86_64.exe")), exe);
  assert.deepEqual(fs.readFileSync(path.join(outputRoot, "source-kit/source.tar")), source);
  assert.equal(fs.existsSync(path.join(outputRoot, "legal/source.tar")), false);
  assert.deepEqual(fs.readFileSync(path.join(outputRoot, "legal/licenses/test/COPYING")), notice);
  await assert.rejects(implementation.acquireWindowsRuntime({ lock, cacheRoot: root, outputRoot }), /fresh/i);
  const corrupt = structuredClone(lock); corrupt.windows.files[0].sha256 = "0".repeat(64);
  await assert.rejects(implementation.acquireWindowsRuntime({ lock: corrupt, cacheRoot: root, outputRoot: path.join(root, "corrupt") }), /integrity/i);
  const missing = structuredClone(lock); missing.windows.source_archives = [];
  await assert.rejects(implementation.acquireWindowsRuntime({ lock: missing, cacheRoot: root, outputRoot: path.join(root, "missing") }), /source/i);
  const unrelated = structuredClone(lock); unrelated.windows.redistribution[0].source_archive = "unrelated.tar";
  await assert.rejects(implementation.acquireWindowsRuntime({ lock: unrelated, cacheRoot: root, outputRoot: path.join(root, "unrelated") }), /source/i);
  const noNotice = structuredClone(lock); noNotice.windows.notices = [];
  await assert.rejects(implementation.acquireWindowsRuntime({ lock: noNotice, cacheRoot: root, outputRoot: path.join(root, "no-notice") }), /notice/i);
});
