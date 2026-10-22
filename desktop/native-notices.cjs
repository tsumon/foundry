"use strict";
const fs = require("node:fs"),
  path = require("node:path"),
  crypto = require("node:crypto");
// Retain every supplied copyright/license text, not just a top-level SPDX tag.
// Binary inventory records the complete available toolset, not a claim that
// every available plugin is embedded in this installer.
function copyNativeNotices(bundle, destination) {
  const notices = [],
    binaries = [];
  function walk(dir) {
    for (const entry of fs.readdirSync(path.join(bundle, dir), {
      withFileTypes: true,
    })) {
      const name = dir ? dir + "/" + entry.name : entry.name,
        file = path.join(bundle, name);
      if (entry.isSymbolicLink()) throw Error("Native toolset symlink refused");
      if (entry.isDirectory()) {
        walk(name);
        continue;
      }
      if (!entry.isFile()) throw Error("Nonregular toolset input");
      if (
        /^(copying|copyright|licen[sc]e|notice)([._-]|$)/i.test(entry.name) ||
        /(^|\/)(licenses?|legal)\//i.test(name)
      ) {
        const target = path.join(destination, name);
        fs.mkdirSync(path.dirname(target), { recursive: true });
        fs.copyFileSync(file, target);
        notices.push(name);
      }
      if (/\.(dll|exe)$/i.test(name))
        binaries.push({
          file: name,
          sha256: crypto
            .createHash("sha256")
            .update(fs.readFileSync(file))
            .digest("hex"),
        });
    }
  }
  walk("");
  if (!notices.length)
    throw Error("Native bundle supplied no license/notice texts");
  return {
    notices: notices.sort(),
    binaries: binaries.sort((a, b) => a.file.localeCompare(b.file)),
  };
}
module.exports = { copyNativeNotices };
