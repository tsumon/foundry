"use strict";

function workbenchReadiness(snapshot, expected) {
  const missing = {
    ready: false,
    document: false,
    navigation: false,
    main: false,
    data: false,
  };
  if (
    !snapshot ||
    snapshot.window !== expected.window ||
    snapshot.processId !== expected.processId ||
    !Array.isArray(snapshot.nodes) ||
    snapshot.nodes.length > 2048
  )
    return missing;
  const nodes = snapshot.nodes;
  for (const [i, n] of nodes.entries()) {
    if (
      !n ||
      !Number.isInteger(n.parent) ||
      (i === 0 ? n.parent !== -1 : n.parent < 0 || n.parent >= i) ||
      typeof n.type !== "string" ||
      n.type.length > 32 ||
      typeof n.name !== "string" ||
      n.name.length > 512 ||
      typeof n.visible !== "boolean" ||
      typeof n.enabled !== "boolean"
    )
      return missing;
  }
  const beneath = (index, parent) => {
    let depth = 0;
    for (let i = index; i >= 0; i = nodes[i].parent) {
      if (++depth > 32 || !nodes[i].visible) return false;
      if (i === parent) return true;
    }
    return false;
  };
  const find = (parent, type, name, excluded = -1) =>
    nodes.findIndex(
      (n, i) =>
        n.type === type &&
        (name === undefined || n.name === name) &&
        n.enabled &&
        beneath(i, parent) &&
        (excluded < 0 || !beneath(i, excluded)),
    );
  const document = find(0, "Document");
  if (document < 0) return missing;
  const nav = find(document, "Group", "主导航");
  const status = find(document, "Group", "工作台状态");
  const navigation =
    nav >= 0 &&
    find(nav, "Button", "交付总览") >= 0 &&
    find(nav, "Button", "设置") >= 0;
  const main =
    find(document, "Text", "交付总览", nav) >= 0 &&
    find(document, "Button", "创建交付任务", nav) >= 0;
  const data =
    status >= 0 &&
    find(status, "Text", "已收到") >= 0 &&
    find(status, "Text", "事件流已连接") >= 0;
  return {
    ready: navigation && main && data,
    document: true,
    navigation,
    main,
    data,
  };
}

module.exports = { workbenchReadiness };

// Test driver only: inspect one installed window, without a debug port,
// renderer injection, activation switches or application security changes.
if (require.main === module) {
  const { execFileSync } = require("node:child_process");
  const path = require("node:path");
  const [window, pid] = process.argv.slice(2);
  if (
    process.platform !== "win32" ||
    !/^[1-9][0-9]{0,18}$/.test(window || "") ||
    !/^[1-9][0-9]{0,9}$/.test(pid || "")
  )
    process.exit(2);
  let receipt;
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
        path.join(__dirname, "windows-ui-snapshot.ps1"),
        "-WindowHandle",
        window,
        "-OwnerProcess",
        pid,
      ],
      {
        encoding: "utf8",
        timeout: 5000,
        maxBuffer: 2 * 1024 * 1024,
        windowsHide: true,
      },
    );
    receipt = workbenchReadiness(JSON.parse(output.replace(/^\uFEFF/, "")), {
      window,
      processId: Number(pid),
    });
  } catch {
    // UIA can report an element disappearing during initial layout. Retry is
    // owned by the outer finite readiness deadline; never expose raw UI text.
    receipt = { ready: false, snapshotAvailable: false };
  }
  process.stdout.write(JSON.stringify(receipt));
}
