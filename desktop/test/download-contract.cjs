// Native Electron characterization: real HTTP cookie/header/download events.
// Not bundled. No accounts or real controller data; downloads are cancelled.
const { app, BrowserWindow, session } = require("electron");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const http = require("node:http");
const assert = require("node:assert/strict");
const { windowOptions } = require("../security.cjs");
const { requestDecision } = require("../runtime.cjs");
const { createDownloadGuard } = require("../downloads.cjs");
app.setPath(
  "userData",
  fs.mkdtempSync(path.join(os.tmpdir(), "foundry-download-contract-")),
);
let win, server, timer;
function finish(error) {
  clearTimeout(timer);
  if (win && !win.isDestroyed()) win.destroy();
  if (server) server.close();
  if (error) console.error(error.message);
  app.exit(error ? 1 : 0);
}
app
  .whenReady()
  .then(async () => {
    const kind = process.argv[2] || "patch";
    assert.ok(["patch", "report"].includes(kind));
    const expectedName = kind === "patch" ? "changes.patch" : "report.md";
    const route = "/api/missions/0123456789abcdef0123456789abcdef/" + kind;
    server = http.createServer((req, res) => {
      if (req.url === route) {
        const authenticated =
          req.headers.cookie === "foundry_session=fixture-cookie" &&
          req.headers["x-foundry-desktop"] === "fixture-capability";
        console.log(JSON.stringify({ event: "server", authenticated }));
        if (!authenticated) {
          res.writeHead(403);
          res.end();
          return;
        }
        res.writeHead(200, {
          "Content-Type": kind === "patch" ? "text/plain" : "text/markdown",
          "Content-Disposition": 'attachment; filename="' + expectedName + '"',
        });
        res.end("fixture artifact");
        return;
      }
      res.writeHead(200, { "Content-Type": "text/html" });
      res.end(
        '<a id="download" href="' + route + '" download>Download patch</a>',
      );
    });
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
    const origin = "http://127.0.0.1:" + server.address().port;
    const partition = "download-contract-" + Date.now();
    const isolated = session.fromPartition(partition, { cache: false });
    const downloads = createDownloadGuard(
      origin,
      () => win?.webContents.id,
      (entry) => console.log(JSON.stringify(entry)),
    );
    await isolated.cookies.set({
      url: origin,
      name: "foundry_session",
      value: "fixture-cookie",
      httpOnly: true,
    });
    isolated.setPermissionRequestHandler((_c, _p, callback) => callback(false));
    isolated.setPermissionCheckHandler(() => false);
    isolated.webRequest.onBeforeSendHeaders((details, callback) => {
      const request = {
        method: details.method,
        webContentsId: details.webContentsId,
        authenticated: Object.entries(details.requestHeaders).some(
          ([name, value]) =>
            name.toLowerCase() === "cookie" &&
            /(?:^|;\s*)foundry_session=/.test(String(value)),
        ),
      };
      if (details.url === origin + route) {
        console.log(
          JSON.stringify({
            event: "request",
            methodGET: request.method === "GET",
            hasCookie: request.authenticated,
            sourceMatches: request.webContentsId === win.webContents.id,
            initiatorMatches: details.initiatorOrigin === origin,
          }),
        );
        downloads.observeRequest(details);
      }
      callback(
        requestDecision(
          origin,
          "fixture-capability",
          details.url,
          details.requestHeaders,
        ),
      );
    });
    isolated.webRequest.onHeadersReceived((details, callback) => {
      downloads.observeResponse(details);
      callback({ cancel: false });
    });
    isolated.on("will-download", (event, item, contents) => {
      event.preventDefault();
      const name = downloads.saveName(item, contents?.id);
      console.log(
        JSON.stringify({
          event: "download",
          chainLength: item.getURLChain().length,
          sourceMatches: contents?.id === win.webContents.id,
          initiatorMatches: item.getInitiatorOrigin() === origin,
          initiatorTrailingSlash: item.getInitiatorOrigin() === origin + "/",
          allowed: !!name,
        }),
      );
      try {
        assert.equal(name, expectedName);
        console.log("NATIVE_DOWNLOAD_CONTRACT_PASS " + kind);
        finish();
      } catch (error) {
        finish(error);
      }
    });
    win = new BrowserWindow(windowOptions(partition));
    timer = setTimeout(
      () => finish(Error("native download did not complete event contract")),
      10000,
    );
    await win.loadURL(origin);
    await win.webContents.executeJavaScript(
      'document.getElementById("download").click()',
    );
  })
  .catch(finish);
