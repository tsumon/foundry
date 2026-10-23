"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { codingWorkbenchReadiness } = require("../windows-ui-ready.cjs");
const { modeActionReceipt } = require("../windows-ui-mode.cjs");

function renderedUnavailableCodingWorkbench() {
  const node = (parent, type, name, enabled = true) => ({
    parent,
    type,
    name,
    visible: true,
    enabled,
  });
  return {
    window: "5678",
    processId: 42,
    nodes: [
      node(-1, "Window", "Foundry — Controlled Coding Workbench"),
      node(0, "Document", "FOUNDRY"),
      node(1, "Group", "工作台状态"),
      node(2, "Text", "已收到"),
      node(2, "Text", "事件流已连接"),
      node(1, "Group", "账户模型编码设置"),
      node(5, "Text", "账户模型编码 · 受控 Docker"),
      node(
        5,
        "Text",
        "账户模型编码不可用：windows_docker_trust_unavailable。配置记录不是授权或成功证据。",
      ),
      node(5, "Text", "不可用 · openai"),
      node(
        5,
        "CheckBox",
        "启用账户模型与受控 Docker（仅创建配置，不批准或执行）",
        false,
      ),
      node(5, "Button", "刷新编码能力", false),
    ],
  };
}

test("coding UI readiness requires the actual Windows-unavailable rendered boundary", () => {
  const expected = { window: "5678", processId: 42 };
  assert.equal(
    codingWorkbenchReadiness(renderedUnavailableCodingWorkbench(), expected)
      .ready,
    true,
  );
  for (const mutate of [
    (s) => (s.nodes[0].name = "Foundry — Persistent Workbench"),
    (s) => (s.nodes[7].name = "账户模型编码不可用：unknown"),
    (s) => (s.nodes[8].name = "可配置 · openai"),
    (s) => (s.nodes[9].enabled = true),
    (s) => (s.nodes[10].enabled = true),
    (s) => (s.nodes[9].visible = false),
    (s) => (s.nodes[10].visible = false),
    (s) => s.nodes.splice(10, 1),
    (s) => (s.processId = 43),
  ]) {
    const snapshot = renderedUnavailableCodingWorkbench();
    mutate(snapshot);
    assert.equal(codingWorkbenchReadiness(snapshot, expected).ready, false);
  }
});

test("native mode driver receipts admit only fixed coding and persistent actions", () => {
  assert.deepEqual(
    modeActionReceipt(
      {
        action: "coding",
        processId: 42,
        window: "5678",
        menu: true,
        confirmation: true,
        fixedExplanation: true,
        destination: true,
        createForm: true,
        codingSection: true,
      },
      "coding",
      42,
    ),
    { ok: true, action: "coding", window: "5678" },
  );
  assert.deepEqual(
    modeActionReceipt(
      {
        action: "persistent",
        processId: 42,
        window: "1234",
        menu: true,
        confirmation: false,
        fixedExplanation: false,
        destination: true,
        createForm: false,
        codingSection: false,
      },
      "persistent",
      42,
    ),
    { ok: true, action: "persistent", window: "1234" },
  );
  for (const bad of [
    { action: "arbitrary", processId: 42, window: "1" },
    { action: "coding", processId: 43, window: "1" },
    {
      action: "coding",
      processId: 42,
      window: "1",
      menu: true,
      confirmation: true,
      fixedExplanation: false,
      destination: true,
      createForm: true,
    },
    {
      action: "coding",
      processId: 42,
      window: "1",
      menu: true,
      confirmation: true,
      fixedExplanation: true,
      destination: true,
      createForm: true,
    },
  ])
    assert.throws(() => modeActionReceipt(bad, "coding", 42));
});

test("native coding action uses bounded UI Automation scrolling to the fixed section", () => {
  const bytes = fs.readFileSync(path.join(__dirname, "../windows-ui-mode.ps1"));
  const source = bytes.toString("utf8").replace(/^\uFEFF/, "");
  assert.match(source, /ScrollItemPattern/);
  assert.match(source, /ScrollPattern/);
  assert.match(source, /function Coding-SectionAnchorsVisible/);
  assert.match(source, /账户模型编码设置/);
  assert.match(
    source,
    /启用账户模型与受控 Docker（仅创建配置，不批准或执行）/,
  );
  assert.match(source, /刷新编码能力/);
  assert.match(
    source,
    /-not \(Coding-SectionAnchorsVisible \$Element\)/,
  );
  assert.match(source, /-lt 16/);
  assert.doesNotMatch(source, /executeJavaScript|debugger|DevTools/);
});

