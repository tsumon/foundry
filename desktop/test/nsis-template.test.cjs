"use strict";
const { test } = require("node:test"),
  assert = require("node:assert/strict"),
  fs = require("node:fs"),
  path = require("node:path"),
  os = require("node:os"),
  crypto = require("node:crypto");
const {
  prepareTemplates,
  selectedPlugins,
  rejectToolsetOverrides,
  compilerArguments,
  unsignedWindowsOptions,
  perUserNsisOptions,
} = require("../nsis-template.cjs");
const { spawnSync } = require("node:child_process");
const templates = path.join(
  __dirname,
  "../node_modules/app-builder-lib/templates/nsis",
);
test("pinned toolset admission rejects native, builder and npm override aliases but allows ordinary caches", () => {
  const forbidden = [
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
  ];
  for (const prefix of [
    "",
    "NPM_CONFIG_",
    "npm_config_",
    "npm_package_config_",
  ])
    for (const suffix of ["MIRROR", "CUSTOM_DIR"])
      forbidden.push(prefix + "ELECTRON_BUILDER_BINARIES_" + suffix);
  for (const key of forbidden)
    for (const name of [key, key.toLowerCase()])
      assert.throws(
        () => rejectToolsetOverrides({ [name]: "C:\\unreviewed" }),
        /toolset override refused/i,
        name,
      );
  // Even an empty CUSTOM_DIR is selected by upstream, so presence is rejected.
  assert.throws(
    () =>
      rejectToolsetOverrides({
        npm_config_electron_builder_binaries_custom_dir: "",
      }),
    /toolset override refused/i,
  );
  for (const key of [
    "CSC_LINK",
    "WIN_CSC_LINK",
    "CSC_KEY_PASSWORD",
    "WIN_CSC_KEY_PASSWORD",
    "CSC_NAME",
    "CSC_KEYCHAIN",
    "CSC_FOR_PULL_REQUEST",
    "FORCE_CODE_SIGNING",
    "ELECTRON_BUILDER_FORCE_CODE_SIGNING",
  ])
    assert.throws(
      () => rejectToolsetOverrides({ [key.toLowerCase()]: "sensitive-value" }),
      (e) =>
        /signing override refused/i.test(e.message) &&
        !e.message.includes("sensitive-value"),
    );
  assert.throws(
    () => rejectToolsetOverrides({ CSC_IDENTITY_AUTO_DISCOVERY: "true" }),
    /signing override refused/i,
  );
  assert.doesNotThrow(() =>
    rejectToolsetOverrides({ CSC_IDENTITY_AUTO_DISCOVERY: "false" }),
  );
  assert.doesNotThrow(() =>
    rejectToolsetOverrides({
      ELECTRON_BUILDER_CACHE: "C:\\cache",
      ELECTRON_CACHE: "C:\\electron-cache",
      ELECTRON_DOWNLOAD_CACHE_MODE: "ReadWrite",
      XDG_CACHE_HOME: "C:\\cache",
      npm_config_cache: "C:\\npm",
      LOCALAPPDATA: "C:\\user\\local",
      TEMP: "C:\\temp",
    }),
  );
});
test("configured unsigned policy makes pinned builder skip its signing queue without reading identities", async () => {
  require("app-builder-lib"); // Initialize pinned builder's public import order.
  const { WinPackager } = require("app-builder-lib/out/winPackager");
  const packager = Object.create(WinPackager.prototype);
  Object.defineProperty(packager, "platformSpecificBuildOptions", {
    value: unsignedWindowsOptions,
  });
  Object.defineProperty(packager, "signingQueue", {
    get() {
      throw Error("signing/identity queue consulted");
    },
  });
  assert.equal(
    await packager.signIf(path.join(os.tmpdir(), "Foundry.exe")),
    false,
  );
});
test("build entry rejects overrides before output or external tools on every host", (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-nsis-env-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const out = path.join(root, "must-not-exist");
  for (const key of ["electron_builder_nsis_resources_dir", "win_csc_link"]) {
    const result = spawnSync(
      process.execPath,
      [path.join(__dirname, "../build-windows.cjs")],
      {
        encoding: "utf8",
        env: {
          ...process.env,
          FOUNDRY_DESKTOP_OUTPUT: out,
          FOUNDRY_GO: path.join(root, "must-not-run"),
          [key]: "sensitive-unreviewed-value",
        },
      },
    );
    assert.equal(result.status, 1);
    assert.match(result.stderr, /override refused/i);
    assert.doesNotMatch(result.stderr, /sensitive-unreviewed-value/);
    assert.equal(fs.existsSync(out), false);
  }
});
test("native compiler disables user nsisconf while retaining pinned verbose flags and original arguments", () => {
  const args = [
    "-WX",
    "-INPUTCHARSET",
    "UTF8",
    "-DBUILD_UNINSTALLER",
    "-DONE_CLICK",
    "-",
  ];
  assert.deepEqual(compilerArguments(args), [
    "-NOCONFIG",
    "-V4",
    "-DUAC_VERBOSE=4",
    "-DMUI_VERBOSE=4",
    ...args,
  ]);
  assert.deepEqual(args, [
    "-WX",
    "-INPUTCHARSET",
    "UTF8",
    "-DBUILD_UNINSTALLER",
    "-DONE_CLICK",
    "-",
  ]);
  assert.throws(() => compilerArguments(["-"]), /per-user.*ONE_CLICK/i);
  for (const define of [
    "INSTALL_MODE_PER_ALL_USERS",
    "INSTALL_MODE_PER_ALL_USERS_REQUIRED",
    "INSTALL_MODE_PER_ALL_USERS_DEFAULT",
    "allowToChangeInstallationDirectory",
    "MULTIUSER_INSTALLMODE_ALLOW_ELEVATION",
  ])
    assert.throws(
      () => compilerArguments(["-DONE_CLICK", "-D" + define, "-"]),
      /per-user/i,
    );
});
test("pinned builder emits only per-user one-click branches, without mode page or silent machine-upgrade branches", async () => {
  require("app-builder-lib");
  const { NsisTarget } = require("app-builder-lib/out/targets/nsis/NsisTarget");
  const defines = {};
  const target = {
    options: perUserNsisOptions,
    packager: {
      info: { cancellationToken: { cancelled: false } },
      appInfo: { sanitizedProductName: "Foundry" },
      getResource: async () => null,
      expandMacro: () => "Foundry 0.1.0-alpha.1",
    },
  };
  await NsisTarget.prototype.configureDefines.call(
    target,
    perUserNsisOptions?.oneClick,
    defines,
  );
  assert.equal(defines.ONE_CLICK, null);
  assert.equal(defines.RUN_AFTER_FINISH, null);
  for (const key of [
    "INSTALL_MODE_PER_ALL_USERS",
    "INSTALL_MODE_PER_ALL_USERS_REQUIRED",
    "INSTALL_MODE_PER_ALL_USERS_DEFAULT",
    "allowToChangeInstallationDirectory",
    "MULTIUSER_INSTALLMODE_ALLOW_ELEVATION",
  ])
    assert.equal(Object.hasOwn(defines, key), false, key);
  assert.doesNotThrow(() =>
    compilerArguments([...Object.keys(defines).map((k) => "-D" + k), "-"]),
  );
});
test("build-owned NSIS copy removes only unused AUMI cleanup and cosmetic banner, preserving all other templates", (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-nsis-test-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const output = path.join(root, "copy");
  prepareTemplates(templates, output);
  const original = fs.readFileSync(
    path.join(templates, "uninstaller.nsh"),
    "utf8",
  );
  assert.equal(
    fs.readFileSync(path.join(output, "uninstaller.nsh"), "utf8"),
    original.replace('    WinShell::UninstAppUserModelId "${APP_ID}"\n', ""),
  );
  const entries = fs
    .readdirSync(templates, { recursive: true })
    .filter((n) => fs.statSync(path.join(templates, n)).isFile());
  const section = fs.readFileSync(
    path.join(templates, "installSection.nsh"),
    "utf8",
  );
  const banner =
    '  ${IfNot} ${Silent}\n    !ifdef HEADER_ICO\n      SpiderBanner::Show /MODERN /ICON "$PLUGINSDIR\\installerHeaderico.ico"\n    !else\n      SpiderBanner::Show /MODERN\n    !endif\n\n    FindWindow $0 "#32770" "" $hwndparent\n    FindWindow $0 "#32770" "" $hwndparent $0\n    GetDlgItem $0 $0 1000\n    SendMessage $0 ${WM_SETTEXT} 0 "STR:$(installing)"\n\n    StrCpy $1 $hwndparent\n\t\tSystem::Call \'user32::ShutdownBlockReasonCreate(${SYSTYPE_PTR}r1, w "$(installing)")\'\n  ${endif}\n';
  assert.equal(
    fs.readFileSync(path.join(output, "installSection.nsh"), "utf8"),
    section.replace(banner, ""),
  );
  assert.deepEqual(
    fs
      .readdirSync(output, { recursive: true })
      .filter((n) => fs.statSync(path.join(output, n)).isFile())
      .sort(),
    entries.sort(),
  );
  for (const name of entries)
    if (!["uninstaller.nsh", "installSection.nsh"].includes(name))
      assert.deepEqual(
        fs.readFileSync(path.join(output, name)),
        fs.readFileSync(path.join(templates, name)),
        name,
      );
  assert.equal(
    crypto
      .createHash("sha256")
      .update(fs.readFileSync(path.join(templates, "uninstaller.nsh")))
      .digest("hex"),
    "9ee2dac4593478083e8aa6f8487287ce9401006ccd50ecc538871d133ea4a42c",
  );
});
test("native plugin trace distinguishes discovery from actual embedded DLLs and refuses unknown plugins", () => {
  const log =
    'Processing default plugins: "C:\\NSIS\\Plugins\\*.dll"\n + WinShell::UninstAppUserModelId\nFile: "System.dll"->"$PLUGINSDIR\\System.dll" [compress] 100 bytes\nPlugin command: Call KERNEL32::GetLastError()i\nFile: "StdUtils.dll"->"$PLUGINSDIR\\StdUtils.dll" 200 bytes\nPlugin command: GetAllParameters 0\n';
  assert.deepEqual(selectedPlugins(log), ["StdUtils.dll", "System.dll"]);
  assert.throws(
    () =>
      selectedPlugins(
        log + 'File: "WinShell.dll"->"$PLUGINSDIR\\WinShell.dll" 100 bytes\n',
      ),
    /unreviewed.*WinShell/i,
  );
  assert.throws(
    () => selectedPlugins(log + 'ReserveFile: "Unknown.dll" 100 bytes\n'),
    /unreviewed.*Unknown/i,
  );
  assert.throws(() => selectedPlugins(" + System::Call\n"), /trace.*missing/i);
  assert.throws(
    () =>
      selectedPlugins(
        log +
          'File: "SpiderBanner.dll"->"$PLUGINSDIR\\SpiderBanner.dll" 100 bytes\n',
      ),
    /unreviewed.*SpiderBanner/i,
  );
});
test("template drift is refused before an output tree is created", (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-nsis-drift-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const source = path.join(root, "source"),
    out = path.join(root, "output");
  fs.cpSync(templates, source, { recursive: true });
  fs.appendFileSync(
    path.join(source, "uninstaller.nsh"),
    "\n!echo unexpected\n",
  );
  assert.throws(() => prepareTemplates(source, out), /pinned.*template/i);
  assert.equal(fs.existsSync(out), false);
});
