"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { codingWorkbenchReadiness } = require("../windows-ui-ready.cjs");
const { modeActionReceipt } = require("../windows-ui-mode.cjs");
const { createCodingMenuItem } = require("../coding-mode.cjs");

test("the actual coding confirmation satisfies the native Windows explanation policy", async () => {
  const source = fs.readFileSync(
    path.join(__dirname, "../windows-taskdialog-action.ps1"),
    "utf8",
  );
  const block = source.match(
    /function Get-CodingExplanationFragments \{([\s\S]*?)\n\}/,
  );
  assert.ok(block, "Native explanation policy is missing");
  const fragments = [...block[1].matchAll(/'([^']+)'/g)].map((m) => m[1]);
  assert.ok(fragments.length >= 5);
  let shown,
    launches = 0;
  await createCodingMenuItem(
    {
      showMessageBox: async (options) => {
        shown = options;
        return { response: 1 };
      },
    },
    () => launches++,
  ).click();
  assert.equal(launches, 0);
  for (const fragment of fragments)
    assert.ok(
      [shown.message, shown.detail].some((text) => text.includes(fragment)),
      "Native acceptance rejects actual confirmation: " + fragment,
    );
});

function renderedVMConfigurableWorkbench() {
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
      node(5, "Text", "账户模型编码 · 内置 VM"),
      node(
        5,
        "Text",
        "元数据可用不等于已授权。创建只冻结仓库、七角色账户/模型/消费类别与此配置；readiness、baseline、context、execute、delivery 仍分别决定。",
      ),
      node(5, "Text", "可配置 · openai"),
      {
        ...node(
          5,
          "CheckBox",
          "启用账户模型与内置 VM（仅创建配置，不批准或执行）",
        ),
        toggleState: "Off",
      },
      node(5, "Button", "刷新编码能力"),
    ],
  };
}

function splitVMConfigurableWorkbench() {
  const node = (parent, type, name, enabled = true, visible = true) => ({
    parent,
    type,
    name,
    visible,
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
      node(5, "Text", "账户模型编码 · 内置 VM"),
      node(
        5,
        "Text",
        "元数据可用不等于已授权。创建只冻结仓库、七角色账户/模型/消费类别与此配置；",
      ),
      node(5, "Text", "readiness、baseline、context、execute、delivery"),
      node(5, "Text", "仍分别决定。"),
      node(5, "Text", "运行时 / 提供商"),
      node(5, "Text", "可配置"),
      node(5, "Text", " · "),
      node(5, "Text", "openai"),
      {
        ...node(
          5,
          "CheckBox",
          "启用账户模型与内置 VM（仅创建配置，不批准或执行）",
        ),
        toggleState: "Off",
      },
      node(5, "Button", "刷新编码能力"),
    ],
  };
}

test("coding UI readiness requires configurable VM metadata with opt-in still unchecked", () => {
  const expected = { window: "5678", processId: 42 };
  assert.equal(
    codingWorkbenchReadiness(renderedVMConfigurableWorkbench(), expected).ready,
    true,
  );
  for (const mutate of [
    (s) => (s.nodes[0].name = "Foundry — Persistent Workbench"),
    (s) =>
      (s.nodes[7].name = "账户模型编码不可用：embedded_vm_bundle_unavailable"),
    (s) => (s.nodes[8].name = "不可用 · openai"),
    (s) => (s.nodes[9].enabled = false),
    (s) =>
      (s.nodes[9].name =
        "启用账户模型与受控 Docker（仅创建配置，不批准或执行）"),
    (s) => (s.nodes[9].toggleState = "On"),
    (s) => (s.nodes[9].toggleState = "Indeterminate"),
    (s) => delete s.nodes[9].toggleState,
    (s) => (s.nodes[10].enabled = false),
    (s) => (s.nodes[9].visible = false),
    (s) => (s.nodes[10].visible = false),
    (s) => s.nodes.splice(10, 1),
    (s) => (s.processId = 43),
  ]) {
    const snapshot = renderedVMConfigurableWorkbench();
    mutate(snapshot);
    assert.equal(codingWorkbenchReadiness(snapshot, expected).ready, false);
  }
});

