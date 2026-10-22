"use strict";
const { artifactRouteName, artifactSaveName } = require("./security.cjs");

function createDownloadGuard(origin, owner, diagnose = () => {}) {
  const pending = new Map(),
    ready = new Map();
  let diagnosticBudget = 24;
  const log = (event, fields) => {
    if (diagnosticBudget-- > 0) diagnose({ event, ...fields });
  };
  const remember = (map, key, value) => {
    if (map.size >= 64) map.delete(map.keys().next().value);
    map.set(key, value);
  };
  function observeRequest(details) {
    if (!artifactRouteName(origin, details.url)) return;
    const source = !!owner() && details.webContentsId === owner();
    const initiator = details.initiatorOrigin === origin;
    const get = details.method === "GET";
    log("delivery_request", { get, source, initiator });
    if (get && source && initiator)
      remember(pending, details.id, {
        id: details.id,
        url: details.url,
        method: details.method,
        webContentsId: details.webContentsId,
      });
  }
  function observeResponse(details) {
    const request = pending.get(details.id);
    pending.delete(details.id);
    if (!request) return;
    const name = artifactRouteName(origin, details.url);
    const headers = details.responseHeaders || {};
    const values = (key) =>
      Object.entries(headers)
        .filter(([name]) => name.toLowerCase() === key)
        .flatMap(([, value]) => (Array.isArray(value) ? value : [value]));
    const attachment = values("content-disposition").some((value) =>
      /^attachment(?:;|$)/i.test(value),
    );
    const type = values("content-type").some(
      (value) =>
        value.split(";")[0].trim().toLowerCase() ===
        (name === "changes.patch" ? "text/plain" : "text/markdown"),
    );
    const statusOK = details.statusCode === 200;
    const sameRequest =
      request.url === details.url &&
      details.method === "GET" &&
      request.webContentsId === owner();
    log("delivery_response", { statusOK, sameRequest, attachment, type });
    // Electron does not expose its Cookie header in onBeforeSendHeaders. A 200
    // attachment from this correlated capability-authenticated controller route
    // is the server's session-authentication verdict; 401/403 cannot enter here.
    if (name && statusOK && sameRequest && attachment && type)
      remember(ready, details.url, { ...request, authenticated: true });
  }
  function saveName(item, source) {
    const target = item.getURL();
    const request = ready.get(target);
    ready.delete(target);
    const chain = item.getURLChain(),
      initiator = item.getInitiatorOrigin();
    const name = artifactSaveName(
      origin,
      request,
      chain,
      initiator,
      source,
      owner(),
    );
    if (artifactRouteName(origin, target))
      log("delivery_download", {
        tracked: !!request,
        chain: chain.length === 1,
        source: source === owner(),
        initiator: initiator === origin,
        allowed: !!name,
      });
    return name;
  }
  return { observeRequest, observeResponse, saveName };
}
module.exports = { createDownloadGuard };
