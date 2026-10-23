"use strict";
const test = require("node:test"), assert = require("node:assert/strict");
const fs = require("node:fs"), os = require("node:os"), path = require("node:path"), crypto = require("node:crypto");
let utf8 = {}; try { utf8 = require("../sandbox/windows-utf8.cjs"); } catch (e) { if (e.code !== "MODULE_NOT_FOUND") throw e; }
const original = () => fs.readFileSync(path.join(__dirname, "fixtures/qemu-11.1.2-version.rc"));
const sha = raw => crypto.createHash("sha256").update(raw).digest("hex");
function fixture(t) {
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-utf8-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.writeFileSync(path.join(root, "version.rc"), original()); return root;
}
test("pinned QEMU resource patch embeds the UTF-8 process manifest without changing version resources", t => {
  assert.equal(typeof utf8.patchUTF8Source, "function", "UTF-8 resource patch is missing");
  const root = fixture(t), receipt = utf8.patchUTF8Source(root);
  const rc = fs.readFileSync(path.join(root, "version.rc"));
  assert.equal(rc.subarray(0, original().length).equals(original()), true);
  assert.equal(rc.subarray(original().length).toString(), '\n1 24 "foundry-utf8.manifest"\n');
  assert.equal(sha(rc), receipt.sha256);
  const manifest = fs.readFileSync(path.join(root, "foundry-utf8.manifest"));
  assert.equal(sha(manifest), receipt.manifest_sha256);
  assert.equal(manifest.equals(utf8.manifestBytes()), true);
  assert.throws(() => utf8.patchUTF8Source(root), /integrity|fresh/i);
});
test("resource patch refuses tampered, linked and pre-existing inputs before modifying any source", t => {
  assert.equal(typeof utf8.patchUTF8Source, "function");
  for (const kind of ["tampered", "symlink", "manifest"]) {
    const root = fixture(t), file = path.join(root, "version.rc");
    if (kind === "tampered") fs.appendFileSync(file, "changed");
    if (kind === "symlink") { fs.renameSync(file, path.join(root, "original.rc")); fs.symlinkSync("original.rc", file); }
    if (kind === "manifest") fs.writeFileSync(path.join(root, "foundry-utf8.manifest"), "unrelated");
    const before = fs.readFileSync(file);
    assert.throws(() => utf8.patchUTF8Source(root), /integrity|regular|fresh/i);
    assert.deepEqual(fs.readFileSync(file), before);
  }
});
test("native Unicode gate rejects failed reads, lost arguments and missing PE manifest instead of accepting exit zero", () => {
  assert.equal(typeof utf8.validateUTF8Proof, "function", "Native Unicode gate is missing");
  const legacy = { acp: 1252, argv_ok: false, access_ok: false, read_ok: false, manifest_ok: true };
  const fixed = { acp: 65001, argv_ok: true, access_ok: true, read_ok: true, manifest_ok: true };
  assert.equal(utf8.validateUTF8Proof({ legacy, fixed }), true);
  assert.equal(utf8.validateUTF8Proof({ legacy: fixed, fixed }), true);
  for (const key of ["argv_ok", "access_ok", "read_ok", "manifest_ok"]) {
    assert.throws(() => utf8.validateUTF8Proof({ legacy, fixed: { ...fixed, [key]: false } }));
    assert.throws(() => utf8.validateUTF8Proof({ legacy, fixed: { ...fixed, [key]: "true" } }));
  }
  assert.throws(() => utf8.validateUTF8Proof({ legacy, fixed: { ...fixed, acp: 1252 } }));
  assert.throws(() => utf8.validateUTF8Proof({ legacy: { ...legacy, acp: 0 }, fixed }));
  assert.throws(() => utf8.validateUTF8Proof({ legacy, fixed: { ...fixed, private_path: "private" } }));
});
test("native probe failures never expose commands, raw stderr or malformed output, and never retry", () => {
  assert.equal(typeof utf8.readProbe, "function", "Redacted native probe boundary is missing");
  for (const failure of [Object.assign(Error("private command and stderr"), { code: "ETIMEDOUT", stderr: "private token" }), Object.assign(Error("private exit"), { status: 1 }), "invalid private stdout", "x".repeat(1025)]) {
    let calls = 0;
    const run = (_program, _args, _cwd, options) => {
      calls++; assert.equal(options.stdio, "pipe"); assert.equal(options.timeout, 15000); assert.equal(options.maxBuffer, 1024);
      if (failure instanceof Error) throw failure; return failure;
    };
    assert.throws(() => utf8.readProbe(run, "private.exe", ["private-file"], "private-root"), error => {
      assert.match(error.message, /^Native Unicode probe (execution|report) failed$/);
      assert.equal(error.cause, undefined); assert.doesNotMatch(error.stack, /private command|private token|private stdout|private exit/); return true;
    });
    assert.equal(calls, 1);
  }
  const facts = { acp: 65001, argv_ok: true, access_ok: true, read_ok: true, manifest_ok: true };
  assert.deepEqual(utf8.readProbe(() => JSON.stringify(facts), "probe", [], "/tmp"), facts);
});