test("native mode driver stays compatible with the fixed Windows PowerShell 5.1 host", () => {
  const bytes = fs.readFileSync(path.join(__dirname, "../windows-ui-mode.ps1"));
  assert.deepEqual([...bytes.subarray(0, 3)], [0xef, 0xbb, 0xbf]);
  const source = bytes.toString("utf8").replace(/^\uFEFF/, "");
  assert.doesNotMatch(source, /\$IsWindows\b/);
  assert.match(source, /\[Environment\]::OSVersion\.Platform/);
  assert.match(source, /\[ValidateSet\('coding','persistent'\)\]/);
  assert.match(source, /Foundry — Controlled Coding Workbench/);
  assert.match(source, /创建交付任务/);
});

test("native mode gate foregrounds the exact owned window without focusing a non-focusable UIA root", () => {
  const modeBytes = fs.readFileSync(
    path.join(__dirname, "../windows-ui-mode.ps1"),
  );
  const modeSource = modeBytes.toString("utf8").replace(/^\uFEFF/, "");
  assert.doesNotMatch(modeSource, /\.SetFocus\(\)/);

  const smokeSource = fs.readFileSync(
    path.join(__dirname, "../windows-installed-smoke.ps1"),
    "utf8",
  );
  assert.match(
    smokeSource,
    /function Assert-WorkbenchForeground\(\$Process, \[string\]\$Title\)/,
  );
  assert.match(
    smokeSource,
    /\$Process\.MainWindowTitle -cne \$Title[^}]+\$Process\.Responding/,
  );
  assert.match(
    smokeSource,
    /SetForegroundWindow\(\$handle\)[^}]+GetForegroundWindow\(\) -ne \$handle/,
  );
  assert.match(
    smokeSource,
    /\$codingSourceHandle = Assert-WorkbenchForeground \$ui 'Foundry — Persistent Workbench'\r?\n\s*\$codingActionRaw = & \$node[^\r\n]+\$codingSourceHandle\.ToInt64\(\)/,
  );
  assert.match(
    smokeSource,
    /\$persistentSourceHandle = Assert-WorkbenchForeground \$ui 'Foundry — Controlled Coding Workbench'\r?\n\s*\$persistentActionRaw = & \$node[^\r\n]+\$persistentSourceHandle\.ToInt64\(\)/,
  );
});

test("native Foundry menu lookup uses the pinned popup-button role under the owned menu bar", () => {
  const bytes = fs.readFileSync(path.join(__dirname, "../windows-ui-mode.ps1"));
  const source = bytes.toString("utf8").replace(/^\uFEFF/, "");
  assert.match(source, /function Wait-FoundryMenuButton/);
  assert.match(source, /ControlType\]::MenuBar/);
  assert.match(source, /ControlType\]::Button/);
  assert.match(source, /\$current\.Name -ceq 'Foundry'/);
  assert.match(source, /\$current\.IsEnabled/);
  assert.match(source, /-not \$current\.IsOffscreen/);
  assert.match(source, /\$foundry = Wait-FoundryMenuButton \$root 5/);
  assert.doesNotMatch(
    source,
    /Wait-OwnedExact \(\[System\.Windows\.Automation\.ControlType\]::MenuItem\) 'Foundry'/,
  );
  assert.match(source, /if \(\$candidates\.Count -lt 32\)/);
  assert.match(source, /if \(\$name\.Length -gt 80\)/);
  assert.match(
    source,
    /ControlType\]::MenuItem[^}]+ControlType\]::MenuBar[^}]+ControlType\]::Menu/,
  );
  assert.match(source, /Owned menu candidates:/);
  for (const field of [
    "total",
    "menuItems",
    "menuBars",
    "menus",
    "menuBarButtons",
    "fixedFoundryName",
    "enabledFoundryButton",
    "disabledFoundryButton",
    "emitted",
    "truncated",
  ])
    assert.match(source, new RegExp(`${field}=`));
  assert.doesNotMatch(
    source,
    /SendKeys|keybd_event|mouse_event|ClickablePoint|FromPoint|executeJavaScript/,
  );
});

test("native Windows gate runs the focused coding and fake credential suites", () => {
  const source = fs.readFileSync(
    path.join(__dirname, "../windows-test.ps1"),
    "utf8",
  );
  for (const file of [
    "desktop/test/coding-mode.test.cjs",
    "desktop/test/coding-smoke.test.cjs",
    "desktop/test/installed-smoke-module.test.cjs",
    "desktop/test/windows-coding-ui.test.cjs",
  ])
    assert.match(source, new RegExp(file.replaceAll(".", "\\.")));
  assert.match(
    source,
    /\.\/internal\/model[^\r\n]+\^Test\(CredentialManager\|WindowsSecret\)/,
  );
  assert.doesNotMatch(
    source,
    /FOUNDRY_WINDOWS_CREDENTIAL_MANAGER_NATIVE_CI/,
  );
});
