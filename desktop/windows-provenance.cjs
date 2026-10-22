"use strict";
// Build/acceptance only: no installed-app dependency or extra runtime tool.
const fs = require("node:fs"),
  path = require("node:path"),
  crypto = require("node:crypto");
const { verifyInput } = require("./runtime-sources.cjs");
function compilerVersions(gcc, macros) {
  if (gcc !== "15.2.0")
    throw Error("GCC 15.2.0 required for pinned runtime notices");
  const expected = {
    __GNUC__: 15,
    __GNUC_MINOR__: 2,
    __GNUC_PATCHLEVEL__: 0,
    __MINGW64_VERSION_MAJOR: 13,
    __MINGW64_VERSION_MINOR: 0,
    __MINGW64_VERSION_BUGFIX: 0,
    __MINGW64_VERSION_RC: 0,
  };
  for (const [name, value] of Object.entries(expected)) {
    const matches = [
      ...macros.matchAll(
        new RegExp(
          "^#define[ \\t]+" + name + "[ \\t]+([0-9]+)[ \\t]*\\r?$",
          "gm",
        ),
      ),
    ];
    if (matches.length !== 1 || Number(matches[0][1]) !== value)
      throw Error("Compiler/MinGW header version mismatch: " + name);
  }
  // Official MinGW v13's STATE is 'alpha'; compatibility MINGW32 macros are
  // not version identity. These headers do not prove every archive member's origin.
  return { gcc: "15.2.0", mingw: "13.0.0", mingwRC: 0 };
}
function toolchainFile(file, root) {
  if (!path.isAbsolute(file) || !path.isAbsolute(root))
    throw Error("Absolute toolchain path required");
  const actual = fs.realpathSync(file),
    base = fs.realpathSync(root),
    relative = path.relative(base, actual);
  if (
    !relative ||
    relative === ".." ||
    relative.startsWith(".." + path.sep) ||
    path.isAbsolute(relative) ||
    !fs.statSync(actual).isFile()
  )
    throw Error("File outside fixed toolchain root");
  const raw = fs.readFileSync(actual);
  return {
    path: actual,
    bytes: raw.length,
    sha256: crypto.createHash("sha256").update(raw).digest("hex"),
  };
}
function peImports(output) {
  if (!/file format pei-x86-64\b/.test(output))
    throw Error("Native x64 PE required");
  const names = [
    ...new Set(
      [...output.matchAll(/^\s*DLL Name:\s*(\S+)\s*$/gm)].map((m) => m[1]),
    ),
  ].sort();
  if (!names.some((n) => n.toLowerCase() === "kernel32.dll"))
    throw Error("PE import table missing");
  const system = new Set([
    "kernel32.dll",
    "msvcrt.dll",
    "ucrtbase.dll",
    "ntdll.dll",
    "advapi32.dll",
    "user32.dll",
    "shell32.dll",
    "ws2_32.dll",
    "bcrypt.dll",
    "crypt32.dll",
    "secur32.dll",
    "iphlpapi.dll",
    "dnsapi.dll",
    "userenv.dll",
    "winmm.dll",
    "ole32.dll",
    "oleaut32.dll",
    "version.dll",
    "psapi.dll",
    "shlwapi.dll",
  ]);
  for (const name of names)
    if (
      !system.has(name.toLowerCase()) &&
      !/^api-ms-win-crt-[a-z0-9-]+\.dll$/i.test(name)
    )
      throw Error("Unexpected Go runtime DLL: " + name);
  return names;
}
function electronCredits(raw) {
  const html = raw.toString("utf8"),
    projects = [...html.matchAll(/<span class="title">([^<]+)<\/span>/g)].map(
      (m) => m[1],
    );
  for (const name of ["Node.js", "ffmpeg"])
    if (!projects.includes(name))
      throw Error("Electron credits missing " + name);
  for (const text of [
    "Copyright Node.js contributors",
    "Permission is hereby granted",
    "GNU Lesser General Public License",
  ])
    if (!html.includes(text))
      throw Error("Electron credits incomplete: " + text);
  // Chromium's generated credits intentionally omit closing html/body tags.
  // Record the complete supplied bytes; section checks are not a legal audit.
  return {
    bytes: raw.length,
    sha256: crypto.createHash("sha256").update(raw).digest("hex"),
    projects: projects.sort(),
  };
}
function verifyFiles(directory, entries) {
  return entries.map((entry) => {
    const file = path.join(directory, entry.file);
    if (!fs.lstatSync(file).isFile())
      throw Error("Regular shipping source/notice required");
    return verifyInput(entry, fs.readFileSync(file));
  });
}
function verifyShipping(sourceKit, legal) {
  if (!path.isAbsolute(sourceKit) || !path.isAbsolute(legal))
    throw Error("Absolute shipping inputs required");
  return {
    runtimeSources: verifyFiles(
      sourceKit,
      require("./runtime-sources.json").files,
    ),
    installerSources: verifyFiles(
      path.join(sourceKit, "installer"),
      require("./installer-sources.json").files,
    ),
    installerNotices: verifyFiles(
      path.join(legal, "third_party/installer-notices"),
      require("../third_party/installer-notices/manifest.json").texts,
    ),
    windowsRuntimeNotices: verifyFiles(
      path.join(legal, "third_party/windows-runtime-notices"),
      require("../third_party/windows-runtime-notices/manifest.json").texts,
    ),
    electronCredits: electronCredits(
      fs.readFileSync(path.join(legal, "Electron-LICENSES.chromium.html")),
    ),
  };
}
module.exports = {
  peImports,
  electronCredits,
  verifyFiles,
  verifyShipping,
  compilerVersions,
  toolchainFile,
};
if (require.main === module) {
  try {
    console.log(
      JSON.stringify(
        verifyShipping(process.argv[2] || "", process.argv[3] || ""),
      ),
    );
  } catch (e) {
    console.error(e.message);
    process.exitCode = 1;
  }
}
