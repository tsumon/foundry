// Native browser contract; synthetic HTML only, no provider or VM execution.
const { app, BrowserWindow, session } = require("electron");
const http = require("node:http");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { windowOptions, allowFrameRequest } = require("../security.cjs");
const { requestDecision } = require("../runtime.cjs");
const data = fs.mkdtempSync(
  path.join(os.tmpdir(), "foundry-preview-contract-"),
);
app.setPath("userData", data);
let win, server, timer;
function finish(error) {
  clearTimeout(timer);
  if (win && !win.isDestroyed()) win.destroy();
  server?.close();
  if (error) console.error(error.stack);
  fs.rmSync(data, { recursive: true, force: true });
  app.exit(error ? 1 : 0);
}
app
  .whenReady()
  .then(async () => {
    const route =
      "/api/missions/0123456789abcdef0123456789abcdef/app-preview/frame?digest=" +
      "a".repeat(64);
    let forbidden = 0,
      frames = 0;
    server = http.createServer((req, res) => {
      if (req.url === route) {
        frames++;
        assert.equal(req.headers["x-foundry-desktop"], "fixture-capability");
        res.writeHead(200, {
          "Content-Type": "text/html; charset=utf-8",
          "Content-Security-Policy":
            "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-src 'none'; frame-ancestors 'self'; sandbox allow-scripts",
          "X-Content-Type-Options": "nosniff",
        });
        res.end(`<!doctype html><h1 id="output">Loading</h1><script>
        (async () => {
          let parentBlocked = false, storageBlocked = false, networkBlocked = false;
          try { parent.document.body.innerHTML = 'escaped'; } catch { parentBlocked = true; }
          try { localStorage.setItem('fixture','bad'); } catch { storageBlocked = true; }
          try { await fetch('/api/profiles'); } catch { networkBlocked = true; }
          document.querySelector('#output').textContent = 'App is running';
          parent.postMessage({kind:'preview-contract', rendered:document.querySelector('#output').textContent, parentBlocked,storageBlocked,networkBlocked}, '*');
        })();
      </script>`);
        return;
      }
      if (req.url.startsWith("/api/")) forbidden++;
      res.writeHead(200, {
        "Content-Type": "text/html",
        "Content-Security-Policy":
          "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; object-src 'none'",
      });
      res.end(
        "<!doctype html><title>Preview contract</title><main>Workbench</main>",
      );
    });
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
    const origin = "http://127.0.0.1:" + server.address().port;
    const partition = "preview-contract-" + Date.now();
    const isolated = session.fromPartition(partition, { cache: false });
    isolated.setPermissionRequestHandler((_w, _p, callback) => callback(false));
    isolated.webRequest.onBeforeRequest((details, callback) =>
      callback({ cancel: !allowFrameRequest(origin, details) }),
    );
    isolated.webRequest.onBeforeSendHeaders((details, callback) =>
      callback(
        allowFrameRequest(origin, details)
          ? requestDecision(
              origin,
              "fixture-capability",
              details.url,
              details.requestHeaders,
            )
          : { cancel: true },
      ),
    );
    win = new BrowserWindow(windowOptions(partition));
    timer = setTimeout(
      () => finish(Error("preview contract timed out")),
      10000,
    );
    await win.loadURL(origin);
    const result = await win.webContents
      .executeJavaScript(`new Promise((resolve) => {
    const frame = document.createElement('iframe'); frame.setAttribute('sandbox','allow-scripts');
    window.addEventListener('message', event => { if(event.source === frame.contentWindow && event.data?.kind === 'preview-contract') resolve(event.data); });
    frame.src = ${JSON.stringify(origin + route)}; document.body.append(frame);
  })`);
    assert.deepEqual(result, {
      kind: "preview-contract",
      rendered: "App is running",
      parentBlocked: true,
      storageBlocked: true,
      networkBlocked: true,
    });
    assert.equal(frames, 1);
    assert.equal(forbidden, 0);
    console.log(
      "NATIVE_PREVIEW_CONTRACT_PASS: inline app runs; parent, storage and network blocked",
    );
    finish();
  })
  .catch(finish);
