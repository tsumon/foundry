"use strict";
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
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
        files[name] = crypto
          .createHash("sha256")
          .update(fs.readFileSync(path.join(root, name)))
          .digest("hex");
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
module.exports = { validateOutput, writeManifest };
