"use strict";
const fs = require("node:fs");
const path = require("node:path");
const { verifySandboxBundle, resourceDigest } = require("./runtime.cjs");
function copySandboxBundle(bundle, target, host) {
  const manifest = verifySandboxBundle(bundle, host);
  const originalDigest = resourceDigest(path.join(bundle, "manifest.json"));
  if (!path.isAbsolute(target) || fs.existsSync(target))
    throw Error("Fresh absolute sandbox output required");
  const relative = path.relative(bundle, target);
  if (
    !relative ||
    (!relative.startsWith(".." + path.sep) &&
      relative !== ".." &&
      !path.isAbsolute(relative))
  )
    throw Error("Sandbox output cannot be inside input");
  fs.mkdirSync(path.dirname(target), { recursive: true });
  fs.cpSync(bundle, target, {
    recursive: true,
    errorOnExist: true,
    force: false,
  });
  verifySandboxBundle(target, host);
  if (
    resourceDigest(path.join(bundle, "manifest.json")) !== originalDigest ||
    resourceDigest(path.join(target, "manifest.json")) !== originalDigest
  )
    throw Error("Sandbox changed during copy");
  return manifest;
}
function verifyMacSandboxSignature(
  bundle,
  execute = require("node:child_process").execFileSync,
) {
  const helper = path.join(bundle, "runtime/foundry-vm-helper");
  execute("/usr/bin/codesign", ["--verify", "--strict", helper], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  const entitlements = execute(
    "/usr/bin/codesign",
    ["-d", "--entitlements", ":-", helper],
    { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
  );
  if (
    !/<key>com\.apple\.security\.virtualization<\/key>\s*<true\s*\/>/.test(
      entitlements,
    )
  )
    throw Error("Missing sandbox virtualization entitlement");
}
function macSigningTargets(app) {
  const signs = [],
    sandbox = path.join(app, "Contents/Resources/sandbox");
  function collect(dir) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const file = path.join(dir, entry.name);
      // Bundle helper was signed with its virtualization entitlement before
      // hashing. Neither it nor bundled native dependencies may be re-signed.
      if (file === sandbox) continue;
      if (entry.isDirectory()) {
        collect(file);
        if (/\.(app|framework)$/.test(entry.name)) signs.push(file);
      } else if (entry.isFile() && /\.(dylib|so)$/.test(entry.name))
        signs.push(file);
    }
  }
  collect(app);
  return [...signs, app];
}
function validateOutput(output, source) {
  if (!output || !path.isAbsolute(output))
    throw Error(
      "FOUNDRY_DESKTOP_OUTPUT must be a new absolute directory outside source",
    );
  const result = path.resolve(output);
  const relative = path.relative(source, result);
  if (
    !relative ||
    (!relative.startsWith(".." + path.sep) &&
      relative !== ".." &&
      !path.isAbsolute(relative)) ||
    fs.existsSync(result)
  )
    throw Error("Build output must be new and outside source");
  return result;
}
function writeManifest(root) {
  const files = {};
  function visit(dir) {
    for (const entry of fs
      .readdirSync(path.join(root, dir), { withFileTypes: true })
      .sort((a, b) => a.name.localeCompare(b.name))) {
      const name = dir ? dir + "/" + entry.name : entry.name;
      if (entry.isDirectory()) visit(name);
      else if (entry.isFile())
        files[name] = resourceDigest(path.join(root, name));
      else throw Error("Non-regular build input");
    }
  }
  visit("");
  fs.writeFileSync(
    path.join(root, "resources-manifest.json"),
    JSON.stringify({ schema: 1, files }, null, 2) + "\n",
  );
  return files;
}
module.exports = {
  validateOutput,
  writeManifest,
  copySandboxBundle,
  verifyMacSandboxSignature,
  macSigningTargets,
};
