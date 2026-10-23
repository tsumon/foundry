"use strict";
// Native macOS builder. No signing identities, accounts, update feeds or releases.
const fs = require("node:fs");
const path = require("node:path");
const { execFileSync } = require("node:child_process");
const crypto = require("node:crypto");
const { validateOutput, writeManifest } = require("./build-support.cjs");
const source = path.dirname(__dirname);
function run(command, args, cwd = source, extra = {}) {
  return execFileSync(command, args, {
    cwd,
    stdio: "inherit",
    env: { ...process.env, CSC_IDENTITY_AUTO_DISCOVERY: "false", ...extra },
  });
}
async function main() {
  if (process.platform !== "darwin" || process.arch !== "arm64")
    throw Error("This build requires native macOS Apple Silicon");
  process.umask(0o022);
  const output = validateOutput(process.env.FOUNDRY_DESKTOP_OUTPUT, source);
  const go = process.env.FOUNDRY_GO || "go";
  const goVersion = execFileSync(go, ["version"], { encoding: "utf8" }).trim();
  if (!goVersion.startsWith("go version go1.27.3 "))
    throw Error("Go 1.27.3 is required");
  if (process.versions.node !== "22.23.2")
    throw Error("Node 22.23.2 is required");
  const npm = path.join(path.dirname(process.execPath), "npm");
  if (
    execFileSync(npm, ["--version"], { encoding: "utf8" }).trim() !== "10.9.8"
  )
    throw Error("npm 10.9.8 is required");
  const electron = path.join(__dirname, "node_modules/electron/dist");
  for (const name of ["LICENSE", "LICENSES.chromium.html", "Electron.app"])
    if (!fs.existsSync(path.join(electron, name)))
      throw Error("Install pinned Electron distribution before packaging");
  fs.mkdirSync(output, { recursive: true });
  const iconset = path.join(output, "Foundry.iconset");
  const icon = path.join(output, "Foundry.icns");
  run("/usr/bin/swift", [path.join(__dirname, "icon.swift"), iconset]);
  run("/usr/bin/iconutil", ["-c", "icns", "-o", icon, iconset]);
  const inputs = path.join(output, "inputs");
  fs.mkdirSync(path.join(inputs, "bin"), { recursive: true });
  fs.mkdirSync(path.join(inputs, "legal"), { recursive: true });
  run(npm, ["run", "build"], path.join(source, "web"));
  for (const name of ["foundry", "foundry-acceptance"]) {
    run(
      go,
      [
        "build",
        "-trimpath",
        "-buildvcs=false",
        "-ldflags=-s -w",
        "-o",
        path.join(inputs, "bin", name),
        "./cmd/" + name,
      ],
      source,
      { CGO_ENABLED: "1", GOOS: "darwin", GOARCH: "arm64" },
    );
    run("/usr/bin/codesign", [
      "--force",
      "--sign",
      "-",
      "--timestamp=none",
      path.join(inputs, "bin", name),
    ]);
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
  for (const name of ["LICENSE", "PATENTS"])
    fs.copyFileSync(
      path.join(goroot, name),
      path.join(inputs, "legal", "Go-" + name),
    );
  const files = writeManifest(inputs);
  const appSource = path.join(output, "app-source");
  fs.mkdirSync(appSource);
  for (const name of [
    "main.cjs",
    "coding-mode.cjs",
    "runtime.cjs",
    "security.cjs",
    "downloads.cjs",
    "package.json",
  ])
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
  const packageJSON = require("./package.json");
  const provenance = {
    source: {
      commit: execFileSync("/usr/bin/git", ["rev-parse", "HEAD"], {
        cwd: source,
        encoding: "utf8",
      }).trim(),
      workingTreeDirty:
        execFileSync("/usr/bin/git", ["status", "--porcelain"], {
          cwd: source,
          encoding: "utf8",
        }).trim() !== "",
    },
    app: packageJSON.version,
    electron: packageJSON.devDependencies.electron,
    electronBuilder: packageJSON.devDependencies["electron-builder"],
    fuses: packageJSON.devDependencies["@electron/fuses"],
    go: goVersion,
    node: process.versions.node,
    npm: "10.9.8",
    signing: "ad-hoc; not Developer ID signed or notarized",
    resources: files,
  };
  fs.writeFileSync(
    path.join(output, "build-inputs.json"),
    JSON.stringify(provenance, null, 2) + "\n",
  );
  const { build, Platform, Arch } = require("electron-builder");
  await build({
    projectDir: appSource,
    targets: Platform.MAC.createTarget(["dir"], Arch.arm64),
    publish: "never",
    config: {
      appId: "org.foundry.desktop",
      productName: "Foundry",
      electronVersion: "45.0.0",
      electronDist: electron,
      asar: true,
      npmRebuild: false,
      directories: { output: path.join(output, "package") },
      files: [
        "main.cjs",
        "coding-mode.cjs",
        "runtime.cjs",
        "security.cjs",
        "downloads.cjs",
        "package.json",
        "resource-integrity.json",
      ],
      extraResources: [{ from: inputs, to: "." }],
      mac: {
        identity: null,
        category: "public.app-category.developer-tools",
        hardenedRuntime: false,
        notarize: false,
        icon,
      },
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
  const app = path.join(output, "package/mac-arm64/Foundry.app");
  // Sign nested native libraries/bundles inside-out without consulting Keychain.
  const signs = [];
  function collect(dir) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const file = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        collect(file);
        if (/\.(app|framework)$/.test(entry.name)) signs.push(file);
      } else if (entry.isFile() && /\.(dylib|so)$/.test(entry.name))
        signs.push(file);
    }
  }
  collect(app);
  for (const file of [...signs, app])
    run("/usr/bin/codesign", [
      "--force",
      "--sign",
      "-",
      "--timestamp=none",
      file,
    ]);
  run("/usr/bin/codesign", ["--verify", "--deep", "--strict", app]);
  require("./runtime.cjs").verifyResources(
    path.join(app, "Contents/Resources"),
  );
  const imageRoot = path.join(output, "dmg-root");
  fs.mkdirSync(imageRoot);
  run("/usr/bin/ditto", [app, path.join(imageRoot, "Foundry.app")]);
  fs.symlinkSync("/Applications", path.join(imageRoot, "Applications"));
  const dmg = path.join(
    output,
    "Foundry-" + packageJSON.version + "-arm64.dmg",
  );
  run("/usr/bin/hdiutil", [
    "create",
    "-volname",
    "Foundry",
    "-srcfolder",
    imageRoot,
    "-format",
    "UDZO",
    "-ov",
    dmg,
  ]);
  fs.writeFileSync(
    path.join(output, "SHA256SUMS"),
    crypto.createHash("sha256").update(fs.readFileSync(dmg)).digest("hex") +
      "  " +
      path.basename(dmg) +
      "\n",
  );
  console.log("Packaged " + app + "\nDMG " + dmg);
}
main().catch((error) => {
  console.error(error.message);
  process.exitCode = 1;
});
