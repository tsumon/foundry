"use strict";
// Native Windows x64 + CGO SQLite. No WSL/cross compiler, signing, or publishing.
const fs = require("node:fs"),
  path = require("node:path"),
  crypto = require("node:crypto");
const { execFileSync, spawnSync } = require("node:child_process");
const {
  validateOutput,
  writeManifest,
  copySandboxBundle,
} = require("./build-support.cjs");
const { verifySandboxBundle } = require("./runtime.cjs");
const {
  rejectToolsetOverrides,
  compilerArguments,
  unsignedWindowsOptions,
  perUserNsisOptions,
} = require("./nsis-template.cjs");
const {
  capturePEHeaders,
  peImports,
  electronCredits,
  verifyFiles,
  compilerVersions,
  toolchainFile,
} = require("./windows-provenance.cjs");
const source = path.dirname(__dirname);
function run(command, args, cwd = source, extra = {}) {
  return execFileSync(command, args, {
    cwd,
    stdio: "inherit",
    env: { ...process.env, ...extra, CSC_IDENTITY_AUTO_DISCOVERY: "false" },
  });
}
async function main() {
  rejectToolsetOverrides(process.env);
  if (process.platform !== "win32" || process.arch !== "x64")
    throw Error("Native Windows x64 required");
  const output = validateOutput(process.env.FOUNDRY_DESKTOP_OUTPUT, source);
  const sandboxBundle = process.env.FOUNDRY_SANDBOX_BUNDLE;
  verifySandboxBundle(sandboxBundle, "windows/amd64");
  const go = process.env.FOUNDRY_GO || "go";
  const goVersion = execFileSync(go, ["version"], { encoding: "utf8" }).trim();
  if (
    !goVersion.startsWith("go version go1.27.3 ") ||
    process.versions.node !== "22.23.2"
  )
    throw Error("Pinned Go 1.27.3 and Node 22.23.2 required");
  const npmCLI = path.join(
    path.dirname(process.execPath),
    "node_modules/npm/bin/npm-cli.js",
  );
  if (
    execFileSync(process.execPath, [npmCLI, "--version"], {
      encoding: "utf8",
    }).trim() !== "10.9.8"
  )
    throw Error("npm 10.9.8 required");
  const cc = "C:\\mingw64\\bin\\gcc.exe";
  if (
    execFileSync(cc, ["-dumpmachine"], { encoding: "utf8" }).trim() !==
    "x86_64-w64-mingw32"
  )
    throw Error("Native MinGW-w64 x64 GCC required");
  const gcc = execFileSync(cc, ["-dumpfullversion"], {
    encoding: "utf8",
  }).trim();
  const headerMacros = execFileSync(
    cc,
    ["-dM", "-E", "-x", "c", "-include", "_mingw.h", "-"],
    { encoding: "utf8", input: "" },
  );
  const versions = compilerVersions(gcc, headerMacros);
  const verbose = spawnSync(cc, ["-v"], { encoding: "utf8" });
  if (verbose.error || verbose.status !== 0)
    throw Error("Cannot capture native GCC configuration");
  const candidateArchives = [];
  for (const [name, args, optional] of [
    ["libgcc.a", ["-print-libgcc-file-name"], false],
    ["libmingw32.a", ["-print-file-name=libmingw32.a"], false],
    ["libmingwex.a", ["-print-file-name=libmingwex.a"], false],
    ["libwinpthread.a", ["-print-file-name=libwinpthread.a"], true],
  ]) {
    const filename = execFileSync(cc, args, { encoding: "utf8" }).trim();
    if (optional && filename === name) {
      candidateArchives.push({ name, available: false });
      continue;
    }
    candidateArchives.push({
      name,
      available: true,
      ...toolchainFile(filename, "C:\\mingw64"),
    });
  }
  const compilerProvenance = {
    ...versions,
    compiler: toolchainFile(cc, "C:\\mingw64"),
    verboseConfiguration: verbose.stdout + verbose.stderr,
    headerMacros,
    candidateArchives,
    scope:
      "Observed compiler and available candidate static archives; not a claim all archives/members are linked or of exact distro source revision",
  };
  const electron = path.join(__dirname, "node_modules/electron/dist");
  for (const name of ["electron.exe", "LICENSE", "LICENSES.chromium.html"])
    if (!fs.existsSync(path.join(electron, name)))
      throw Error("Pinned Windows Electron distribution missing");
  fs.mkdirSync(output);
  const inputs = path.join(output, "inputs");
  const trustedGit = "C:\\Program Files\\Git\\cmd\\git.exe";
  const git = (args) =>
    execFileSync(trustedGit, ["-c", "core.fsmonitor=false", ...args], {
      cwd: source,
      encoding: "utf8",
    }).trim();
  const sourceCommit = git(["rev-parse", "HEAD"]),
    sourceDirty =
      git(["status", "--porcelain", "--untracked-files=normal"]) !== "";
  for (const name of ["bin", "legal"])
    fs.mkdirSync(path.join(inputs, name), { recursive: true });
  copySandboxBundle(
    sandboxBundle,
    path.join(inputs, "sandbox"),
    "windows/amd64",
  );
  run(process.execPath, [npmCLI, "run", "build"], path.join(source, "web"));
  const goBinaries = [];
  for (const name of [
    "foundry",
    "foundry-acceptance",
    "foundry-vm-acceptance",
  ]) {
    run(
      go,
      [
        "build",
        "-trimpath",
        "-buildvcs=false",
        "-ldflags=-s -w",
        "-o",
        path.join(inputs, "bin", name + ".exe"),
        "./cmd/" + name,
      ],
      source,
      {
        CGO_ENABLED: "1",
        CC: cc,
        GOOS: "windows",
        GOARCH: "amd64",
        GOTOOLCHAIN: "local",
      },
    );
    const file = path.join(inputs, "bin", name + ".exe");
    const imports = capturePEHeaders(file);
    fs.writeFileSync(path.join(output, name + "-pe-imports.txt"), imports);
    goBinaries.push({
      file: name + ".exe",
      imports: peImports(imports),
      goBuildInfo: execFileSync(go, ["version", "-m", file], {
        encoding: "utf8",
      }),
    });
  }
  fs.cpSync(path.join(source, "web/dist"), path.join(inputs, "web"), {
    recursive: true,
  });
  for (const name of ["LICENSE", "THIRD_PARTY_NOTICES.md", "third_party"])
    fs.cpSync(path.join(source, name), path.join(inputs, "legal", name), {
      recursive: true,
    });
  fs.copyFileSync(
    path.join(__dirname, "runtime-sources.md"),
    path.join(inputs, "legal", "README-runtime-sources.md"),
  );
  for (const name of ["LICENSE", "LICENSES.chromium.html"])
    fs.copyFileSync(
      path.join(electron, name),
      path.join(inputs, "legal", "Electron-" + name),
    );
  const goroot = execFileSync(go, ["env", "GOROOT"], {
    encoding: "utf8",
  }).trim();
  const credits = electronCredits(
    fs.readFileSync(
      path.join(inputs, "legal", "Electron-LICENSES.chromium.html"),
    ),
  );
  const installerNotices = verifyFiles(
    path.join(inputs, "legal/third_party/installer-notices"),
    require("../third_party/installer-notices/manifest.json").texts,
  );
  const windowsRuntimeNotices = verifyFiles(
    path.join(inputs, "legal/third_party/windows-runtime-notices"),
    require("../third_party/windows-runtime-notices/manifest.json").texts,
  );
  for (const name of ["LICENSE", "PATENTS"])
    fs.copyFileSync(
      path.join(goroot, name),
      path.join(inputs, "legal", "Go-" + name),
    );
  const nsisVersion = "1.2.1";
  const nsisTool =
    await require("app-builder-lib/out/toolsets/windows").getMakeNsisPath(
      nsisVersion,
    );
  const nsisBundle = path.dirname(nsisTool.path);
  const nativeNotices = require("./native-notices.cjs").copyNativeNotices(
    nsisBundle,
    path.join(inputs, "legal", "NSIS-toolset"),
  );
  fs.writeFileSync(
    path.join(inputs, "legal", "NSIS-toolset-inventory.json"),
    JSON.stringify(
      {
        toolset: nsisVersion,
        archive: "nsis-bundle-3.12.tar.gz",
        archiveSHA256:
          "56997fdefe25e7928a1a68b4583d08b240b66cf660234053b20131a74cc082f4",
        scope:
          "available bundle binaries; generated native installer log determines selected plugins",
        ...nativeNotices,
      },
      null,
      2,
    ) + "\n",
  );
  const files = writeManifest(inputs),
    appSource = path.join(output, "app-source");
  fs.mkdirSync(appSource);
  const appFiles = [
    "main.cjs",
    "coding-mode.cjs",
    "runtime.cjs",
    "security.cjs",
    "downloads.cjs",
    "package.json",
  ];
  for (const name of appFiles)
    fs.copyFileSync(path.join(__dirname, name), path.join(appSource, name));
  fs.writeFileSync(
    path.join(appSource, "resource-integrity.json"),
    JSON.stringify({
      manifestSHA256: crypto
        .createHash("sha256")
        .update(fs.readFileSync(path.join(inputs, "resources-manifest.json")))
        .digest("hex"),
    }) + "\n",
  );
  const pkg = require("./package.json");
  fs.writeFileSync(
    path.join(output, "build-inputs.json"),
    JSON.stringify(
      {
        app: pkg.version,
        platform: "windows-x64",
        sourceCommit,
        sourceDirty,
        go: goVersion,
        node: process.versions.node,
        npm: "10.9.8",
        gcc,
        compilerProvenance,
        cc,
        cgo: true,
        goBinaries,
        electronCredits: credits,
        installerNotices,
        windowsRuntimeNotices,
        electron: pkg.devDependencies.electron,
        electronBuilder: pkg.devDependencies["electron-builder"],
        nsisToolset: nsisVersion,
        signing: "unsigned; no verified publisher",
        resources: files,
      },
      null,
      2,
    ) + "\n",
  );
  const { build, Platform, Arch } = require("electron-builder");
  const nsisUtil = require("app-builder-lib/out/targets/nsis/nsisUtil"),
    builderUtil = require("builder-util");
  const { prepareTemplates, selectedPlugins } = require("./nsis-template.cjs");
  const originalTemplates = nsisUtil.nsisTemplatesDir,
    buildTemplates = path.join(output, "nsis-templates");
  const templateChange = prepareTemplates(originalTemplates, buildTemplates);
  const originalSpawn = builderUtil.spawnAndWriteWithOutput,
    compilerRuns = [];
  // Change only the build's in-memory template root, not vendor files or the
  // custom-script option (which would bypass default uninstaller/size checks).
  nsisUtil.nsisTemplatesDir = buildTemplates;
  builderUtil.spawnAndWriteWithOutput = async function (
    command,
    args,
    script,
    options,
  ) {
    if (
      path.resolve(command).toLowerCase() !==
      path.resolve(nsisTool.path).toLowerCase()
    )
      return originalSpawn(command, args, script, options);
    const result = await originalSpawn(
      command,
      compilerArguments(args),
      script,
      options,
    );
    const phase = args.some((arg) => arg === "-DBUILD_UNINSTALLER")
      ? "uninstaller"
      : "installer";
    fs.writeFileSync(
      path.join(output, `nsis-${phase}-compiler.log`),
      result.stdout + "\n" + result.stderr,
    );
    compilerRuns.push({ phase, plugins: selectedPlugins(result.stdout) });
    return result;
  };
  try {
    await build({
      projectDir: appSource,
      targets: Platform.WINDOWS.createTarget(["nsis"], Arch.x64),
      publish: "never",
      config: {
        appId: "org.foundry.desktop",
        productName: "Foundry",
        electronVersion: pkg.devDependencies.electron,
        electronDist: electron,
        asar: true,
        npmRebuild: false,
        directories: { output: path.join(output, "package") },
        files: [...appFiles, "resource-integrity.json"],
        extraResources: [{ from: inputs, to: "." }],
        toolsets: { nsis: nsisVersion },
        win: {
          target: "nsis",
          icon: path.join(__dirname, "icon.ico"),
          artifactName: "Foundry-${version}-x64.exe",
          ...unsignedWindowsOptions,
        },
        nsis: perUserNsisOptions,
        electronFuses: {
          runAsNode: false,
          enableCookieEncryption: false,
          enableNodeOptionsEnvironmentVariable: false,
          enableNodeCliInspectArguments: false,
          enableEmbeddedAsarIntegrityValidation: true,
          onlyLoadAppFromAsar: true,
          grantFileProtocolExtraPrivileges: false,
        },
      },
    });
  } finally {
    nsisUtil.nsisTemplatesDir = originalTemplates;
    builderUtil.spawnAndWriteWithOutput = originalSpawn;
  }
  if (
    compilerRuns.length !== 2 ||
    new Set(compilerRuns.map((run) => run.phase)).size !== 2
  )
    throw Error("Both default native NSIS phases must be traced");
  const selected = [
    ...new Set(compilerRuns.flatMap((run) => run.plugins)),
  ].sort();
  const selectedBinaries = selected.map((name) => {
    const found = nativeNotices.binaries.filter(
      (item) =>
        item.file
          .replaceAll("\\", "/")
          .toLowerCase()
          .endsWith("/x86-unicode/" + name.toLowerCase()) &&
        item.file.startsWith("windows/"),
    );
    if (found.length !== 1)
      throw Error(
        "Unique native x86 Unicode plugin provenance missing: " + name,
      );
    return found[0];
  });
  fs.writeFileSync(
    path.join(output, "nsis-provenance.json"),
    JSON.stringify(
      {
        templateChange,
        compilerRuns,
        selectedBinaries,
        noWinShell: true,
        noElevateHelper: true,
      },
      null,
      2,
    ) + "\n",
  );
  const installer = path.join(
    output,
    "package",
    `Foundry-${pkg.version}-x64.exe`,
  );
  if (!fs.statSync(installer).isFile()) throw Error("Installer missing");
  require("./runtime.cjs").verifyResources(
    path.join(output, "package/win-unpacked/resources"),
  );
  if (
    fs.existsSync(
      path.join(output, "package/win-unpacked/resources/elevate.exe"),
    )
  )
    throw Error("Unexpected elevation helper");
  fs.copyFileSync(installer, path.join(output, path.basename(installer)));
  fs.writeFileSync(
    path.join(output, "SHA256SUMS"),
    crypto
      .createHash("sha256")
      .update(fs.readFileSync(installer))
      .digest("hex") +
      "  " +
      path.basename(installer) +
      "\n",
  );
  console.log("Built unsigned native Windows installer " + installer);
}
main().catch((e) => {
  console.error(e.message);
  process.exitCode = 1;
});
