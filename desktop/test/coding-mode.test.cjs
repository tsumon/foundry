"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const { createCodingMenuItem } = require("../coding-mode.cjs");

test("controlled coding menu explains configuration-only authority and launches only after native confirmation", async () => {
  const calls = [];
  const dialog = {
    async showMessageBox(options) {
      calls.push(options);
      return { response: 0 };
    },
  };
  const launches = [];
  const item = createCodingMenuItem(dialog, async (...args) => {
    launches.push(args);
  });

  assert.equal(item.label, "Controlled Coding Workbench");
  await item.click();
  assert.equal(calls.length, 1);
  assert.match(calls[0].message, /configuration/i);
  assert.match(calls[0].detail, /no model calls/i);
  assert.match(calls[0].detail, /no Docker commands/i);
  assert.match(calls[0].detail, /separate approvals/i);
  assert.deepEqual(launches, [["coding"]]);
});

test("cancelling controlled coding confirmation has no launch effect", async () => {
  const dialog = {
    async showMessageBox() {
      return { response: 1 };
    },
  };
  let launches = 0;
  const item = createCodingMenuItem(dialog, async () => {
    launches++;
  });
  await item.click();
  assert.equal(launches, 0);
});
