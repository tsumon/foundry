const { test } = require("node:test");
const assert = require("node:assert/strict");
const {
  allowNavigation,
  allowExternal,
  windowOptions,
  activationSwitches,
  artifactSaveName,
} = require("../security.cjs");
test("only owned authenticated GET patch/report downloads reach a native save dialog", () => {
  const origin = "http://127.0.0.1:12345";
  const patch = origin + "/api/missions/0123456789abcdef0123456789abcdef/patch";
  const request = { method: "GET", webContentsId: 7, authenticated: true };
  assert.equal(
    artifactSaveName(origin, request, [patch], origin, 7, 7),
    "changes.patch",
  );
  assert.equal(
    artifactSaveName(
      origin,
      request,
      [patch.replace(/patch$/, "report")],
      origin,
      7,
      7,
    ),
    "report.md",
  );
  for (const [r, chain, initiator, source, owner] of [
    [{ ...request, method: "POST" }, [patch], origin, 7, 7],
    [{ ...request, authenticated: false }, [patch], origin, 7, 7],
    [request, [patch, patch], origin, 7, 7],
    [request, [patch + "?x=y"], origin, 7, 7],
    [
      request,
      [patch.replace("0123456789abcdef0123456789abcdef", "../other")],
      origin,
      7,
      7,
    ],
    [request, [patch.replace("127.0.0.1", "evil.test")], origin, 7, 7],
    [request, ["blob:" + patch], origin, 7, 7],
    [request, [patch], "https://evil.test", 7, 7],
    [request, [patch], origin, 8, 7],
    [request, [patch], origin, 7, 8],
    [undefined, [patch], origin, 7, 7],
  ])
    assert.equal(
      artifactSaveName(origin, r, chain, initiator, source, owner),
      null,
    );
});
test("activation flags cannot configure browser debugging, proxy or sandbox", () => {
  assert.deepEqual(
    activationSwitches([
      "/Applications/Foundry.app",
      "--remote-debugging-port=9222",
      "--no-sandbox",
      "--proxy-server",
      "evil",
      "--openai-inference-route=x",
    ]),
    [
      "remote-debugging-port",
      "no-sandbox",
      "proxy-server",
      "openai-inference-route",
    ],
  );
});
test("navigation denies every scheme and origin outside current controller", () => {
  const origin = "http://127.0.0.1:12345";
  assert.equal(allowNavigation(origin, origin + "/missions"), true);
  for (const url of [
    "file:///etc/passwd",
    "javascript:alert(1)",
    "https://example.com",
    "http://127.0.0.1:12346",
    origin + "@evil.test",
  ])
    assert.equal(allowNavigation(origin, url), false);
});
test("single-dash Chromium switches are stripped on macOS", () => {
  assert.deepEqual(
    activationSwitches(
      [
        "/Applications/Foundry.app",
        "-remote-debugging-port=9222",
        "-proxy-server=http://evil.test",
        "--no-sandbox",
        "/not-a-posix-switch",
      ],
      "darwin",
    ),
    ["remote-debugging-port", "proxy-server", "no-sandbox"],
  );
});
test("Windows slash switches are stripped and normalized like Chromium", () => {
  assert.deepEqual(
    activationSwitches(
      [
        "C:\\Foundry.exe",
        "/REMOTE-DEBUGGING-PORT=9222",
        "-proxy-server=x",
        "--no-sandbox",
      ],
      "win32",
    ),
    ["remote-debugging-port", "proxy-server", "no-sandbox"],
  );
});
test("external destinations are an exact documentation allowlist", () => {
  assert.equal(
    allowExternal("https://www.electronjs.org/docs/latest/tutorial/security"),
    true,
  );
  for (const url of [
    "https://evil.test",
    "https://www.electronjs.org.evil.test/docs",
    "https://www.electronjs.org/docs/latest/tutorial/security?token=x",
    "file:///tmp/a",
  ])
    assert.equal(allowExternal(url), false);
});
test("renderer cannot use Node, preload, persistent storage or webviews", () => {
  const options = windowOptions("launch-1");
  assert.equal(options.webPreferences.sandbox, true);
  assert.equal(options.webPreferences.contextIsolation, true);
  assert.equal(options.webPreferences.nodeIntegration, false);
  assert.equal(options.webPreferences.webviewTag, false);
  assert.equal(options.webPreferences.preload, undefined);
  assert.equal(options.webPreferences.partition.startsWith("persist:"), false);
});
