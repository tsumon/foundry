"use strict";
// Build-time only: installed applications never fetch runtimes or change backends.
const fs = require("node:fs"), path = require("node:path"), crypto = require("node:crypto");
const origins = new Set(["https://dl-cdn.alpinelinux.org", "https://dl.google.com", "https://go.dev", "https://cdn.kernel.org", "https://raw.githubusercontent.com", "https://codeload.github.com", "https://mirror.msys2.org", "https://repo.msys2.org", "https://download.qemu.org"]);
function sourceURL(raw) {
  const u = new URL(raw);
  if (!origins.has(u.origin) || u.username || u.password || u.hash || u.search) throw Error("Unapproved sandbox asset origin");
  return u;
}
function validateAsset(e) {
  if (!e || !/^[a-zA-Z0-9][a-zA-Z0-9._+~-]{0,159}$/.test(e.file) || /^(con|prn|aux|nul|com[0-9]|lpt[0-9])(?:\.|$)/i.test(e.file) || e.file.endsWith(".") || ["receipt.json", "manifest.json"].includes(e.file.toLowerCase()) || !Number.isSafeInteger(e.bytes) || e.bytes < 1 || e.bytes > 512 * 1024 * 1024 || !/^[a-f0-9]{64}$/.test(e.sha256) || !["kernel", "toolchain", "runtime", "dependency", "source", "recipe", "notice"].includes(e.kind) || !Array.isArray(e.platforms) || e.platforms.length < 1 || e.platforms.some(p => !["darwin/arm64", "windows/amd64"].includes(p)) || typeof e.license !== "string" || !e.license || typeof e.provenance !== "string" || !e.provenance) throw Error("Invalid sandbox asset metadata");
  sourceURL(e.url);
}
function verifyAsset(e, raw) {
  validateAsset(e);
  const actual = { file: e.file, bytes: raw.length, sha256: crypto.createHash("sha256").update(raw).digest("hex") };
  if (actual.bytes !== e.bytes || actual.sha256 !== e.sha256) throw Error("Sandbox asset integrity failure: " + e.file);
  return actual;
}
async function fetchAsset(e, fetchImpl = fetch) {
  validateAsset(e);
  let url = sourceURL(e.url), response;
  const signal = AbortSignal.timeout(300000);
  for (let redirects = 0; redirects <= 3; redirects++) {
    response = await fetchImpl(url, { redirect: "manual", signal });
    if (![301, 302, 303, 307, 308].includes(response.status)) break;
    const location = response.headers.get("location");
    await response.body?.cancel();
    if (!location || redirects === 3) throw Error("Sandbox asset redirect bound");
    url = sourceURL(new URL(location, url).href);
  }
  if (!response?.ok || !response.body) throw Error("Sandbox asset HTTP " + response?.status);
  const chunks = []; let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    if (size > e.bytes) throw Error("Sandbox asset exceeds declared bound");
    chunks.push(chunk);
  }
  const raw = Buffer.concat(chunks); verifyAsset(e, raw); return raw;
}
function regularDirectory(directory) {
  if (!path.isAbsolute(directory)) throw Error("Absolute asset directory required");
  let current = path.parse(directory).root;
  for (const part of path.relative(current, directory).split(path.sep).filter(Boolean)) {
    current = path.join(current, part);
    if (!fs.lstatSync(current).isDirectory()) throw Error("Regular non-symlink asset directory required");
  }
}
async function prepareAssets({ output, local, entries, platform, fetchImpl }) {
  if (!["darwin/arm64", "windows/amd64"].includes(platform)) throw Error("Unsupported sandbox platform");
  if (!path.isAbsolute(output) || fs.existsSync(output)) throw Error("Fresh absolute asset output required");
  regularDirectory(path.dirname(output));
  if (local) regularDirectory(local);
  if (!Array.isArray(entries) || entries.length < 1 || entries.length > 128) throw Error("Invalid asset inventory");
  const names = new Set();
  for (const e of entries) { validateAsset(e); if (names.has(e.file.toLowerCase())) throw Error("Duplicate sandbox asset"); names.add(e.file.toLowerCase()); }
  const selected = entries.filter(e => e.platforms.includes(platform));
  if (selected.length === 0) throw Error("No assets for selected platform");
  fs.mkdirSync(output, { mode: 0o700 });
  const receipt = { schema_version: 1, platform, assets: [] };
  for (const e of selected) {
    let raw;
    if (local) {
      const file = path.join(local, e.file), stat = fs.lstatSync(file);
      if (!stat.isFile()) throw Error("Regular non-symlink input required");
      if (stat.size !== e.bytes) throw Error("Sandbox asset integrity failure: size");
      raw = fs.readFileSync(file);
    } else raw = await fetchAsset(e, fetchImpl);
    const actual = verifyAsset(e, raw);
    fs.writeFileSync(path.join(output, e.file), raw, { flag: "wx", mode: 0o600 });
    receipt.assets.push({ ...e, ...actual });
  }
  fs.writeFileSync(path.join(output, "receipt.json"), JSON.stringify(receipt, null, 2) + "\n", { flag: "wx", mode: 0o600 });
  return receipt;
}
module.exports = { validateAsset, verifyAsset, fetchAsset, prepareAssets };
if (require.main === module) prepareAssets({ output: process.argv[2] || "", platform: process.argv[3] || "", local: process.env.FOUNDRY_SANDBOX_INPUTS || "", entries: require("./inputs.lock.json").assets }).then(r => console.log(JSON.stringify({ platform: r.platform, verified: r.assets.length }))).catch(e => { console.error(e.message); process.exitCode = 1; });
