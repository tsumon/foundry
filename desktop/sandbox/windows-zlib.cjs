"use strict";
// Build-only security backport. No prebuilt zlib DLL is a runtime candidate.
const fs = require("node:fs"), path = require("node:path"), crypto = require("node:crypto"), { isDeepStrictEqual } = require("node:util");
const { readPEExports, readPEImports, isSystemImport } = require("./windows-bundle.cjs");
const hash = b => crypto.createHash("sha256").update(b).digest("hex");
const nativeTests = ["upstream-example", "upstream-example64", "gzip-roundtrip", "dll-abi", "nonblocking-regression"];
function applyPinnedPatch(sources, raw, spec) {
  if (hash(raw) !== spec.sha256) throw Error("Patch integrity failure");
  const lines = raw.toString("utf8").split("\n"), result = new Map(), expected = new Map(spec.files.map(e => [e.path, e]));
  for (let i = 0; i < lines.length; i++) {
    if (!lines[i].startsWith("--- ")) continue;
    const oldName = lines[i].slice(4).split(/\s/)[0].split("/").slice(1).join("/");
    const name = (lines[++i] || "").replace(/^\+\+\+ /, "").split(/\s/)[0].split("/").slice(1).join("/");
    const e = expected.get(name), source = sources.get(name);
    if (oldName !== name || !e || result.has(name) || !source || hash(source) !== e.before_sha256) throw Error("Patch source file integrity/inventory failure");
    const original = source.toString("utf8").split("\n"), output = []; let cursor = 0, hunks = 0;
    while (i + 1 < lines.length && lines[i + 1].startsWith("@@ ")) {
      const m = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(lines[++i]);
      if (!m) throw Error("Invalid patch hunk");
      const start = Number(m[1]) - 1, oldCount = Number(m[2] ?? 1), newCount = Number(m[4] ?? 1);
      if (start < cursor || start > original.length || output.length + start - cursor !== Number(m[3]) - 1) throw Error("Patch hunk offset mismatch");
      output.push(...original.slice(cursor, start)); cursor = start; let oldUsed = 0, newUsed = 0;
      while (oldUsed < oldCount || newUsed < newCount) {
        const line = lines[++i]; if (line === undefined) throw Error("Truncated patch hunk");
        const sign = line === "" ? " " : line[0], text = line === "" ? "" : line.slice(1);
        if (sign === " " || sign === "-") { if (oldUsed >= oldCount || original[cursor++] !== text) throw Error("Patch context mismatch"); oldUsed++; }
        if (sign === " " || sign === "+") { if (newUsed >= newCount) throw Error("Patch hunk size mismatch"); output.push(text); newUsed++; }
        if (![" ", "-", "+"].includes(sign)) throw Error("Invalid patch hunk line");
      }
      hunks++;
    }
    if (!hunks) throw Error("Missing patch hunk");
    output.push(...original.slice(cursor)); const fixed = Buffer.from(output.join("\n"));
    if (hash(fixed) !== e.sha256) throw Error("Patched result integrity failure"); result.set(name, fixed);
  }
  if (result.size !== expected.size) throw Error("Patch file inventory mismatch");
  return result;
}
function identity(lock) {
  return { configuration: lock.windows.zlib, toolchain: (lock.windows.build_packages || []).map(name => { const e = lock.assets.find(e => e.package === name); return { file: e.file, sha256: e.sha256 }; }), recipes: ["windows-zlib.cjs", "zlib-regression.c"].map(file => ({ file, sha256: hash(fs.readFileSync(path.join(__dirname, file))) })) };
}
function zlibArtifactReceipt(raw, lock, tests) {
  if (hash(raw) === lock.windows.zlib.rejected_runtime_sha256) throw Error("Known vulnerable zlib runtime rejected");
  if (!isDeepStrictEqual(tests, nativeTests)) throw Error("Required native tests have not passed");
  const exports = readPEExports(raw);
  if (!isDeepStrictEqual(exports, lock.windows.zlib.abi_exports)) throw Error("zlib ABI export mismatch");
  const imports = readPEImports(raw);
  if (imports.some(n => !isSystemImport(n))) throw Error("Unexpected zlib runtime dependency");
  return { kind: "source-built-patched-zlib", inputs: identity(lock), bytes: raw.length, sha256: hash(raw), exports, imports, native_tests: [...tests] };
}
function verifyZlibArtifact(raw, proof, lock) {
  if (hash(raw) === lock.windows.zlib.rejected_runtime_sha256) throw Error("Known vulnerable zlib runtime rejected");
  if (!proof || proof.kind !== "source-built-patched-zlib" || !isDeepStrictEqual(proof.inputs, identity(lock))) throw Error("Patched zlib provenance required");
  if (!isDeepStrictEqual(proof, zlibArtifactReceipt(raw, lock, proof.native_tests))) throw Error("Patched zlib artifact integrity mismatch");
  return true;
}
function addPatchedZlib(available, artifact, lock) {
  if (available.has("zlib1.dll")) throw Error("Prebuilt zlib already present");
  verifyZlibArtifact(artifact.raw, artifact.receipt, lock); available.set("zlib1.dll", artifact.raw);
}
const cSources = ["adler32", "compress", "crc32", "deflate", "gzclose", "gzlib", "gzread", "gzwrite", "infback", "inffast", "inflate", "inftrees", "trees", "uncompr", "zutil"];
function buildPatchedZlib({ lock, inputsRoot, outputRoot, msysRoot, run, tar }) {
  if (process.platform !== "win32" || process.arch !== "x64") throw Error("Native Windows x64 zlib build required");
  const pin = lock.windows.zlib, root = path.join(outputRoot, "zlib-patched"), source = path.join(root, "source");
  fs.mkdirSync(root); fs.mkdirSync(source);
  const input = name => { const raw = fs.readFileSync(path.join(inputsRoot, name)); require("./assets.cjs").verifyAsset(lock.assets.find(e => e.file === name), raw); return raw; };
  input(pin.source_archive);
  const member = (archive, name, bytes) => {
    const args = [archive, "--include", name], opts = { input: bytes, encoding: "utf8", maxBuffer: 8 * 1024 * 1024 };
    const listing = run(tar, ["-tvf", ...args], root, opts).trim().split("\n");
    if (listing.length !== 1 || !listing[0].startsWith("-")) throw Error("Regular zlib source member required");
    return run(tar, ["-xOf", ...args], root, { ...opts, encoding: null });
  };
  const inner = member(path.join(inputsRoot, pin.source_archive), pin.source_member);
  if (inner.length !== pin.source_bytes || hash(inner) !== pin.source_sha256) throw Error("Upstream zlib source integrity failure");
  const names = run(tar, ["-tf", "-"], root, { input: inner }).trim().split("\n").filter(n => /^zlib-1\.3\.2\/(?:[A-Za-z][A-Za-z0-9._-]*\.(?:c|h|h\.in)|test\/(?:example|minigzip)\.c|LICENSE)$/.test(n));
  if (new Set(names).size !== names.length || names.length < 20 || names.length > 64) throw Error("Invalid zlib source inventory");
  const sources = new Map(names.map(n => [n.slice("zlib-1.3.2/".length), member("-", n, inner)]));
  for (const spec of pin.patches) {
    const raw = spec.asset ? input(spec.asset) : member(path.join(inputsRoot, spec.archive), spec.member);
    for (const [name, bytes] of applyPinnedPatch(sources, raw, spec)) sources.set(name, bytes);
  }
  for (const [name, bytes] of sources) { const target = path.join(source, name); fs.mkdirSync(path.dirname(target), { recursive: true }); fs.writeFileSync(target, bytes, { flag: "wx" }); }
  const gcc = path.join(msysRoot, "ucrt64/bin/gcc.exe"), log = [];
  const command = (program, args, options = {}) => { const out = run(program, args, root, options); log.push({ program: path.basename(program), args, output_sha256: hash(Buffer.isBuffer(out) ? out : Buffer.from(out)) }); return out; };
  // Equivalent to upstream win32/Makefile.gcc's C source list, with MSYS2's
  // export-all-symbols ABI and exact large-file header patch retained. No make,
  // autotools, source downloads, static zlib link or unpinned compiler search.
  const flags = ["-std=c99", "-O2", "-g0", "-DHAVE_UNISTD_H=1", "-DHAVE_STDARG_H=1", "-D_LARGEFILE64_SOURCE=1", "-I", source, "-ffile-prefix-map=" + source + "=/usr/src/zlib"];
  for (const name of cSources) command(gcc, [...flags, "-c", path.join(source, name + ".c"), "-o", name + ".o"]);
  const objects = cSources.map(n => n + ".o");
  command(gcc, ["-shared", "-static-libgcc", "-Wl,--export-all-symbols,--enable-auto-image-base,--no-insert-timestamp,--out-implib,libz.dll.a", "-o", "zlib1.dll", ...objects]);
  // Test executables load zlib1.dll next to them and use installed MSYS2
  // consumer headers/import ABI; they cannot silently test the prebuilt DLL.
  const consumer = ["-std=c99", "-O2", "-static-libgcc", "-I", path.join(msysRoot, "ucrt64/include")];
  for (const [name, extra] of [["example", []], ["example64", ["-D_FILE_OFFSET_BITS=64"]]]) {
    command(gcc, [...consumer, ...extra, path.join(source, "test/example.c"), "libz.dll.a", "-o", name + ".exe"]);
    command(path.join(root, name + ".exe"), []);
  }
  command(gcc, [...consumer, path.join(source, "test/minigzip.c"), "libz.dll.a", "-o", "minigzip.exe"]);
  const plain = Buffer.from("Foundry patched zlib gzip roundtrip\n".repeat(256));
  const compressed = command(path.join(root, "minigzip.exe"), [], { input: plain, encoding: null });
  if (!command(path.join(root, "minigzip.exe"), ["-d"], { input: compressed, encoding: null }).equals(plain)) throw Error("Native zlib gzip roundtrip failed");
  const regression = path.join(__dirname, "zlib-regression.c");
  command(gcc, [...consumer, "-DFOUNDRY_DLL_ABI_TEST", regression, "libz.dll.a", "-o", "abi.exe"]); command(path.join(root, "abi.exe"), []);
  command(gcc, [...flags, "-Dwrite=foundry_stall_write", "-c", path.join(source, "gzwrite.c"), "-o", "gzwrite-regression.o"]);
  command(gcc, [...flags, "-static-libgcc", regression, ...objects.filter(n => n !== "gzwrite.o"), "gzwrite-regression.o", "-o", "regression.exe"]); command(path.join(root, "regression.exe"), []);
  const raw = fs.readFileSync(path.join(root, "zlib1.dll")), receipt = zlibArtifactReceipt(raw, lock, nativeTests);
  fs.writeFileSync(path.join(root, "build.json"), JSON.stringify({ receipt, commands: log }, null, 2) + "\n", { flag: "wx" });
  return { raw, receipt };
}
module.exports = { applyPinnedPatch, zlibArtifactReceipt, verifyZlibArtifact, addPatchedZlib, buildPatchedZlib };
