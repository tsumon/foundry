const { test } = require("node:test");
const assert = require("node:assert/strict");
const { createDownloadGuard } = require("../downloads.cjs");
const origin = "http://127.0.0.1:12345";
const url = origin + "/api/missions/0123456789abcdef0123456789abcdef/patch";
const request = {
  id: 17,
  url,
  method: "GET",
  webContentsId: 7,
  initiatorOrigin: origin,
  requestHeaders: {},
};
const response = {
  ...request,
  statusCode: 200,
  responseHeaders: {
    "Content-Type": ["text/plain; charset=utf-8"],
    "Content-Disposition": ['attachment; filename="changes.patch"'],
  },
};
function item(target = url) {
  return {
    getURL: () => target,
    getURLChain: () => [target],
    getInitiatorOrigin: () => origin,
  };
}
test("delivery guard authenticates a correlated successful response, not hidden cookie headers", () => {
  const guard = createDownloadGuard(origin, () => 7);
  guard.observeRequest(request);
  assert.equal(guard.saveName(item(), 7), null);
  guard.observeResponse(response);
  assert.equal(guard.saveName(item(), 7), "changes.patch");
  assert.equal(guard.saveName(item(), 7), null);
});
test("uncorrelated, unauthenticated, wrong-method, redirected and wrong-type responses never authorize saves", () => {
  for (const [req, res] of [
    [request, { ...response, id: 18 }],
    [request, { ...response, statusCode: 403 }],
    [{ ...request, method: "POST" }, response],
    [{ ...request, initiatorOrigin: "https://evil.test" }, response],
    [request, { ...response, url: origin + "/other" }],
    [
      request,
      {
        ...response,
        responseHeaders: {
          "Content-Type": ["text/html"],
          "Content-Disposition": ["attachment"],
        },
      },
    ],
    [
      request,
      { ...response, responseHeaders: { "Content-Type": ["text/plain"] } },
    ],
  ]) {
    const guard = createDownloadGuard(origin, () => 7);
    guard.observeRequest(req);
    guard.observeResponse(res);
    assert.equal(guard.saveName(item(), 7), null);
  }
});
test("correlated Markdown response authorizes only the fixed report filename", () => {
  const guard = createDownloadGuard(origin, () => 7);
  const target = url.replace(/patch$/, "report");
  guard.observeRequest({ ...request, url: target });
  guard.observeResponse({
    ...response,
    url: target,
    responseHeaders: {
      "content-type": ["text/markdown; charset=utf-8"],
      "content-disposition": ['attachment; filename="ignored.md"'],
    },
  });
  assert.equal(guard.saveName(item(target), 7), "report.md");
});
test("delivery diagnostics are bounded booleans without URL, headers, tokens or paths", () => {
  const records = [];
  const guard = createDownloadGuard(
    origin,
    () => 7,
    (entry) => records.push(entry),
  );
  for (let i = 0; i < 100; i++) {
    guard.observeRequest({ ...request, requestHeaders: { Cookie: "secret" } });
    guard.observeResponse(response);
    guard.saveName(item(), 7);
  }
  assert.ok(records.length > 0 && records.length <= 24);
  assert.ok(!JSON.stringify(records).includes(origin));
  assert.ok(!JSON.stringify(records).includes("secret"));
  for (const record of records)
    for (const [key, value] of Object.entries(record))
      assert.ok(key === "event" || typeof value === "boolean");
});
