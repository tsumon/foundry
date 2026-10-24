"use strict";
const {
  app,
  BrowserWindow,
  Menu,
  dialog,
  shell,
  session,
} = require("electron");
const path = require("node:path");
const { execFileSync } = require("node:child_process");
const crypto = require("node:crypto");
const {
  startBackend,
  requestDecision,
  verifyResources,
  childEnvironment,
} = require("./runtime.cjs");
const { createDownloadGuard } = require("./downloads.cjs");
const { createCodingMenuItem } = require("./coding-mode.cjs");
const { createOpenAIHandoff } = require("./openai-handoff.cjs");
const {
  allowNavigation,
  allowExternal,
  windowOptions,
  activationSwitches,
} = require("./security.cjs");
const { manifestSHA256: integrity } = require("./resource-integrity.json");

// Activation arguments are deliberately never parsed or forwarded to a child.
for (const name of activationSwitches(process.argv))
  app.commandLine.removeSwitch(name);
app.enableSandbox();
let windowsDataRoot;
if (process.platform === "win32") {
  verifyResources(process.resourcesPath, integrity);
  // The bundled native helper obtains LocalAppData from the OS Known Folder
  // API and creates a protected DACL before Electron stores any profile data.
  windowsDataRoot = execFileSync(
    path.join(process.resourcesPath, "bin", "foundry.exe"),
    ["--desktop-prepare-data"],
    {
      encoding: "utf8",
      windowsHide: true,
      env: childEnvironment(
        process.env,
        "",
        path.join(process.resourcesPath, "web"),
      ),
    },
  ).trim();
  if (!path.isAbsolute(windowsDataRoot))
    throw Error("Invalid native desktop data root");
  app.setPath("userData", path.join(windowsDataRoot, "electron"));
}
const owner = app.requestSingleInstanceLock();
if (!owner) {
  app.quit();
} else {
  let window = null,
    backend = null,
    changing = false,
    quitting = false;
  const dataRoot =
    windowsDataRoot || path.join(app.getPath("userData"), "controller-data");
  app.on("second-instance", () => {
    if (window) {
      if (window.isMinimized()) window.restore();
      window.show();
      window.focus();
    }
  });
  app.on("web-contents-created", (_event, contents) => {
    contents.on("will-attach-webview", (event) => event.preventDefault());
  });
  async function launch(mode = "normal", repository = "") {
    if (changing || quitting) return;
    changing = true;
    try {
      if (window) {
        window.destroy();
        window = null;
      }
      if (backend) {
        await backend.stop();
        backend = null;
      }
      backend = await startBackend({
        resources: process.resourcesPath,
        dataRoot,
        mode,
        repository,
        integrity,
      });
      const current = backend;
      const partition = "foundry-" + crypto.randomUUID();
      const isolated = session.fromPartition(partition, { cache: false });
      const downloads = createDownloadGuard(
        current.ready.url,
        () => window?.webContents.id,
        (entry) => console.error(JSON.stringify(entry)),
      );
      isolated.setPermissionRequestHandler((_contents, _permission, callback) =>
        callback(false),
      );
      isolated.setPermissionCheckHandler(() => false);
      isolated.on("will-download", (event, item, contents) => {
        const filename = downloads.saveName(item, contents?.id);
        if (!filename || backend !== current || current.isStopping()) {
          event.preventDefault();
          return;
        }
        // No setSavePath: Electron must present its native save dialog. Never
        // auto-open the file or honor an untrusted Content-Disposition filename.
        item.setSaveDialogOptions({
          defaultPath: path.join(app.getPath("downloads"), filename),
          properties: ["showOverwriteConfirmation"],
        });
      });
      isolated.webRequest.onBeforeRequest((details, callback) =>
        callback({ cancel: !allowNavigation(current.ready.url, details.url) }),
      );
      isolated.webRequest.onBeforeSendHeaders((details, callback) => {
        downloads.observeRequest(details);
        callback(
          requestDecision(
            current.ready.url,
            current.capability,
            details.url,
            details.requestHeaders,
          ),
        );
      });
      window = new BrowserWindow(windowOptions(partition));
      isolated.webRequest.onHeadersReceived((details, callback) => {
        downloads.observeResponse(details);
        callback({ cancel: false });
      });
      window.setTitle(
        mode === "normal"
          ? "Foundry — Persistent Workbench"
          : mode === "coding"
            ? "Foundry — Controlled Coding Workbench"
            : "Foundry — SYNTHETIC Local Repository Simulation",
      );
      window.on("page-title-updated", (event) => event.preventDefault());
      window.webContents.on("will-navigate", (event, url) => {
        if (!allowNavigation(current.ready.url, url)) event.preventDefault();
      });
      window.webContents.on("will-redirect", (event, url) => {
        if (!allowNavigation(current.ready.url, url)) event.preventDefault();
      });
      const openAccountHandoff = createOpenAIHandoff({
        platform: process.platform,
        mode,
        isCurrent: () =>
          backend === current &&
          !current.isStopping() &&
          !changing &&
          !quitting,
        verify: async (url) => {
          const response = await isolated.fetch(
            current.ready.url + "/api/desktop/openai-handoff",
            {
              method: "POST",
              credentials: "include",
              headers: {
                "Content-Type": "application/json",
                Origin: current.ready.url,
              },
              body: JSON.stringify({ url }),
              redirect: "error",
            },
          );
          return (
            response.status === 200 && (await response.json()).allowed === true
          );
        },
        open: (url) => shell.openExternal(url),
      });
      window.webContents.setWindowOpenHandler(({ url }) => {
        if (allowExternal(url)) void shell.openExternal(url);
        else void openAccountHandoff(url);
        return { action: "deny" };
      });
      window.on("closed", () => {
        window = null;
        if (!changing && !quitting) app.quit();
      });
      current.exited.then(() => {
        if (!current.isStopping() && !quitting) {
          dialog.showErrorBox(
            "Foundry controller stopped",
            "The local controller stopped. Data and evidence have been retained. Quit and reopen Foundry to try again.",
          );
          app.quit();
        }
      });
      await window.loadURL(current.ready.url);
      window.show();
    } catch {
      if (backend) await backend.stop();
      backend = null;
      dialog.showErrorBox(
        "Foundry could not start",
        "The bundled controller or workbench could not start. Verify the installation and ensure " +
          (process.platform === "win32"
            ? "official Git for Windows is installed at Program Files\\Git\\cmd\\git.exe"
            : "/usr/bin/git is available") +
          " for repository features. Existing data and evidence are retained. There is no automatic retry.",
      );
    } finally {
      changing = false;
    }
  }
  function menu() {
    Menu.setApplicationMenu(
      Menu.buildFromTemplate([
        {
          label: "Foundry",
          submenu: [
            { role: "about" },
            { type: "separator" },
            { label: "Persistent Workbench", click: () => launch() },
            createCodingMenuItem(dialog, launch),
            {
              label: "New Isolated Local Repository Simulation",
              click: () => launch("simulation"),
            },
            {
              label: "Open Committed Descriptor Repository…",
              click: async () => {
                if (changing) return;
                const picked = await dialog.showOpenDialog({
                  title:
                    "Read-only committed repository containing foundry.task.json",
                  properties: ["openDirectory"],
                });
                if (!picked.canceled && picked.filePaths.length === 1)
                  await launch("simulation", picked.filePaths[0]);
              },
            },
            { type: "separator" },
            {
              label: "Show Current Data / Evidence Folder",
              click: () => shell.openPath(backend?.ready.dataDir || dataRoot),
            },
            { type: "separator" },
            { role: "quit" },
          ],
        },
        {
          label: "Edit",
          submenu: [
            { role: "undo" },
            { role: "redo" },
            { type: "separator" },
            { role: "cut" },
            { role: "copy" },
            { role: "paste" },
            { role: "selectAll" },
          ],
        },
        {
          label: "View",
          submenu: [
            { role: "resetZoom" },
            { role: "zoomIn" },
            { role: "zoomOut" },
            { role: "togglefullscreen" },
          ],
        },
        { label: "Window", submenu: [{ role: "minimize" }, { role: "zoom" }] },
        {
          label: "Help",
          submenu: [
            {
              label: "Third-party Notices",
              click: () =>
                shell.openPath(path.join(process.resourcesPath, "legal")),
            },
          ],
        },
      ]),
    );
  }
  app.on("before-quit", (event) => {
    if (quitting) return;
    event.preventDefault();
    quitting = true;
    (async () => {
      if (backend) await backend.stop();
      app.quit();
    })();
  });
  app.on("window-all-closed", () => {
    if (!changing) app.quit();
  });
  app.whenReady().then(() => {
    menu();
    return launch();
  });
}
