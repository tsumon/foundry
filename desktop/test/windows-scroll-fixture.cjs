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
    <style>body{margin:0;font:14px sans-serif}#status{position:fixed;top:0;background:white;z-index:1;padding:8px}
    main{padding:50px 40px}section{border:1px solid #aaa;padding:20px}.above{height:1400px}.below{height:1000px}
    dl{display:grid;grid-template-columns:160px minmax(0,1fr);margin:0}
    dt,dd{margin:0;padding:10px 0;line-height:22px;border-bottom:1px solid #ddd;overflow-wrap:anywhere}
    p{margin:16px 0;line-height:22px}label{display:block;margin:16px 0;line-height:22px}button{height:39px;margin:0}</style></head><body>
    <div id="status" role="group" aria-label="工作台状态"><span>已收到</span><span>事件流已连接</span></div>
    <main><div class="above">Preceding task inputs</div>
    <section role="group" aria-label="账户模型编码设置">
      <div class="above">Language selectors and fixed VM identity metadata</div>
      <dl><dt>运行时 / 提供商</dt><dd>可配置 · openai</dd>
        <dt>固定配置</dt><dd>c4_typescript_model_v2 · linux/amd64</dd>
        <dt>固定验证命令</dt><dd>TypeScript typecheck and node:test · checked manifest command</dd>
        <dt>调用与修复限制</dt><dd>最多 7 次模型调用 · 最多 1 轮修复</dd>
        <dt>独立审批</dt><dd>readiness → baseline → context → execute → delivery</dd></dl>
      <p>元数据可用不等于已授权。创建只冻结仓库、七角色账户/模型/消费类别与此配置；readiness、baseline、context、execute、delivery 仍分别决定。Token 用量不是订阅剩余额度；界面不宣称金额硬上限，也不会自动重试、换模型或付费降级。</p>
      <label><input type="checkbox">启用账户模型与内置 VM（仅创建配置，不批准或执行）</label>
      <button type="button">刷新编码能力</button>
    </section><div class="below">Following task inputs</div></main></body></html>`;
  await window.loadURL("data:text/html;charset=utf-8," + encodeURIComponent(html));
}).catch((error) => {
  process.stderr.write(`Native scroll fixture failed: ${error.message}\n`);
  app.exit(1);
});
app.on("window-all-closed", () => app.quit());
