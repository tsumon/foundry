"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const { createOpenAIHandoff } = require("../openai-handoff.cjs");
test("only current Mac coding session can launch once after host verification", async () => {
  let verified = 0,
    opened = 0,
    current = true,
    allowed = true;
  const opts = {
    platform: "darwin",
    mode: "coding",
    isCurrent: () => current,
    verify: async () => {
      verified++;
      return allowed;
    },
    open: async () => {
      opened++;
    },
  };
  const handoff = createOpenAIHandoff(opts);
  for (const url of [
    "http://localhost:1234/auth/start",
    "http://127.0.0.1:1234/",
    "http://127.0.0.1:1234/auth/start?state=x",
    "https://example.com",
    "http://127.0.0.1:01234/auth/start",
  ])
    assert.equal(await handoff(url), false);
  assert.equal(verified, 0);
  const link = "http://127.0.0.1:1234/auth/start";
  allowed = false;
  assert.equal(await handoff(link), false);
  assert.equal(opened, 0);
  allowed = true;
  assert.equal(await handoff(link), true);
  assert.equal(await handoff(link), false);
  assert.equal(opened, 1);
  current = false;
  assert.equal(await handoff("http://127.0.0.1:4321/auth/start"), false);
  for (const patch of [
    { platform: "linux" },
    { mode: "normal" },
    { mode: "simulation" },
  ])
    assert.equal(
      await createOpenAIHandoff({ ...opts, ...patch, isCurrent: () => true })(
        link,
      ),
      false,
    );
  assert.equal(opened, 1);
});
test("Windows coding handoff requires independent verification and launches only once", async () => {
  let allowed = false,
    opened = 0;
  const handoff = createOpenAIHandoff({
    platform: "win32",
    mode: "coding",
    isCurrent: () => true,
    verify: async () => allowed,
    open: async () => opened++,
  });
  const link = "http://127.0.0.1:2345/auth/start";
  assert.equal(await handoff(link), false);
  assert.equal(opened, 0);
  allowed = true;
  assert.equal(await handoff(link), true);
  assert.equal(await handoff(link), false);
  assert.equal(opened, 1);
});
test("concurrent clicks, backend changes and verification failures never replay", async () => {
  const link = "http://127.0.0.1:2345/auth/start";
  let opened = 0,
    resolve,
    current = true;
  const handoff = createOpenAIHandoff({
    platform: "darwin",
    mode: "coding",
    isCurrent: () => current,
    verify: () => new Promise((r) => (resolve = r)),
    open: async () => opened++,
  });
  const first = handoff(link);
  assert.equal(await handoff(link), false);
  current = false;
  resolve(true);
  assert.equal(await first, false);
  assert.equal(opened, 0);
  const failed = createOpenAIHandoff({
    platform: "darwin",
    mode: "coding",
    isCurrent: () => true,
    verify: async () => true,
    open: async () => {
      opened++;
      throw Error("unknown");
    },
  });
  assert.equal(await failed(link), false);
  assert.equal(await failed(link), false);
  assert.equal(opened, 1);
});