test("coding UI readiness reconstructs only exact ordered visible setup text fragments", () => {
  const expected = { window: "5678", processId: 42 };
  assert.equal(
    codingWorkbenchReadiness(splitVMConfigurableWorkbench(), expected).ready,
    true,
  );
  for (const mutate of [
    (s) => s.nodes.splice(8, 1),
    (s) => ([s.nodes[7], s.nodes[8]] = [s.nodes[8], s.nodes[7]]),
    (s) => (s.nodes[8].name = "已授权执行"),
    (s) => (s.nodes[13].name = "anthropic"),
    (s) => (s.nodes[8].visible = false),
    (s) => (s.nodes[8].parent = 1),
  ]) {
    const snapshot = splitVMConfigurableWorkbench();
    mutate(snapshot);
    assert.equal(codingWorkbenchReadiness(snapshot, expected).ready, false);
  }
});

test("diagnostic text preserves tree order for nested UIA fragments", () => {
  const snapshot = renderedVMConfigurableWorkbench();
  snapshot.nodes[8] = { ...snapshot.nodes[8], type: "Group", name: "" };
  snapshot.nodes.push({
    parent: 5,
    type: "Text",
    name: " · openai",
    visible: true,
    enabled: true,
  });
  snapshot.nodes.push({
    parent: 8,
    type: "Text",
    name: "可配置",
    visible: true,
    enabled: true,
  });
  const result = codingWorkbenchReadiness(snapshot, {
    window: "5678",
    processId: 42,
  });
  assert.equal(result.ready, true);
  assert.equal(result.diagnostics.providerVisibleText, true);
  assert.equal(result.diagnostics.providerAnyText, true);
});

test("coding readiness diagnoses hidden wrappers without accepting hidden controls", () => {
  const snapshot = renderedVMConfigurableWorkbench();
  snapshot.nodes.push({
    parent: 5,
    type: "Group",
    name: "private-wrapper-name",
    visible: false,
    enabled: true,
  });
  snapshot.nodes.push({ ...snapshot.nodes[9], parent: 11 });
  snapshot.nodes[9].name = "unrelated-private-checkbox";
  snapshot.nodes[8].visible = false;
  const result = codingWorkbenchReadiness(snapshot, {
    window: "5678",
    processId: 42,
  });
  assert.equal(result.ready, false);
  assert.equal(result.providerConfigurable, false);
  assert.equal(result.unchecked, false);
  assert.ok(result.diagnostics, "Missing bounded diagnostic receipt");
  assert.equal(result.diagnostics.providerAnyText, true);
  assert.equal(result.diagnostics.providerVisibleText, false);
  assert.deepEqual(result.diagnostics.checkbox, {
    matches: 1,
    exactName: true,
    visible: true,
    enabled: true,
    state: "Off",
    visiblePath: false,
    ancestors: [
      { type: "Group", visible: false, enabled: true },
      { type: "Group", visible: true, enabled: true },
    ],
  });
  assert.equal(JSON.stringify(result).includes("private-"), false);
});

