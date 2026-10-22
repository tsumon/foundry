"use strict";
const fs = require("node:fs"),
  path = require("node:path"),
  crypto = require("node:crypto");
const pins = {
  "installer.nsi":
    "8811964416d122612c3e7601728af5d3f998d677918df829a4e8b4c739c8b9f8",
  "uninstaller.nsh":
    "9ee2dac4593478083e8aa6f8487287ce9401006ccd50ecc538871d133ea4a42c",
  "installSection.nsh":
    "74c4dbb4163cdd90ca4850b75d304e916235c882d6af7b5cdad63bffbc977072",
  "oneClick.nsh":
    "87d6c095f37716759c8d9373dcab906394546007abb626c04e1c784158f6de5b",
  "multiUser.nsh":
    "9aca256695c289ec8a875143101fae8c6236bc8c6a7cf369bbe8b4986e09c9cb",
};
const unused = '    WinShell::UninstAppUserModelId "${APP_ID}"\n';
const cosmeticBanner =
  '  ${IfNot} ${Silent}\n    !ifdef HEADER_ICO\n      SpiderBanner::Show /MODERN /ICON "$PLUGINSDIR\\installerHeaderico.ico"\n    !else\n      SpiderBanner::Show /MODERN\n    !endif\n\n    FindWindow $0 "#32770" "" $hwndparent\n    FindWindow $0 "#32770" "" $hwndparent $0\n    GetDlgItem $0 $0 1000\n    SendMessage $0 ${WM_SETTEXT} 0 "STR:$(installing)"\n\n    StrCpy $1 $hwndparent\n\t\tSystem::Call \'user32::ShutdownBlockReasonCreate(${SYSTYPE_PTR}r1, w "$(installing)")\'\n  ${endif}\n';
