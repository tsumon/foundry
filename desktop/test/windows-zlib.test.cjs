"use strict";
const test = require("node:test"), assert = require("node:assert/strict"), crypto = require("node:crypto");
let zlib = {}; try { zlib = require("../sandbox/windows-zlib.cjs"); } catch (e) { if (e.code !== "MODULE_NOT_FOUND") throw e; }
const hash = raw => crypto.createHash("sha256").update(raw).digest("hex");
test("pinned patch requires exact original bytes, hunk context and resulting bytes", () => {
  assert.equal(typeof zlib.applyPinnedPatch, "function", "Exact source patching is missing");
  const original = Buffer.from("one\nunsafe\nthree\n"), fixed = Buffer.from("one\nsafe\nthree\n");
  const patch = Buffer.from("--- a/gzwrite.c\n+++ b/gzwrite.c\n@@ -1,3 +1,3 @@\n one\n-unsafe\n+safe\n three\n");
  const spec = { sha256: hash(patch), files: [{ path: "gzwrite.c", before_sha256: hash(original), sha256: hash(fixed) }] };
  const source = new Map([["gzwrite.c", original]]);
  assert.deepEqual(zlib.applyPinnedPatch(source, patch, spec).get("gzwrite.c"), fixed);
  assert.deepEqual(source.get("gzwrite.c"), original, "Input snapshot is not mutated");
  assert.throws(() => zlib.applyPinnedPatch(new Map([["gzwrite.c", fixed]]), patch, spec), /source|integrity/i);
  assert.throws(() => zlib.applyPinnedPatch(source, Buffer.from("different patch"), spec), /patch|integrity/i);
  const badContext = Buffer.from(patch.toString().replace(" one\n", " other\n"));
  assert.throws(() => zlib.applyPinnedPatch(source, badContext, { ...spec, sha256: hash(badContext) }), /context|hunk/i);
  assert.throws(() => zlib.applyPinnedPatch(source, patch, { ...spec, files: [{ ...spec.files[0], sha256: "0".repeat(64) }] }), /result|integrity/i);
  const extra = Buffer.from(patch + "--- a/other.c\n+++ b/other.c\n@@ -1 +1 @@\n-a\n+b\n");
  assert.throws(() => zlib.applyPinnedPatch(source, extra, { ...spec, sha256: hash(extra) }), /file|inventory/i);
});
// One bounded AMD64 DLL with a named ordinal export and no imports.
function dll() {
  const b = Buffer.alloc(2048); b.write("MZ"); b.writeUInt32LE(128, 60); b.write("PE\0\0", 128);
  b.writeUInt16LE(0x8664, 132); b.writeUInt16LE(1, 134); b.writeUInt16LE(240, 148); b.writeUInt16LE(0x20b, 152); b.writeUInt32LE(16, 260);
  b.writeUInt32LE(4096, 264); b.writeUInt32LE(128, 268);
  b.writeUInt32LE(1536, 400); b.writeUInt32LE(4096, 404); b.writeUInt32LE(1536, 408); b.writeUInt32LE(512, 412);
  b.writeUInt32LE(1, 528); b.writeUInt32LE(1, 532); b.writeUInt32LE(1, 536);
  b.writeUInt32LE(4140, 540); b.writeUInt32LE(4144, 544); b.writeUInt32LE(4148, 548);
  b.writeUInt32LE(4608, 556); b.writeUInt32LE(4160, 560); b.writeUInt16LE(0, 564); b.write("gzwrite\0", 576); return b;
}
test("generated zlib rejects old bytes, altered ABI, missing native tests and substituted provenance", () => {
  assert.equal(typeof zlib.zlibArtifactReceipt, "function", "Patched zlib receipt is missing");
  const raw = dll(), lock = { windows: { zlib: { advisory: "CVE-2026-85091", rejected_runtime_sha256: hash(Buffer.from("old")), abi_exports: [{ name: "gzwrite", ordinal: 1 }] } } };
  const receipt = zlib.zlibArtifactReceipt(raw, lock, ["upstream-example", "upstream-example64", "gzip-roundtrip", "dll-abi", "nonblocking-regression"]);
  assert.equal(zlib.verifyZlibArtifact(raw, receipt, lock), true);
  assert.throws(() => zlib.verifyZlibArtifact(Buffer.from("old"), receipt, lock), /vulnerable|rejected/i);
  assert.throws(() => zlib.zlibArtifactReceipt(raw, lock, []), /native test/i);
  assert.throws(() => zlib.verifyZlibArtifact(raw, { ...receipt, inputs: {} }, lock), /provenance/i);
  const changed = dll(); changed.write("badname\0", 576);
  assert.throws(() => zlib.zlibArtifactReceipt(changed, lock, receipt.native_tests), /ABI/i);
  assert.throws(() => zlib.verifyZlibArtifact(raw, { ...receipt, sha256: "0".repeat(64) }, lock), /integrity/i);
  const available = new Map([["qemu-system-x86_64.exe", raw]]);
  zlib.addPatchedZlib(available, { raw, receipt }, lock); assert.deepEqual(available.get("zlib1.dll"), raw);
  assert.throws(() => zlib.addPatchedZlib(available, { raw, receipt }, lock), /already|prebuilt/i);
});
test("PE exports preserve ordinals and reject forwarded, unnamed or malformed export tables", () => {
  const { readPEExports } = require("../sandbox/windows-bundle.cjs");
  assert.equal(typeof readPEExports, "function", "PE export verification is missing");
  assert.deepEqual(readPEExports(dll()), [{ name: "gzwrite", ordinal: 1 }]);
  const forward = dll(); forward.writeUInt32LE(4160, 556);
  assert.throws(() => readPEExports(forward), /forward/i);
  const ordinal = dll(); ordinal.writeUInt16LE(1, 564);
  assert.throws(() => readPEExports(ordinal), /ordinal/i);
  const unnamed = dll(); unnamed.writeUInt32LE(2, 532);
  assert.throws(() => readPEExports(unnamed), /unnamed|count/i);
});
test("runtime policy excludes the old package DLL and independently pins the fix inputs", () => {
  const lock = require("../sandbox/inputs.lock.json"), pin = lock.windows.zlib;
  assert.ok(pin, "Pinned patched zlib policy is missing");
  assert.equal(pin.advisory, "CVE-2026-85091");
  assert.equal(pin.fix_commit, "df84af25dc1942490e1d1c899a07619152a46148");
  assert.equal(lock.windows.dependency_files.some(e => e.path === "runtime/zlib1.dll"), false);
  assert.equal(lock.assets.find(e => e.package === "mingw-w64-ucrt-x86_64-zlib").kind, "toolchain");
  for (const name of [pin.source_archive, pin.patches[1].asset]) {
    assert.ok(lock.windows.source_archives.includes(name));
    require("../sandbox/assets.cjs").validateAsset(lock.assets.find(e => e.file === name));
  }
  assert.equal(pin.abi_exports.length, 114);
  assert.equal(typeof zlib.buildPatchedZlib, "function", "Native patched build is missing");
});
