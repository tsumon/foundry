"use strict";
const { test } = require("node:test"),
  assert = require("node:assert/strict"),
  fs = require("node:fs"),
  os = require("node:os"),
  path = require("node:path");
test("patched build matchers preserve file selection and ASAR unpacked bytes", async (t) => {
  const root = fs.mkdtempSync(
    path.join(os.tmpdir(), "foundry-package-matchers-"),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const src = path.join(root, "app");
  fs.mkdirSync(path.join(src, "lib"), { recursive: true });
  for (const [name, data] of Object.entries({
    "main.cjs": "module.exports=42;",
    "lib/native.node": "inert fixture",
    "lib/.keep": "hidden",
    "lib/skip.tmp": "excluded",
  }))
    fs.writeFileSync(path.join(src, name), data);
  require("app-builder-lib");
  const { FileMatcher } = require("app-builder-lib/out/fileMatcher");
  const filter = new FileMatcher(
    src,
    path.join(root, "out"),
    (value) => value,
    ["**/*", "!**/*.tmp"],
  ).createFilter();
  for (const [name, want] of Object.entries({
    "main.cjs": true,
    "lib/native.node": true,
    "lib/.keep": true,
    "lib/skip.tmp": false,
  }))
    assert.equal(
      filter(path.join(src, name), fs.statSync(path.join(src, name))),
      want,
      name,
    );
  const asar = require("@electron/asar"),
    archive = path.join(root, "app.asar");
  await asar.createPackageWithOptions(src, archive, { unpack: "*.node" });
  assert.equal(
    asar.extractFile(archive, "main.cjs").toString(),
    "module.exports=42;",
  );
  assert.equal(asar.statFile(archive, "lib/native.node").unpacked, true);
  assert.equal(
    fs.readFileSync(archive + ".unpacked/lib/native.node", "utf8"),
    "inert fixture",
  );
  const { FileList } = require("filelist");
  assert.deepEqual(new FileList(path.join(src, "**/*.cjs")).toArray(), [
    path.join(src, "main.cjs"),
  ]);
});

test("NSIS build excludes the unused Squirrel peer without excluding it from the audit lock", () => {
  assert.throws(() => require.resolve("electron-builder-squirrel-windows"), {
    code: "MODULE_NOT_FOUND",
  });
  const lock = require("../package-lock.json");
  assert.ok(
    lock.packages["node_modules/electron-builder-squirrel-windows"].peer,
  );
  assert.ok(lock.packages["node_modules/app-builder-lib"]);
  assert.ok(require("dmg-builder"));
  for (const workflow of ["desktop.yml", "verify.yml"]) {
    const source = fs.readFileSync(
      path.join(__dirname, "../../.github/workflows", workflow),
      "utf8",
    );
    const commands = source.match(/npm ci --prefix desktop[^\n]*/g);
    assert.ok(commands?.length);
    for (const command of commands) assert.ok(command.includes("--omit=peer"));
    assert.doesNotMatch(
      source,
      /npm audit[^\n]*(?:--omit|--audit-level=(?:moderate|high|critical))/,
    );
  }
});
