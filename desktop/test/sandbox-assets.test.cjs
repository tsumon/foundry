"use strict";
const test = require("node:test"), assert = require("node:assert/strict");
const crypto = require("node:crypto"), fs = require("node:fs"), os = require("node:os"), path = require("node:path");
const { validateAsset, verifyAsset, fetchAsset, prepareAssets } = require("../sandbox/assets.cjs");
const raw = Buffer.from("verified-runtime-input");
const entry = () => ({ file: "kernel-arm64", url: "https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/aarch64/netboot-3.24.3/vmlinuz-virt", bytes: raw.length, sha256: crypto.createHash("sha256").update(raw).digest("hex"), kind: "kernel", platforms: ["darwin/arm64"], license: "GPL-2.0-only", provenance: "Alpine 3.24.3 immutable release archive" });
test("runtime input validates bytes and digest, not a filename assertion", () => {
  assert.equal(verifyAsset(entry(), raw).sha256, entry().sha256);
  assert.throws(() => verifyAsset(entry(), Buffer.from("modified-runtime-input")), /integrity/);
  for (const bad of [{ bytes: 0 }, { bytes: 1024 ** 3 }, { file: "../kernel" }, { file: "AUX" }, { sha256: "a" }, { url: "http://dl-cdn.alpinelinux.org/file" }, { url: "https://dl-cdn.alpinelinux.org.evil.test/file" }, { url: "https://user:pass@dl-cdn.alpinelinux.org/file" }]) assert.throws(() => validateAsset({ ...entry(), ...bad }));
});
test("official libstdc++ archive names are accepted without allowing path syntax", () => {
  validateAsset({ ...entry(), file: "mingw-w64-ucrt-x86_64-libstdc++-16.2.0-6-any.pkg.tar.zst" });
  validateAsset({ ...entry(), file: "mingw-w64-ucrt-x86_64-pkgconf-1~3.0.10-1-any.pkg.tar.zst" });
  for (const file of ["a+/../b", "a+\\b", "a+:b", "a+%2fb"]) assert.throws(() => validateAsset({ ...entry(), file }));
});
test("only exact declared origins redirect; one fetch and no retry on error", async () => {
  let calls = 0;
  const got = await fetchAsset(entry(), async () => { calls++; return new Response(raw); });
  assert.deepEqual(got, raw); assert.equal(calls, 1);
  calls = 0;
  await assert.rejects(fetchAsset(entry(), async () => { calls++; return new Response("bad", { status: 503 }); }), /503/);
  assert.equal(calls, 1);
  calls = 0;
  await assert.rejects(fetchAsset(entry(), async () => { calls++; return new Response(null, { status: 302, headers: { location: "https://unknown.test/input" } }); }), /origin/);
  assert.equal(calls, 1);
});
test("oversized stream and truncated input fail before installation", async () => {
  await assert.rejects(fetchAsset(entry(), async () => new Response(Buffer.alloc(raw.length + 1))), /bound/);
  await assert.rejects(fetchAsset(entry(), async () => new Response(raw.subarray(1))), /integrity/);
});
test("local inputs are reverified, duplicates and symlinks cannot pass, no overwrite", async t => {
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-sandbox-assets-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const local = path.join(root, "local"); fs.mkdirSync(local); fs.writeFileSync(path.join(local, entry().file), raw);
  const output = path.join(root, "output");
  const receipt = await prepareAssets({ output, local, entries: [entry()], platform: "darwin/arm64" });
  assert.equal(receipt.assets.length, 1); assert.deepEqual(fs.readFileSync(path.join(output, entry().file)), raw);
  await assert.rejects(prepareAssets({ output, local, entries: [entry()], platform: "darwin/arm64" }), /fresh/i);
  await assert.rejects(prepareAssets({ output: path.join(root, "duplicate"), local, entries: [entry(), entry()], platform: "darwin/arm64" }), /duplicate/i);
  fs.unlinkSync(path.join(local, entry().file)); fs.symlinkSync(path.join(output, entry().file), path.join(local, entry().file));
  await assert.rejects(prepareAssets({ output: path.join(root, "symlink"), local, entries: [entry()], platform: "darwin/arm64" }), /regular|symlink/i);
});
