"use strict";

function modeActionReceipt(value, action, processId) {
  if (
    !value ||
    !["coding", "persistent"].includes(action) ||
    value.action !== action ||
    value.processId !== processId ||
    !/^[1-9][0-9]{0,18}$/.test(value.window || "") ||
    value.menu !== true ||
    value.destination !== true ||
    (action === "coding" &&
      (value.confirmation !== true ||
        value.fixedExplanation !== true ||
        value.createForm !== true ||
        value.codingSection !== true)) ||
    (action === "persistent" &&
      (value.confirmation !== false ||
        value.fixedExplanation !== false ||
        value.createForm !== false ||
        value.codingSection !== false))
  )
    throw Error("Native mode action was not confirmed");
  return { ok: true, action, window: value.window };
}

module.exports = { modeActionReceipt };

if (require.main === module) {
  const { execFileSync } = require("node:child_process");
  const path = require("node:path");
  const [window, pid, action] = process.argv.slice(2);
  if (
    process.platform !== "win32" ||
    !/^[1-9][0-9]{0,18}$/.test(window || "") ||
    !/^[1-9][0-9]{0,9}$/.test(pid || "") ||
    !["coding", "persistent"].includes(action)
  )
    process.exit(2);
  try {
    const output = execFileSync(
      path.join(
        process.env.SystemRoot,
        "System32",
        "WindowsPowerShell",
        "v1.0",
        "powershell.exe",
      ),
      [
        "-NoProfile",
        "-NonInteractive",
        "-File",
        path.join(__dirname, "windows-ui-mode.ps1"),
        "-WindowHandle",
        window,
        "-OwnerProcess",
        pid,
        "-Action",
        action,
      ],
      {
        encoding: "utf8",
        timeout: 120000,
        maxBuffer: 64 * 1024,
        windowsHide: true,
      },
    );
    process.stdout.write(
      JSON.stringify(
        modeActionReceipt(
          JSON.parse(output.replace(/^\uFEFF/, "")),
          action,
          Number(pid),
        ),
      ),
    );
  } catch {
    process.stdout.write(
      JSON.stringify({ ok: false, action, snapshotAvailable: false }),
    );
    process.exitCode = 1;
  }
}
