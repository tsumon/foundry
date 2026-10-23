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
    vmConfigurable: false,
    independentApprovals: false,
    providerConfigurable: false,
    unchecked: false,
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
    (node) =>
      node.type === "Group" && node.name === "工作台状态" && node.enabled,
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
  if (setup < 0) return { ...missing, document: true, status: statusReady };
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
  const independentApprovals = setupText.includes(
    normalizeWhitespace(
      "元数据可用不等于已授权。创建只冻结仓库、七角色账户/模型/消费类别与此配置；readiness、baseline、context、execute、delivery 仍分别决定。",
    ),
  );
  const providerConfigurable = setupText.includes(
    normalizeWhitespace("可配置 · openai"),
  );
  const unchecked =
    find(
      setup,
      (node) =>
        node.type === "CheckBox" &&
        node.name === "启用账户模型与内置 VM（仅创建配置，不批准或执行）" &&
        node.enabled &&
        node.toggleState === "Off",
    ) >= 0;
  const refresh =
    find(
      setup,
      (node) =>
        node.type === "Button" && node.name === "刷新编码能力" && node.enabled,
    ) >= 0;
  // Diagnostics are not authorization/readiness. They may describe clipped
  // controls but never turn them into visible, enabled or unchecked controls.
  const withinSetup = (index) => {
    for (let depth = 0; index >= 0 && depth < 32; depth += 1) {
      if (index === setup) return true;
      index = nodes[index].parent;
    }
    return false;
  };
  const allTextParts = [];
  const diagnosticStack = [...children[setup]].reverse();
  while (diagnosticStack.length > 0) {
    const index = diagnosticStack.pop();
    if (!withinSetup(index)) continue;
    if (nodes[index].type === "Text") allTextParts.push(nodes[index].name);
    for (let child = children[index].length - 1; child >= 0; child -= 1)
      diagnosticStack.push(children[index][child]);
  }
  const allSetupText = normalizeWhitespace(allTextParts.join(""));
  const optInName = "启用账户模型与内置 VM（仅创建配置，不批准或执行）";
  const matches = nodes.flatMap((node, index) =>
    node.type === "CheckBox" &&
    withinSetup(index) &&
    normalizeWhitespace(node.name) === normalizeWhitespace(optInName)
      ? [index]
      : [],
  );
  let checkbox = { matches: matches.length };
  if (matches.length > 0) {
    const index = matches[0];
    const node = nodes[index];
    const ancestors = [];
    for (
      let parent = node.parent;
      parent >= 0 && ancestors.length < 32;
      parent = nodes[parent].parent
    ) {
      const ancestor = nodes[parent];
      ancestors.push({
        type: [
          "Group",
          "Pane",
          "Document",
          "Window",
          "Custom",
          "Text",
        ].includes(ancestor.type)
          ? ancestor.type
          : "Other",
        visible: ancestor.visible,
        enabled: ancestor.enabled,
      });
      if (parent === setup) break;
    }
    checkbox = {
      matches: matches.length,
      exactName: node.name === optInName,
      visible: node.visible,
      enabled: node.enabled,
      state: ["Off", "On", "Indeterminate", "Unavailable"].includes(
        node.toggleState,
      )
        ? node.toggleState
        : "Unknown",
      visiblePath: beneath(index, setup),
      ancestors,
    };
  }
  return {
    ready:
      statusReady &&
      independentApprovals &&
      providerConfigurable &&
      unchecked &&
      refresh,
    document: true,
    status: statusReady,
    setup: true,
    vmConfigurable: providerConfigurable && unchecked,
    independentApprovals,
    providerConfigurable,
    unchecked,
    diagnostics: {
      providerAnyText: allSetupText.includes(
        normalizeWhitespace("可配置 · openai"),
      ),
      providerVisibleText: providerConfigurable,
      providerUnavailableText: allSetupText.includes(
        normalizeWhitespace("不可用 · openai"),
      ),
      checkbox,
      refresh,
    },
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
    !["persistent", "coding-vm-configurable"].includes(mode)
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
      mode === "coding-vm-configurable"
        ? codingWorkbenchReadiness(snapshot, expected)
        : workbenchReadiness(snapshot, expected);
  } catch {
    // UIA can report an element disappearing during initial layout. Retry is
    // owned by the outer finite readiness deadline; never expose raw UI text.
    receipt = { ready: false, snapshotAvailable: false };
  }
  process.stdout.write(JSON.stringify(receipt));
}
