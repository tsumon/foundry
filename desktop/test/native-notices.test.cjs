"use strict";
const { test } = require("node:test"),
  assert = require("node:assert/strict"),
  fs = require("node:fs"),
  path = require("node:path"),
  os = require("node:os");
const { copyNativeNotices } = require("../native-notices.cjs");
test("native bundle legal collector preserves nested complete texts and inventories binary hashes", (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-notice-test-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const bundle = path.join(root, "bundle"),
    out = path.join(root, "out");
  fs.mkdirSync(path.join(bundle, "plugin"), { recursive: true });
  fs.writeFileSync(path.join(bundle, "COPYING"), "full core notice\n");
  fs.writeFileSync(
    path.join(bundle, "plugin", "LICENSE.txt"),
    "full plugin license\n",
  );
  fs.writeFileSync(path.join(bundle, "plugin", "plugin.dll"), "binary");
  const result = copyNativeNotices(bundle, out);
  assert.equal(
    fs.readFileSync(path.join(out, "plugin", "LICENSE.txt"), "utf8"),
    "full plugin license\n",
  );
  assert.equal(result.notices.length, 2);
  assert.equal(result.binaries.length, 1);
  assert.match(result.binaries[0].sha256, /^[a-f0-9]{64}$/);
  fs.rmSync(path.join(bundle, "COPYING"));
  fs.rmSync(path.join(bundle, "plugin", "LICENSE.txt"));
  assert.throws(
    () => copyNativeNotices(bundle, path.join(root, "empty")),
    /license|notice/i,
  );
});
