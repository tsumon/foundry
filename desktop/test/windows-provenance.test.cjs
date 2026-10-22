"use strict";
const { test } = require("node:test"),
  assert = require("node:assert/strict"),
  fs = require("node:fs"),
  path = require("node:path"),
  os = require("node:os"),
  crypto = require("node:crypto");
const {
  peImports,
  electronCredits,
  verifyFiles,
  compilerVersions,
  toolchainFile,
} = require("../windows-provenance.cjs");
test("compiler notices gate exact GCC and actual MinGW64 header versions, not compatibility aliases", () => {
  const macros =
    '#define __GNUC__ 15\n#define __GNUC_MINOR__ 2\n#define __GNUC_PATCHLEVEL__ 0\n#define __MINGW64_VERSION_MAJOR 13\n#define __MINGW64_VERSION_MINOR 0\n#define __MINGW64_VERSION_BUGFIX 0\n#define __MINGW64_VERSION_RC 0\n#define __MINGW64_VERSION_STATE "alpha"\n#define __MINGW32_MAJOR_VERSION 3\n';
  assert.deepEqual(compilerVersions("15.2.0", macros), {
    gcc: "15.2.0",
    mingw: "13.0.0",
    mingwRC: 0,
  });
  for (const version of ["15.2.1", "15.20.0", "15.2", "14.2.0"])
    assert.throws(() => compilerVersions(version, macros), /GCC.*15.2.0/);
  for (const changed of [
    macros.replace("VERSION_MAJOR 13", "VERSION_MAJOR 12"),
    macros.replace("VERSION_BUGFIX 0", "VERSION_BUGFIX 1"),
    macros.replace("VERSION_RC 0", "VERSION_RC 1"),
    macros.replace("__GNUC_MINOR__ 2", "__GNUC_MINOR__ 1"),
    macros.replace("#define __MINGW64_VERSION_MAJOR 13\n", ""),
  ])
    assert.throws(() => compilerVersions("15.2.0", changed), /compiler|MinGW/i);
});
test("toolchain candidate provenance hashes exact bytes and rejects paths outside fixed compiler root", (t) => {
  const root = fs.mkdtempSync(
    path.join(os.tmpdir(), "foundry-compiler-proof-"),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const tool = path.join(root, "gcc.exe");
  fs.writeFileSync(tool, "compiler fixture");
  const result = toolchainFile(tool, root);
  assert.equal(result.path, fs.realpathSync(tool));
  assert.equal(result.bytes, 16);
  assert.equal(
    result.sha256,
    "7542bea79fb5965c7520abacd1736d5ef00ee9397abc3ee60a122214c496d617",
  );
  assert.throws(
    () => toolchainFile(tool, path.join(root, "different")),
    /toolchain|ENOENT/,
  );
  assert.throws(() => toolchainFile("libgcc.a", root), /absolute/i);
});
test("Go PE import admission rejects build-tool DLL dependencies and missing import tables", () => {
  const header = "foundry.exe: file format pei-x86-64\n";
  assert.deepEqual(
    peImports(header + " DLL Name: KERNEL32.dll\n DLL Name: msvcrt.dll\n"),
    ["KERNEL32.dll", "msvcrt.dll"],
  );
  for (const dll of [
    "libgcc_s_seh-1.dll",
    "libwinpthread-1.dll",
    "sqlite3.dll",
    "surprise.dll",
  ])
    assert.throws(
      () =>
        peImports(header + " DLL Name: KERNEL32.dll\n DLL Name: " + dll + "\n"),
      /unexpected.*DLL/i,
    );
  assert.throws(
    () => peImports("file format pe-i386\n DLL Name: KERNEL32.dll\n"),
    /x64/i,
  );
  assert.throws(() => peImports(header), /import.*missing/i);
});
test("exact Electron credits inspection refuses missing Node or FFmpeg sections", () => {
  const html =
    '<html><span class="title">ffmpeg</span><pre>GNU Lesser General Public License version 2.1</pre><span class="title">Node.js</span><pre>Copyright Node.js contributors. Permission is hereby granted</pre></html>';
  const facts = electronCredits(Buffer.from(html));
  assert.deepEqual(facts.projects, ["Node.js", "ffmpeg"]);
  assert.match(facts.sha256, /^[a-f0-9]{64}$/);
  assert.throws(
    () =>
      electronCredits(
        Buffer.from(html.replace("Node.js</span>", "Other</span>")),
      ),
    /Node.js/,
  );
  assert.throws(
    () =>
      electronCredits(
        Buffer.from(html.replace("ffmpeg</span>", "Other</span>")),
      ),
    /ffmpeg/,
  );
});
test("shipping notice/source files must match exact byte and digest manifests", (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-shipping-proof-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const content = Buffer.from("exact notice\r\n"),
    entry = {
      file: "NOTICE.txt",
      bytes: content.length,
      sha256: crypto.createHash("sha256").update(content).digest("hex"),
    };
  fs.writeFileSync(path.join(dir, entry.file), content);
  assert.equal(verifyFiles(dir, [entry]).length, 1);
  fs.writeFileSync(path.join(dir, entry.file), "changed");
  assert.throws(() => verifyFiles(dir, [entry]), /integrity/);
  fs.rmSync(path.join(dir, entry.file));
  assert.throws(() => verifyFiles(dir, [entry]));
});
