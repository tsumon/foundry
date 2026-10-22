"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const { childEnvironment, executableName } = require("../runtime.cjs");
const { workbenchReadiness } = require("../windows-ui-ready.cjs");

test("native UI timeout logs its sanitized receipt before scoped cleanup can fail", () => {
  const source = require("node:fs").readFileSync(
    require("node:path").join(__dirname, "../windows-installed-smoke.ps1"),
    "utf8",
  );
  assert.match(source, /\[Console\]::Error\.WriteLine\(\$diagnostic\)\s+throw 'Native workbench readiness deadline exceeded'/);
  assert.match(source, /\$diagnostic = .*semantics=\$last; paint=\$paint.*ConvertTo-Json -Compress/);
});

function renderedWorkbench() {
  const node = (parent, type, name) => ({
    parent,
    type,
    name,
    visible: true,
    enabled: true,
  });
  return {
    window: "1234",
    processId: 42,
    nodes: [
      node(-1, "Window", "Foundry — Persistent Workbench"),
      node(0, "Document", "FOUNDRY"),
      node(1, "Group", "主导航"),
      node(2, "Button", "交付总览"),
      node(2, "Button", "设置"),
      node(1, "Text", "交付总览"),
      node(1, "Button", "创建交付任务"),
      node(1, "Group", "工作台状态"),
      node(7, "Text", "已收到"),
      node(7, "Text", "事件流已连接"),
    ],
  };
}
const nativeWindow = { window: "1234", processId: 42 };
test("native UI readiness requires scoped rendered workbench controls and received data", () => {
  assert.equal(
    workbenchReadiness(renderedWorkbench(), nativeWindow).ready,
    true,
  );
  for (const mutate of [
    (s) => s.nodes.splice(1), // The previously accepted title/menu-only blank window.
    (s) => {
      s.nodes[8].name = "正在读取";
    },
    (s) => {
      s.nodes[9].name = "事件流重连中";
    },
    (s) => {
      s.nodes[4].visible = false;
    },
    (s) => {
      s.nodes[6].enabled = false;
    },
    (s) => {
      s.nodes[5].type = "Button";
    },
    (s) => {
      s.nodes[5].parent = 2;
    },
    (s) => {
      s.nodes[2].visible = false;
    },
    (s) => {
      s.nodes[8].parent = 1;
    },
    (s) => {
      s.nodes[4].parent = 1;
    },
    (s) => {
      s.processId = 43;
    },
    (s) => {
      s.window = "5678";
    },
    (s) => {
      s.nodes[2].parent = 9;
    },
  ]) {
    const snapshot = renderedWorkbench();
    mutate(snapshot);
    assert.equal(workbenchReadiness(snapshot, nativeWindow).ready, false);
  }
});

test("native readiness receipt excludes UI text and bounds malformed snapshots", () => {
  const snapshot = renderedWorkbench();
  snapshot.nodes.push({
    parent: 1,
    type: "Text",
    name: "PRIVATE_SENTINEL",
    visible: true,
    enabled: true,
  });
  const receipt = workbenchReadiness(snapshot, nativeWindow);
  assert.equal(receipt.ready, true);
  assert.doesNotMatch(JSON.stringify(receipt), /PRIVATE_SENTINEL|交付|1234/);
  for (const malformed of [
    null,
    {},
    { ...snapshot, nodes: Array(2049).fill(snapshot.nodes[0]) },
    { ...snapshot, nodes: [{ ...snapshot.nodes[0], name: "a".repeat(513) }] },
  ]) {
    assert.equal(workbenchReadiness(malformed, nativeWindow).ready, false);
  }
});
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