// Pinned builder26.15.3 toolsets/windows, icons, 7zip and binDownload/
// electronGet honor these before their reviewed archives. Cache locations are
// deliberately not overrides. Match keys case-insensitively on every host.
function rejectToolsetOverrides(env) {
  const toolKeys = new Set([
    "ELECTRON_BUILDER_NSIS_DIR",
    "ELECTRON_BUILDER_NSIS_RESOURCES_DIR",
    "ELECTRON_BUILDER_7ZIP_PATH",
    "ELECTRON_BUILDER_RCEDIT_PATH",
    "ELECTRON_BUILDER_ICONS_TOOLSET_DIR",
    "ELECTRON_BUILDER_WINDOWS_KITS_PATH",
    "ELECTRON_BUILDER_OSSL_SIGNCODE_PATH",
    "SIGNTOOL_PATH",
    "USE_SYSTEM_SIGNCODE",
    "USE_SYSTEM_OSSLSIGNCODE",
    "NSISDIR",
    "NSISCONFDIR",
    "NSISLAUNCHCOMMAND",
    "ELECTRON_BUILDER_BINARIES_DOWNLOAD_OVERRIDE_URL",
    "ELECTRON_BUILDER_BINARIES_ALLOW_HTTP",
  ]);
  for (const [key, value] of Object.entries(env)) {
    if (value === undefined) continue;
    const name = key.toUpperCase();
    // Presence is rejected, including empty CUSTOM_DIR (upstream honors it).
    if (
      toolKeys.has(name) ||
      /^(?:NPM_CONFIG_|NPM_PACKAGE_CONFIG_)?ELECTRON_BUILDER_BINARIES_(?:MIRROR|CUSTOM_DIR)$/.test(
        name,
      )
    )
      throw Error("Build toolset override refused: " + key);
    if (name === "CSC_IDENTITY_AUTO_DISCOVERY") {
      if (value !== "false")
        throw Error("Build signing override refused: " + key);
    } else if (
      /^(?:WIN_)?CSC_/.test(name) ||
      [
        "FORCE_CODE_SIGNING",
        "ELECTRON_BUILDER_FORCE_CODE_SIGNING",
        "NPM_CONFIG_FORCE_CODE_SIGNING",
        "NPM_PACKAGE_CONFIG_FORCE_CODE_SIGNING",
      ].includes(name)
    )
      throw Error("Build signing override refused: " + key);
  }
}
function compilerArguments(args) {
  if (!args.includes("-DONE_CLICK"))
    throw Error("Per-user installer requires ONE_CLICK");
  if (
    args.some((arg) =>
      /^-D(?:INSTALL_MODE_PER_ALL_USERS(?:_REQUIRED|_DEFAULT)?|allowToChangeInstallationDirectory|MULTIUSER_INSTALLMODE_ALLOW_ELEVATION)(?:=|$)/.test(
        arg,
      ),
    )
  )
    throw Error("Per-user installer forbids machine-mode compiler branches");
  // NSIS otherwise reads both global and home nsisconf.nsh, even without an
  // NSISCONFDIR override. Keep default two-phase build but no ambient script.
  return ["-NOCONFIG", "-V4", "-DUAC_VERBOSE=4", "-DMUI_VERBOSE=4", ...args];
}
const unsignedWindowsOptions = Object.freeze({
  signAndEditExecutable: true,
  signExecutable: false,
  forceCodeSigning: false,
});
const perUserNsisOptions = Object.freeze({
  oneClick: true,
  perMachine: false,
  allowElevation: false,
  packElevateHelper: false,
  createDesktopShortcut: false,
  createStartMenuShortcut: false,
  runAfterFinish: true,
  deleteAppDataOnUninstall: false,
  differentialPackage: false,
});
function prepareTemplates(source, destination) {
  for (const [name, sha] of Object.entries(pins))
    if (
      crypto
        .createHash("sha256")
        .update(fs.readFileSync(path.join(source, name)))
        .digest("hex") !== sha
    )
      throw Error("Pinned NSIS template changed: " + name);
  const original = fs.readFileSync(
    path.join(source, "uninstaller.nsh"),
    "utf8",
  );
  if (original.split(unused).length !== 2)
    throw Error("Pinned NSIS template cleanup match changed");
  const section = fs.readFileSync(
    path.join(source, "installSection.nsh"),
    "utf8",
  );
  if (section.split(cosmeticBanner).length !== 2)
    throw Error("Pinned NSIS template banner match changed");
  if (fs.existsSync(destination))
    throw Error("Fresh NSIS template directory required");
  fs.cpSync(source, destination, {
    recursive: true,
    errorOnExist: true,
    force: false,
  });
  // No shortcuts, associations or AUMI registrations are created by this
  // package. Remove only their otherwise unconditional legacy cleanup call.
  // Keep electron-builder's default two-phase uninstaller and size verification.
  fs.writeFileSync(
    path.join(destination, "uninstaller.nsh"),
    original.replace(unused, ""),
  );
  // Keep standard MUI progress and all install/quit/privacy behavior. Only the
  // optional third-party cosmetic overlay is omitted; no SpiderBanner payload.
  fs.writeFileSync(
    path.join(destination, "installSection.nsh"),
    section.replace(cosmeticBanner, ""),
  );
  return {
    originalSHA256: pins,
    change:
      "remove unused WinShell UninstAppUserModelId cleanup and whole cosmetic SpiderBanner window block; no other template changes",
  };
}
function selectedPlugins(stdout) {
  // NSIS 3.12 Source/script.cpp do_add_file emits File: only when embedding;
  // TOK__PLUGINCOMMAND emits Plugin command:. Discovery lists are not payload.
  const plugins = new Set();
  for (const match of stdout.matchAll(
    /^(?:Reserve)?File: "([^"\r\n]+\.dll)"/gim,
  )) {
    const name = path.win32.basename(match[1]);
    if (
      ![
        "System.dll",
        "nsDialogs.dll",
        "nsExec.dll",
        "UAC.dll",
        "StdUtils.dll",
        "Nsis7z.dll",
      ].some((allowed) => allowed.toLowerCase() === name.toLowerCase())
    )
      throw Error("Unreviewed embedded NSIS plugin: " + name);
    plugins.add(name);
  }
  if (!/^Plugin command: /m.test(stdout) || plugins.size === 0)
    throw Error("Native NSIS plugin trace missing");
  return [...plugins].sort();
}
module.exports = {
  prepareTemplates,
  selectedPlugins,
  rejectToolsetOverrides,
  compilerArguments,
  unsignedWindowsOptions,
  perUserNsisOptions,
};
