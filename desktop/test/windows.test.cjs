"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const { childEnvironment, executableName } = require("../runtime.cjs");
test("Windows child environment excludes activation, credentials and PATH injection", () => {
  const env = childEnvironment(
    {
      SystemRoot: "C:\\Windows",
      TEMP: "C:\\Temp",
      LOCALAPPDATA: "C:\\Users\\用户\\AppData\\Local",
      PATH: "C:\\evil",
      FOUNDRY_DOCKER: "evil",
      OPENAI_API_KEY: "secret",
      NODE_OPTIONS: "--inspect",
    },
    "C:\\data",
    "C:\\web",
    "win32",
  );
  assert.equal(env.PATH, "C:\\Windows\\System32");
  assert.equal(env.SystemRoot, "C:\\Windows");
  assert.equal(env.OPENAI_API_KEY, undefined);
  assert.equal(env.NODE_OPTIONS, undefined);
  assert.equal(env.FOUNDRY_DATA_DIR, "C:\\data");
  assert.notEqual(env.FOUNDRY_DOCKER, "evil");
  assert.equal(executableName("foundry", "win32"), "foundry.exe");
  assert.equal(executableName("foundry", "darwin"), "foundry");
});
