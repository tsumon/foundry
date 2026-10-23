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

function codingWorkbenchReadiness(snapshot, expected) {
  const missing = {
    ready: false,
    document: false,
    status: false,
    setup: false,
    unavailable: false,
    unavailableWarning: false,
    providerUnavailable: false,
    closed: false,
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
  if (
    nodes[0]?.type !== "Window" ||
    nodes[0]?.name !== "Foundry — Controlled Coding Workbench"
  )
    return missing;
  const beneath = (index, parent) => {
    let depth = 0;
    for (let i = index; i >= 0; i = nodes[i].parent) {
      if (++depth > 32 || !nodes[i].visible) return false;
      if (i === parent) return true;
    }
    return false;
  };
  const find = (parent, predicate) =>
    nodes.findIndex((node, index) => predicate(node) && beneath(index, parent));
  const document = find(0, (node) => node.type === "Document" && node.enabled);
  if (document < 0) return missing;
  const status = find(
    document,
    (node) => node.type === "Group" && node.name === "工作台状态" && node.enabled,
  );
  const statusReady =
    status >= 0 &&
    find(status, (node) => node.type === "Text" && node.name === "已收到") >=
      0 &&
    find(
      status,
      (node) => node.type === "Text" && node.name === "事件流已连接",
    ) >= 0;
  const setup = find(
    document,
    (node) =>
      node.type === "Group" && node.name === "账户模型编码设置" && node.enabled,
  );
  if (setup < 0)
    return { ...missing, document: true, status: statusReady };
  const children = nodes.map(() => []);
  for (let index = 1; index < nodes.length; index += 1)
    children[nodes[index].parent].push(index);
  const setupTextParts = [];
  const stack = [...children[setup]].reverse();
  while (stack.length > 0) {
    const index = stack.pop();
    const node = nodes[index];
    if (!beneath(index, setup)) continue;
    if (node.type === "Text") setupTextParts.push(node.name);
    for (let child = children[index].length - 1; child >= 0; child -= 1)
      stack.push(children[index][child]);
  }
  const normalizeWhitespace = (value) => value.replace(/\s+/gu, "");
  const setupText = normalizeWhitespace(setupTextParts.join(""));
  const unavailableWarning = setupText.includes(
    normalizeWhitespace(
      "账户模型编码不可用：windows_docker_trust_unavailable。配置记录不是授权或成功证据。",
    ),
  );
  const providerUnavailable = setupText.includes(
    normalizeWhitespace("不可用 · openai"),
  );
  const unavailable = unavailableWarning && providerUnavailable;
  const closed =
    find(
      setup,
      (node) =>
        node.type === "CheckBox" &&
        node.name ===
          "启用账户模型与受控 Docker（仅创建配置，不批准或执行）" &&
        !node.enabled,
    ) >= 0;
  const refresh =
    find(
      setup,
      (node) =>
        node.type === "Button" && node.name === "刷新编码能力" && !node.enabled,
    ) >= 0;
  return {
    ready: statusReady && unavailable && closed && refresh,
    document: true,
    status: statusReady,
    setup: true,
    unavailable,
    unavailableWarning,
    providerUnavailable,
    closed,
  };
}

module.exports = { workbenchReadiness, codingWorkbenchReadiness };

// Test driver only: inspect one installed window, without a debug port,
// renderer injection, activation switches or application security changes.
if (require.main === module) {
  const { execFileSync } = require("node:child_process");
  const path = require("node:path");
  const [window, pid, mode = "persistent"] = process.argv.slice(2);
  if (
    process.platform !== "win32" ||
    !/^[1-9][0-9]{0,18}$/.test(window || "") ||
    !/^[1-9][0-9]{0,9}$/.test(pid || "") ||
    !["persistent", "coding-unavailable"].includes(mode)
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
    const snapshot = JSON.parse(output.replace(/^\uFEFF/, ""));
    const expected = { window, processId: Number(pid) };
    receipt =
      mode === "coding-unavailable"
        ? codingWorkbenchReadiness(snapshot, expected)
        : workbenchReadiness(snapshot, expected);
  } catch {
    // UIA can report an element disappearing during initial layout. Retry is
    // owned by the outer finite readiness deadline; never expose raw UI text.
    receipt = { ready: false, snapshotAvailable: false };
  }
  process.stdout.write(JSON.stringify(receipt));
}
