"use strict";
// Build-only Unicode compatibility. No system locale or installed host settings.
const fs = require("node:fs"), path = require("node:path"), crypto = require("node:crypto");
const hash = raw => crypto.createHash("sha256").update(raw).digest("hex");
function manifestBytes() {
  return Buffer.from('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">\n  <assemblyIdentity name="Foundry.QEMU" version="11.1.2.0" type="win32" processorArchitecture="amd64"/>\n  <application xmlns="urn:schemas-microsoft-com:asm.v3">\n    <windowsSettings>\n      <activeCodePage xmlns="http://schemas.microsoft.com/SMI/2019/WindowsSettings">UTF-8</activeCodePage>\n    </windowsSettings>\n  </application>\n</assembly>\n');
}
const utf8PatchIdentity = Object.freeze({
  version: "qemu-11.1.2-foundry-utf8-v1", path: "version.rc",
  before_sha256: "9aefa9b21a1af73d02722dddad0410ec9ac32f034f6e60e1358226414ef5ef23",
  sha256: "3faaa029fd1ec79e18ce5e6ae1f58f62963162a2bba8cd9975e0133bf6200ad5",
  manifest_sha256: hash(manifestBytes())
});
function patchUTF8Source(root) {
  if (!path.isAbsolute(root) || !fs.lstatSync(root).isDirectory() || fs.lstatSync(root).isSymbolicLink()) throw Error("Regular QEMU source directory required");
  const file = path.join(root, "version.rc"), manifest = path.join(root, "foundry-utf8.manifest");
  if (!fs.lstatSync(file).isFile() || fs.lstatSync(file).isSymbolicLink()) throw Error("Regular QEMU resource required");
  const raw = fs.readFileSync(file);
  if (hash(raw) !== utf8PatchIdentity.before_sha256) throw Error("QEMU resource source integrity failure");
  if (fs.lstatSync(manifest, { throwIfNoEntry: false })) throw Error("Fresh QEMU manifest required");
  const fixed = Buffer.concat([raw, Buffer.from('\n1 24 "foundry-utf8.manifest"\n')]);
  if (hash(fixed) !== utf8PatchIdentity.sha256) throw Error("QEMU patched resource integrity failure");
  fs.writeFileSync(manifest, manifestBytes(), { flag: "wx" }); fs.writeFileSync(file, fixed);
  if (hash(fs.readFileSync(file)) !== utf8PatchIdentity.sha256 || hash(fs.readFileSync(manifest)) !== utf8PatchIdentity.manifest_sha256) throw Error("QEMU resource written integrity failure");
  return { ...utf8PatchIdentity };
}
function validateUTF8Proof(proof) {
  const keys = ["acp", "access_ok", "argv_ok", "manifest_ok", "read_ok"];
  if (!proof || !proof.legacy || !proof.fixed || Object.keys(proof).sort().join() !== "fixed,legacy") throw Error("Complete Unicode comparison required");
  for (const item of [proof.legacy, proof.fixed]) {
    if (Object.keys(item).sort().join() !== [...keys].sort().join() || !Number.isInteger(item.acp) || item.acp < 1 || item.acp > 65535 || keys.slice(1).some(k => typeof item[k] !== "boolean")) throw Error("Invalid Unicode comparison facts");
  }
  if (proof.fixed.acp !== 65001 || keys.slice(1).some(k => proof.fixed[k] !== true)) throw Error("UTF-8 native file/resource regression failed");
  return true;
}
function readProbe(run, exe, args, root) {
  let raw;
  try { raw = run(exe, args, root, { stdio: "pipe", env: { SYSTEMROOT: process.env.SystemRoot, LANG: "C", LC_ALL: "C" }, timeout: 15000, maxBuffer: 1024 }); }
  catch { throw Error("Native Unicode probe execution failed"); }
  try {
    if (typeof raw !== "string" || Buffer.byteLength(raw) > 1024) throw Error();
    return JSON.parse(raw);
  } catch { throw Error("Native Unicode probe report failed"); }
}
function runUTF8Regression({ outputRoot, msysRoot, qemu, run, report = console.log }) {
  if (process.platform !== "win32" || process.arch !== "x64") throw Error("Native Windows UCRT regression required");
  const root = path.join(outputRoot, "utf8-regression"); fs.mkdirSync(root);
  const input = path.join(root, "验证-\u{1f60a}.bin"); fs.writeFileSync(input, "FOUNDRY_UTF8_FILE\n", { flag: "wx" });
  fs.writeFileSync(path.join(root, "foundry-utf8.manifest"), manifestBytes(), { flag: "wx" });
  fs.writeFileSync(path.join(root, "utf8.rc"), '1 24 "foundry-utf8.manifest"\n', { flag: "wx" });
  fs.writeFileSync(path.join(root, "foundry-utf8-expected.h"), "static const char expected_manifest[] = " + JSON.stringify(manifestBytes().toString()) + ";\n", { flag: "wx" });
  const gcc = path.join(msysRoot, "ucrt64/bin/gcc.exe"), windres = path.join(msysRoot, "ucrt64/bin/windres.exe");
  const source = path.join(__dirname, "windows-utf8-probe.c"), object = path.join(root, "utf8.o");
  run(windres, ["-O", "coff", "-I", root, "-i", path.join(root, "utf8.rc"), "-o", object], root);
  const facts = {};
  for (const kind of ["legacy", "fixed"]) {
    const exe = path.join(root, kind + ".exe");
    run(gcc, ["-std=c11", "-Wall", "-Wextra", "-Werror", "-Os", "-static-libgcc", "-I", root, source, ...(kind === "fixed" ? [object] : []), "-o", exe], root);
    facts[kind] = readProbe(run, exe, [input, ...(kind === "fixed" ? [qemu] : [])], root);
  }
  // Only the fixed finite schema is publishable. Preserve the comparison before
  // failing the gate so the native result is not lost behind an exit code.
  const safe = {};
  for (const kind of ["legacy", "fixed"]) {
    const item = facts[kind] || {}; safe[kind] = { acp: Number.isInteger(item.acp) && item.acp > 0 && item.acp <= 65535 ? item.acp : "invalid" };
    for (const key of ["argv_ok", "access_ok", "read_ok", "manifest_ok"]) safe[kind][key] = typeof item[key] === "boolean" ? item[key] : "invalid";
  }
  fs.writeFileSync(path.join(root, "comparison.json"), JSON.stringify(safe, null, 2) + "\n", { flag: "wx", mode: 0o600 });
  report("foundry-utf8-comparison " + JSON.stringify(safe));
  validateUTF8Proof(facts); return facts;
}
module.exports = { manifestBytes, utf8PatchIdentity, patchUTF8Source, validateUTF8Proof, readProbe, runUTF8Regression };
