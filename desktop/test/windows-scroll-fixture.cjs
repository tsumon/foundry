"use strict";
// Pinned Electron accessibility fixture; no backend, account, VM or network.
const { app, BrowserWindow } = require("electron");

app.whenReady().then(async () => {
  const window = new BrowserWindow({
    width: 900,
    height: 640,
    title: "Foundry — Controlled Coding Workbench",
    webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false },
  });
  window.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  const html = `<!doctype html><html><head><meta charset="utf-8">
    <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">
    <title>Foundry — Controlled Coding Workbench</title>
    <style>body{margin:0;font:16px sans-serif}#status{position:fixed;top:0;background:white;z-index:1;padding:8px}
    main{padding:50px 40px}section{border:1px solid #aaa;padding:20px}.above{height:1400px}.below{height:1000px}
    dt,dd,p,label{margin:12px 0}label{display:block}button{margin:12px 0}</style></head><body>
    <div id="status" role="group" aria-label="工作台状态"><span>已收到</span><span>事件流已连接</span></div>
    <main><div class="above">Preceding task inputs</div>
    <section role="group" aria-label="账户模型编码设置">
      <div class="above">Language selectors and fixed VM identity metadata</div>
      <dl><dt>运行时 / 提供商</dt><dd>可配置 · openai</dd></dl>
      <p>元数据可用不等于已授权。创建只冻结仓库、七角色账户/模型/消费类别与此配置；readiness、baseline、context、execute、delivery 仍分别决定。</p>
      <label><input type="checkbox">启用账户模型与内置 VM（仅创建配置，不批准或执行）</label>
      <button type="button">刷新编码能力</button>
    </section><div class="below">Following task inputs</div></main></body></html>`;
  await window.loadURL("data:text/html;charset=utf-8," + encodeURIComponent(html));
}).catch((error) => {
  process.stderr.write(`Native scroll fixture failed: ${error.message}\n`);
  app.exit(1);
});
app.on("window-all-closed", () => app.quit());
