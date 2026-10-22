"use strict";
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const { spawn, execFileSync } = require("node:child_process");
function executableName(name, platform = process.platform) {
  return name + (platform === "win32" ? ".exe" : "");
}

function parseReadiness(line, pid) {
  if (Buffer.byteLength(line) > 8192)
    throw Error("Oversized controller readiness");
  const ready = JSON.parse(line);
  if (
    ready.protocol !== 1 ||
    ready.pid !== pid ||
    typeof ready.dataDir !== "string" ||
    !path.isAbsolute(ready.dataDir)
  )
    throw Error("Invalid controller readiness");
  const url = new URL(ready.url);
  if (
    url.protocol !== "http:" ||
    url.hostname !== "127.0.0.1" ||
    !url.port ||
    Number(url.port) < 1 ||
    url.username ||
    url.password ||
    url.pathname !== "/" ||
    url.search ||
    url.hash ||
    ready.url !== url.origin
  )
    throw Error("Invalid controller origin");
  return {
    protocol: 1,
    url: url.origin,
    pid: ready.pid,
    dataDir: ready.dataDir,
  };
}

function childEnvironment(
  source,
  dataDir,
  webDir,
  platform = process.platform,
) {
  const env = {
    PATH: "/usr/bin:/bin:/usr/sbin:/sbin",
    LANG: "en_US.UTF-8",
    FOUNDRY_DATA_DIR: dataDir,
    FOUNDRY_WEB_DIR: webDir,
    FOUNDRY_ADDR: "127.0.0.1:0",
    FOUNDRY_DOCKER: "/__foundry_desktop_docker_disabled__",
  };
  for (const key of ["HOME", "TMPDIR"]) if (source[key]) env[key] = source[key];
  if (platform === "win32") {
    const system = source.SystemRoot || source.SYSTEMROOT;
    if (!system || !path.win32.isAbsolute(system))
      throw Error("Windows system directory unavailable");
    env.PATH = path.win32.join(system, "System32");
    env.SystemRoot = system;
    env.WINDIR = system;
    for (const key of ["TEMP", "TMP", "LOCALAPPDATA", "USERPROFILE"])
      if (source[key]) env[key] = source[key];
    env.FOUNDRY_DOCKER = "C:\\__foundry_desktop_docker_disabled__.exe";
  }
  return env;
}

function requestDecision(origin, capability, target, headers) {
  try {
    if (new URL(target).origin !== origin) return { cancel: true };
  } catch {
    return { cancel: true };
  }
  const clean = {};
  for (const [key, value] of Object.entries(headers))
    if (key.toLowerCase() !== "x-foundry-desktop") clean[key] = value;
  clean["X-Foundry-Desktop"] = capability;
  return { cancel: false, requestHeaders: clean };
}

function verifyResources(root, expectedManifestHash) {
  requireDirectoryChain(root);
  if (!fs.lstatSync(path.join(root, "resources-manifest.json")).isFile())
    throw Error("Resource manifest must not be a symlink");
  const raw = fs.readFileSync(path.join(root, "resources-manifest.json"));
  if (
    expectedManifestHash &&
    crypto.createHash("sha256").update(raw).digest("hex") !==
      expectedManifestHash
  )
    throw Error("Installed manifest integrity check failed");
  const manifest = JSON.parse(raw);
  for (const name of [
    "bin/" + executableName("foundry"),
    "bin/" + executableName("foundry-acceptance"),
    "web/index.html",
  ])
    if (!manifest.files?.[name]) throw Error("Missing installed resources");
  for (const [name, hash] of Object.entries(manifest.files)) {
    if (
      path.isAbsolute(name) ||
      /[\\:]/.test(name) ||
      name.split("/").some((part) => !part || part === "." || part === "..") ||
      !["bin", "web", "legal"].includes(name.split("/")[0]) ||
      !/^[a-f0-9]{64}$/.test(hash)
    )
      throw Error("Invalid resource manifest");
    const file = path.join(root, name);
    requireDirectoryChain(path.dirname(file));
    const stat = fs.lstatSync(file);
    if (
      !stat.isFile() ||
      crypto
        .createHash("sha256")
        .update(fs.readFileSync(file))
        .digest("hex") !== hash
    )
      throw Error("Installed resource integrity check failed");
  }
  function inspect(directory) {
    for (const name of fs.readdirSync(path.join(root, directory))) {
      const relative = directory + "/" + name;
      const info = fs.lstatSync(path.join(root, relative));
      if (info.isSymbolicLink())
        throw Error("Installed resource symlink rejected");
      if (info.isDirectory()) inspect(relative);
      else if (!info.isFile() || !Object.hasOwn(manifest.files, relative))
        throw Error("Unlisted installed resource rejected");
    }
  }
  for (const directory of ["bin", "web", "legal"]) {
    if (fs.existsSync(path.join(root, directory))) {
      requireDirectoryChain(path.join(root, directory));
      inspect(directory);
    }
  }
}

