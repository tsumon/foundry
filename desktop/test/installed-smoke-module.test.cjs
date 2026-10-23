"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");
const { execFileSync, spawnSync } = require("node:child_process");

function removeCryptoImport(source) {
  const mutated = source.replace(
    /^const crypto = require\("node:crypto"\);\r?\n/m,
    "",
  );
  assert.notEqual(mutated, source, "crypto import mutation must change source");
  return mutated;
}

test("crypto import mutation is exact across LF and CRLF source", () => {
  for (const newline of ["\n", "\r\n"]) {
    const source = [
      '"use strict";',
      'const crypto = require("node:crypto");',
      'const fs = require("node:fs");',
      "",
    ].join(newline);
    assert.equal(
      removeCryptoImport(source),
      ['"use strict";', 'const fs = require("node:fs");', ""].join(newline),
    );
  }
});

test("installed smoke hashing executes in its real CommonJS module scope", (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-smoke-module-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const input = path.join(root, "evidence.db");
  fs.writeFileSync(input, "installed-smoke-module-scope");
  const modulePath = path.join(__dirname, "../installed-smoke.cjs");
  const runner = path.join(root, "runner.cjs");
  fs.writeFileSync(
    runner,
    'const {hashFile}=require(process.argv[2]);process.stdout.write(hashFile(process.argv[3]));',
  );
  const childEnv =
    process.platform === "win32"
      ? { SystemRoot: process.env.SystemRoot }
      : {};
  const run = (target) =>
    execFileSync(process.execPath, [runner, target, input], {
      encoding: "utf8",
      env: childEnv,
    });
  const output = run(modulePath);
  assert.equal(
    output,
    crypto.createHash("sha256").update(fs.readFileSync(input)).digest("hex"),
  );

  const source = fs.readFileSync(modulePath, "utf8");
  assert.match(source, /const crypto = require\("node:crypto"\);/);
  const withoutImport = removeCryptoImport(source)
    .replace(
      'require("./runtime.cjs")',
      `require(${JSON.stringify(path.join(__dirname, "../runtime.cjs"))})`,
    )
    .replace(
      'require("./installed-flow.cjs")',
      `require(${JSON.stringify(path.join(__dirname, "../installed-flow.cjs"))})`,
    )
    .replace(
      'require("./coding-smoke.cjs")',
      `require(${JSON.stringify(path.join(__dirname, "../coding-smoke.cjs"))})`,
    );
  assert.doesNotMatch(withoutImport, /const crypto = require\("node:crypto"\);/);
  const brokenModule = path.join(root, "installed-smoke-without-crypto.cjs");
  fs.writeFileSync(brokenModule, withoutImport);
  const mutation = spawnSync(process.execPath, [runner, brokenModule, input], {
    encoding: "utf8",
    env: childEnv,
  });
  assert.notEqual(mutation.status, 0);
  assert.match(mutation.stderr, /crypto\.createHash|crypto is not defined/);
});
