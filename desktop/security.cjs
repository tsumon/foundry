"use strict";
const documentation = new Set([
  "https://www.electronjs.org/docs/latest/tutorial/security",
]);
function activationSwitches(args, platform = process.platform) {
  const prefix = platform === "win32" ? /^(?:--?|\/)/ : /^--?/;
  return args
    .slice(1)
    .filter((arg) => prefix.test(arg) && arg !== "--")
    .map((arg) => arg.replace(prefix, "").split("=")[0].toLowerCase())
    .filter((name) => name && !name.startsWith("-"));
}
function allowNavigation(origin, target) {
  try {
    const url = new URL(target);
    return url.origin === origin && !url.username && !url.password;
  } catch {
    return false;
  }
}
function allowExternal(target) {
  return documentation.has(target);
}
function artifactSaveName(origin, request, chain, initiator, source, owner) {
  if (
    !request ||
    request.method !== "GET" ||
    !request.authenticated ||
    !owner ||
    source !== owner ||
    request.webContentsId !== owner ||
    initiator !== origin ||
    chain.length !== 1
  )
    return null;
  return artifactRouteName(origin, chain[0]);
}
function artifactRouteName(origin, target) {
  try {
    const url = new URL(target);
    if (!allowNavigation(origin, target) || url.search || url.hash) return null;
    const match = /^\/api\/missions\/[a-f0-9]{32}\/(patch|report)$/.exec(
      url.pathname,
    );
    return match
      ? match[1] === "patch"
        ? "changes.patch"
        : "report.md"
      : null;
  } catch {
    return null;
  }
}
function windowOptions(partition) {
  return {
    width: 1440,
    height: 960,
    minWidth: 980,
    minHeight: 640,
    show: false,
    title: "Foundry",
    webPreferences: {
      partition,
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
      webviewTag: false,
      devTools: false,
      webSecurity: true,
      allowRunningInsecureContent: false,
    },
  };
}
module.exports = {
  allowNavigation,
  allowExternal,
  windowOptions,
  activationSwitches,
  artifactSaveName,
  artifactRouteName,
};