function requireDirectoryChain(directory) {
  if (!path.isAbsolute(directory)) throw Error("Directory must be absolute");
  let current = path.parse(directory).root;
  for (const part of path
    .relative(current, path.resolve(directory))
    .split(path.sep)
    .filter(Boolean)) {
    current = path.join(current, part);
    const info = fs.lstatSync(current);
    if (info.isSymbolicLink() || !info.isDirectory())
      throw Error("Directory symlink or non-directory rejected");
  }
}

function privateDirectory(directory) {
  if (!path.isAbsolute(directory))
    throw Error("Private directory must be absolute");
  const parent = path.dirname(directory);
  if (!fs.existsSync(parent)) privateDirectory(parent);
  requireDirectoryChain(parent);
  try {
    fs.mkdirSync(directory, { mode: 0o700 });
  } catch (error) {
    if (error.code !== "EEXIST") throw error;
  }
  const info = fs.lstatSync(directory);
  if (info.isSymbolicLink() || !info.isDirectory())
    throw Error("Private directory symlink rejected");
  if (
    process.platform !== "win32" &&
    (info.uid !== process.getuid() || (info.mode & 0o077) !== 0)
  )
    throw Error("Existing data directory is not private and user-owned");
}

async function startBackend({
  resources,
  dataRoot,
  mode = "normal",
  repository = "",
  timeout = 30000,
  integrity,
}) {
  verifyResources(resources, integrity);
  if (!["normal", "simulation"].includes(mode))
    throw Error("Unknown desktop mode");
  if (repository && (mode !== "simulation" || !path.isAbsolute(repository)))
    throw Error("Invalid repository selection");
  if (process.platform === "win32") {
    execFileSync(
      path.join(resources, "bin", "foundry.exe"),
      ["--desktop-prepare-data", dataRoot],
      {
        windowsHide: true,
        env: childEnvironment(
          process.env,
          dataRoot,
          path.join(resources, "web"),
        ),
        stdio: ["ignore", "pipe", "pipe"],
      },
    );
  } else privateDirectory(dataRoot);
  const dataDir = path.join(
    dataRoot,
    mode === "normal" ? "workbench" : "simulations",
  );
  if (process.platform !== "win32") privateDirectory(dataDir);
  const webDir = path.join(resources, "web");
  const args =
    mode === "normal"
      ? ["--desktop"]
      : [
          "--desktop",
          "--repository-tasks-fixture",
          "--web-dir",
          webDir,
          ...(repository ? ["--repository-task-path", repository] : []),
        ];
  const capability = crypto.randomBytes(32).toString("hex");
  const child = spawn(
    path.join(
      resources,
      "bin",
      executableName(mode === "normal" ? "foundry" : "foundry-acceptance"),
    ),
    args,
    {
      cwd: dataRoot,
      env: childEnvironment(process.env, dataDir, webDir),
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    },
  );
  // Never surface raw child output in renderer, dialogs or logs.
  child.stderr.resume();
  child.stdin.on("error", () => {});
  let stopped = false;
  let stopPromise;
  const exited = new Promise((resolve) => {
    child.once("exit", resolve);
    child.once("error", resolve);
  });
  const stop = () =>
    (stopPromise ??= (async () => {
      stopped = true;
      child.stdin.end();
      let timer;
      await Promise.race([
        exited,
        new Promise((resolve) => {
          timer = setTimeout(() => {
            child.kill("SIGTERM");
            resolve();
          }, 5000);
        }),
      ]);
      clearTimeout(timer);
      if (child.exitCode === null && child.signalCode === null) {
        await Promise.race([
          exited,
          new Promise((resolve) => {
            timer = setTimeout(() => {
              child.kill("SIGKILL");
              resolve();
            }, 2000);
          }),
        ]);
        clearTimeout(timer);
        await exited;
      }
    })());
  try {
    const ready = await new Promise((resolve, reject) => {
      let buffer = "";
      const timer = setTimeout(
        () => reject(Error("Controller startup timed out")),
        timeout,
      );
      const fail = () => {
        clearTimeout(timer);
        reject(Error("Controller exited before readiness"));
      };
      child.once("error", fail);
      child.once("exit", fail);
      child.stdout.on("data", (chunk) => {
        buffer += chunk.toString("utf8");
        if (Buffer.byteLength(buffer) > 8192) {
          clearTimeout(timer);
          reject(Error("Oversized controller readiness"));
          return;
        }
        const newline = buffer.indexOf("\n");
        if (newline < 0) return;
        try {
          const ready = parseReadiness(buffer.slice(0, newline), child.pid);
          clearTimeout(timer);
          child.removeListener("exit", fail);
          child.removeListener("error", fail);
          child.stdout.removeAllListeners("data");
          child.stdout.resume();
          resolve(ready);
        } catch (err) {
          clearTimeout(timer);
          reject(err);
        }
      });
      child.stdin.write(capability + "\n");
    });
    return {
      ready,
      capability,
      process: child,
      stop,
      exited,
      isStopping: () => stopped,
    };
  } catch (error) {
    await stop();
    throw error;
  }
}

module.exports = {
  executableName,
  parseReadiness,
  childEnvironment,
  requestDecision,
  verifyResources,
  startBackend,
};
