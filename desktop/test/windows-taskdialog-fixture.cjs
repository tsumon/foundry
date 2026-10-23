"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { app, dialog } = require("electron");
const { createCodingMenuItem } = require("../coding-mode.cjs");

const resultPath = process.env.FOUNDRY_TASKDIALOG_FIXTURE_RESULT;
if (!resultPath || !path.isAbsolute(resultPath))
  throw Error("Absolute TaskDialog fixture result path required");

app.whenReady().then(async () => {
  const launches = [];
  const item = createCodingMenuItem(dialog, async (mode) => {
    launches.push(mode);
  });
  await item.click();
  fs.writeFileSync(resultPath, JSON.stringify({ launches }) + "\n", {
    flag: "wx",
  });
  app.quit();
}).catch((error) => {
  process.stderr.write(`TaskDialog fixture failed: ${error.message}\n`);
  app.exit(1);
});
