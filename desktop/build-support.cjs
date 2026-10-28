"use strict";
const fs = require("node:fs");
const path = require("node:path");
const { verifySandboxBundle, verifySandboxBundleV2, strictJSON, resourceDigest } = require("./runtime.cjs");
function copySandboxBundle(bundle, target, host, schema = 1) {
  if (![1, 2].includes(schema)) throw Error("Unknown sandbox schema");
  const verify = schema === 2 ? verifySandboxBundleV2 : verifySandboxBundle;
  const manifest = verify(bundle, host);
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
  verify(target, host);
  if (
    resourceDigest(path.join(bundle, "manifest.json")) !== originalDigest ||
    resourceDigest(path.join(target, "manifest.json")) !== originalDigest
  )
    throw Error("Sandbox changed during copy");
  return manifest;
}
function verifySourceSidecar(bundle, sources, sidecar) {
  if (!["sandbox-sources", "sandbox-v2-sources"].includes(sidecar) || !path.isAbsolute(sources) || fs.realpathSync(sources) !== sources || !fs.lstatSync(sources).isDirectory()) throw Error("Canonical source sidecar required");
  const index = strictJSON(fs.readFileSync(path.join(bundle, "legal/sources.json"), "utf8"));
  if (!index || Object.keys(index).sort().join() !== "files,schema_version,sidecar" || index.schema_version !== 1 || index.sidecar !== sidecar || !Array.isArray(index.files) || index.files.length < 1 || index.files.length > 20000) throw Error("Invalid source sidecar inventory");
  const expected = new Map(), folded = new Set(), directories = new Set();
  for (const row of index.files) {
    if (!row || Object.keys(row).sort().join() !== "path,sha256,size" || typeof row.path !== "string" || row.path.length > 240 || /[^ -~]|[\\:]/.test(row.path) || row.path.split("/").some(p => !p || p === "." || p === ".." || /[. ]$|^(?:\.git|\.hg|\.svn)$|^\.env|credential|secret|token|^(con|prn|aux|nul|com[0-9]|lpt[0-9])(?:\.|$)/i.test(p)) || folded.has(row.path.toLowerCase()) || !Number.isSafeInteger(row.size) || row.size < 1 || row.size > 512*1024**2 || !/^[a-f0-9]{64}$/.test(row.sha256)) throw Error("Invalid source sidecar file");
    expected.set(row.path, row); folded.add(row.path.toLowerCase());
    for (let parent = path.posix.dirname(row.path); parent !== "."; parent = path.posix.dirname(parent)) directories.add(parent);
  }
  let count = 0;
  function visit(relative = "") {
    for (const name of fs.readdirSync(path.join(sources, relative))) {
      const next = relative ? relative + "/" + name : name, file = path.join(sources, next), info = fs.lstatSync(file);
      if (info.isDirectory() && !info.isSymbolicLink()) {
        if (!directories.has(next)) throw Error("Unlisted source sidecar directory");
        visit(next);
      } else {
        const wanted = expected.get(next);
        if (!wanted || !info.isFile() || info.nlink !== 1 || info.size !== wanted.size || resourceDigest(file) !== wanted.sha256) throw Error("Source sidecar integrity failure: " + next);
        count++;
      }
    }
  }
  visit();
  if (count !== expected.size) throw Error("Missing source sidecar file");
  return true;
}
function copyDesktopSandboxes({v1, v2, v1Sources, v2Sources, inputs, output, host}) {
  verifySandboxBundle(v1, host); verifySandboxBundleV2(v2, host);
  verifySourceSidecar(v1, v1Sources, "sandbox-sources");
  verifySourceSidecar(v2, v2Sources, "sandbox-v2-sources");
  copySandboxBundle(v1, path.join(inputs, "sandbox"), host);
  copySandboxBundle(v2, path.join(inputs, "sandbox-v2"), host, 2);
  for (const [bundle, sources, name] of [[v1,v1Sources,"sandbox-sources"],[v2,v2Sources,"sandbox-v2-sources"]]) {
    const target = path.join(output, name);
    if (fs.existsSync(target)) throw Error("Fresh source sidecar output required");
    fs.cpSync(sources, target, {recursive:true, errorOnExist:true, force:false});
    verifySourceSidecar(bundle, target, name);
  }
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
      if (file === sandbox || file === sandbox + "-v2") continue;
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
  copyDesktopSandboxes,
  verifySourceSidecar,
  verifyMacSandboxSignature,
  macSigningTargets,
};