test("coding diagnostics distinguish disabled, renamed and unchecked controls without leaking names", () => {
  const expected = { window: "5678", processId: 42 };
  const snapshot = renderedVMConfigurableWorkbench();
  snapshot.nodes[9].enabled = false;
  snapshot.nodes[9].toggleState = "sensitive-invalid-toggle";
  snapshot.nodes[8].name = "不可用 · openai";
  const result = codingWorkbenchReadiness(snapshot, expected);
  assert.equal(result.ready, false);
  assert.ok(result.diagnostics, "Missing bounded diagnostic receipt");
  assert.equal(result.diagnostics.providerUnavailableText, true);
  assert.equal(result.diagnostics.checkbox.enabled, false);
  assert.equal(result.diagnostics.checkbox.state, "Unknown");
  assert.equal(JSON.stringify(result).includes("sensitive"), false);
  snapshot.nodes[9].name = "启用账户模型与内置 VM （仅创建配置，不批准或执行）";
  const spaced = codingWorkbenchReadiness(snapshot, expected);
  assert.equal(spaced.ready, false);
  assert.equal(spaced.diagnostics.checkbox.matches, 1);
  assert.equal(spaced.diagnostics.checkbox.exactName, false);
  snapshot.processId = 43;
  assert.equal(
    codingWorkbenchReadiness(snapshot, expected).diagnostics,
    undefined,
  );
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

test("native coding scrolling targets owned controls and runs actual native regressions early", () => {
  const mode = fs.readFileSync(path.join(__dirname, "../windows-ui-mode.ps1"), "utf8");
  const scroll = fs.readFileSync(path.join(__dirname, "../windows-ui-scroll.ps1"), "utf8");
  const gate = fs.readFileSync(path.join(__dirname, "../windows-test.ps1"), "utf8");
  assert.match(mode, /windows-ui-scroll\.ps1/);
  assert.match(mode, /Scroll-CodingControlsIntoView \$codingSection \$OwnerProcess/);
  assert.doesNotMatch(mode, /function Scroll-IntoViewExact/);
  assert.match(scroll, /Get-CodingScrollDecision/);
  assert.match(scroll, /ScrollItemPattern/);
  assert.match(scroll, /SetScrollPercent/);
  assert.match(scroll, /-lt 16/);
  assert.doesNotMatch(scroll, /executeJavaScript|DevTools|SetFocus|\.Toggle\(|\.Invoke\(/);
  assert.match(gate, /Checked \$windowsPowerShell @\('-NoProfile', '-NonInteractive', '-File', \(Join-Path \$PWD 'desktop\/windows-ui-scroll.test.ps1'\)\)/);
  const native = fs.readFileSync(path.join(__dirname, "../windows-ui-scroll.test.ps1"), "utf8");
  assert.match(native, /Get-CodingScrollDecision/);
  assert.match(native, /windows-scroll-fixture\.cjs/);
  assert.match(native, /windows-ui-ready\.cjs/);
  assert.match(native, /coding-vm-configurable/);
});

test("scrolling does not make clipped provider, approval or controls ready", () => {
  const expected = { window: "5678", processId: 42 };
  for (const index of [7, 8, 9, 10]) {
    const snapshot = renderedVMConfigurableWorkbench();
    snapshot.nodes[index].visible = false;
    assert.equal(codingWorkbenchReadiness(snapshot, expected).ready, false);
  }
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

function runNativeModeDriver({ elapsedMs, action = "coding" }) {
  const source = fs.readFileSync(
    path.join(__dirname, "../windows-ui-mode.cjs"),
    "utf8",
  );
  let stdout = "";
  const module = { exports: {} };
  const requireForDriver = (specifier) => {
    if (specifier === "node:child_process")
      return {
        execFileSync(_file, _args, options) {
          if (elapsedMs > options.timeout) {
            const error = new Error("simulated native driver timeout");
            error.code = "ETIMEDOUT";
            throw error;
          }
          return JSON.stringify({
            action,
            processId: 42,
            window: "5678",
            menu: true,
            confirmation: action === "coding",
            fixedExplanation: action === "coding",
            destination: true,
            createForm: action === "coding",
            codingSection: action === "coding",
          });
        },
      };
    if (specifier === "node:path") return path.win32;
    throw new Error(`unexpected module: ${specifier}`);
  };
  requireForDriver.main = module;
  const processForDriver = {
    argv: ["node", "windows-ui-mode.cjs", "1234", "42", action],
    env: { SystemRoot: "C:\\Windows" },
    platform: "win32",
    stdout: {
      write(value) {
        stdout += value;
      },
    },
    exitCode: 0,
    exit(code) {
      throw new Error(`unexpected process.exit(${code})`);
    },
  };
  vm.runInNewContext(source, {
    __dirname: "C:\\Foundry\\desktop",
    module,
    exports: module.exports,
    require: requireForDriver,
    process: processForDriver,
    JSON,
    Number,
    Error,
  });
  return { output: JSON.parse(stdout), exitCode: processForDriver.exitCode };
}

test("native mode outer deadline admits the bounded coding path and rejects runaway work", () => {
  // Existing bounded menu/loading waits plus sixteen two-second scroll
  // acknowledgements must fit inside the parent process boundary.
  assert.deepEqual(runNativeModeDriver({ elapsedMs: 150_000 }), {
    output: { ok: true, action: "coding", window: "5678" },
    exitCode: 0,
  });
  assert.deepEqual(runNativeModeDriver({ elapsedMs: 161_000 }), {
    output: { ok: false, action: "coding", snapshotAvailable: false },
    exitCode: 1,
  });
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
    "desktop/test/native-vm-smoke.test.cjs",
    "desktop/test/windows-coding-ui.test.cjs",
  ])
    assert.match(source, new RegExp(file.replaceAll(".", "\\.")));
  assert.match(
    source,
    /\.\/internal\/model[^\r\n]+\^Test\(CredentialManager\|WindowsSecret\)/,
  );
  assert.doesNotMatch(source, /FOUNDRY_WINDOWS_CREDENTIAL_MANAGER_NATIVE_CI/);
});
