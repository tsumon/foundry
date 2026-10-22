"use strict";
// Build-time source sidecar only. Never imported by the installed application.
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const origins = new Set([
  "https://chromium.googlesource.com",
  "https://raw.githubusercontent.com",
  "https://codeload.github.com",
  "https://github.com",
  "https://release-assets.githubusercontent.com",
  "https://nsis.sourceforge.io",
  "https://www.7-zip.org",
]);
function validateEntry(entry) {
  if (
    !/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(entry.file) ||
    ["manifest.json", "readme.md", "sha256sums"].includes(
      entry.file.toLowerCase(),
    ) ||
    !Number.isSafeInteger(entry.bytes) ||
    entry.bytes < 1 ||
    entry.bytes > 64 * 1024 * 1024 ||
    !/^[a-f0-9]{64}$/.test(entry.sha256) ||
    (entry.encoding !== undefined && entry.encoding !== "base64")
  )
    throw Error("Runtime source integrity failure: invalid metadata");
  if (entry.canonical !== undefined) {
    const c = entry.canonical;
    if (
      !c ||
      c.format !== "gitiles-tar-v1" ||
      !/^[a-f0-9]{64}$/.test(c.sha256) ||
      !Number.isSafeInteger(c.entries) ||
      c.entries < 1 ||
      c.entries > 100000 ||
      !Number.isSafeInteger(c.fileBytes) ||
      c.fileBytes < 0 ||
      c.fileBytes > 256 * 1024 * 1024 ||
      entry.encoding !== undefined ||
      !Number.isSafeInteger(entry.maxDownloadBytes) ||
      entry.maxDownloadBytes < entry.bytes ||
      entry.maxDownloadBytes > 64 * 1024 * 1024
    )
      throw Error(
        "Runtime source integrity failure: invalid canonical metadata",
      );
  } else if (entry.maxDownloadBytes !== undefined)
    throw Error("Runtime source integrity failure: invalid transport metadata");
}
function verifyInput(entry, raw) {
  validateEntry(entry);
  const actual = {
    file: entry.file,
    bytes: raw.length,
    sha256: crypto.createHash("sha256").update(raw).digest("hex"),
  };
  if (entry.canonical) {
    if (!raw.length || raw.length > entry.maxDownloadBytes)
      throw Error("Runtime source integrity failure: compressed bound");
    const observed = require("./gitiles-archive.cjs").archiveIdentity(raw),
      expected = entry.canonical;
    if (
      observed.sha256 !== expected.sha256 ||
      observed.entries !== expected.entries ||
      observed.fileBytes !== expected.fileBytes
    )
      throw Error(
        "Runtime source integrity failure: " +
          entry.file +
          " expected " +
          JSON.stringify(expected) +
          " observed " +
          JSON.stringify(observed),
      );
  } else if (actual.bytes !== entry.bytes || actual.sha256 !== entry.sha256)
    throw Error(
      "Runtime source integrity failure: " +
        entry.file +
        " expected " +
        JSON.stringify({ bytes: entry.bytes, sha256: entry.sha256 }) +
        " observed " +
        JSON.stringify(actual),
    );
  return actual;
}
async function download(entry, fetchImpl = fetch) {
  validateEntry(entry);
  let url = new URL(entry.url),
    response;
  const signal = AbortSignal.timeout(120000);
  for (let hops = 0; hops <= 3; hops++) {
    if (!origins.has(url.origin) || url.username || url.password)
      throw Error("Unapproved source origin");
    response = await fetchImpl(url, { redirect: "manual", signal });
    if (![301, 302, 303, 307, 308].includes(response.status)) break;
    const location = response.headers.get("location");
    await response.body?.cancel();
    if (!location || hops === 3) throw Error("Runtime source redirect bound");
    url = new URL(location, url);
  }
  if (!response.ok || !response.body)
    throw Error("Runtime source download failed: HTTP " + response.status);
  const chunks = [];
  let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    const bound =
      entry.encoding === "base64"
        ? Math.ceil(entry.bytes / 3) * 4 + 16
        : (entry.maxDownloadBytes ?? entry.bytes);
    if (size > bound) throw Error("Runtime source transport exceeds bound");
    chunks.push(chunk);
  }
  const raw = Buffer.concat(chunks);
  return entry.encoding === "base64"
    ? Buffer.from(raw.toString("ascii").trim(), "base64")
    : raw;
}
async function prepareSources({ output, local, manifest, readme, fetchImpl }) {
  if (!path.isAbsolute(output) || fs.existsSync(output))
    throw Error("Source output must be a fresh absolute directory");
  if (local && !path.isAbsolute(local))
    throw Error("Local source input must be absolute");
  const names = new Set();
  for (const entry of manifest.files) {
    validateEntry(entry);
    if (names.has(entry.file.toLowerCase()))
      throw Error("Duplicate source input");
    names.add(entry.file.toLowerCase());
  }
  fs.mkdirSync(output, { mode: 0o700 });
  const receipts = [];
  for (const entry of manifest.files) {
    let raw;
    if (local) {
      const filename = path.join(local, entry.file);
      const stat = fs.lstatSync(filename);
      if (path.dirname(filename) !== local || !stat.isFile())
        throw Error("Regular local source input required");
      if (
        entry.canonical
          ? stat.size > entry.maxDownloadBytes
          : stat.size !== entry.bytes
      )
        throw Error("Runtime source integrity failure: size");
      raw = fs.readFileSync(filename);
    } else raw = await download(entry, fetchImpl);
    const receipt = verifyInput(entry, raw);
    receipts.push({
      ...entry,
      ...receipt,
      ...(entry.canonical
        ? { originalTransport: { bytes: entry.bytes, sha256: entry.sha256 } }
        : {}),
    });
    fs.writeFileSync(path.join(output, entry.file), raw, {
      flag: "wx",
      mode: 0o644,
    });
  }
  fs.writeFileSync(
    path.join(output, "manifest.json"),
    JSON.stringify({ ...manifest, files: receipts }, null, 2) + "\n",
    { flag: "wx", mode: 0o644 },
  );
  fs.writeFileSync(path.join(output, "README.md"), readme, {
    flag: "wx",
    mode: 0o644,
  });
  fs.writeFileSync(
    path.join(output, "SHA256SUMS"),
    receipts.map((e) => e.sha256 + "  " + e.file).join("\n") + "\n",
    { flag: "wx", mode: 0o644 },
  );
}
module.exports = { prepareSources, verifyInput, download };
if (require.main === module) {
  const kind = process.argv[3] || "runtime";
  if (!["runtime", "installer"].includes(kind))
    throw Error("Unknown source kit");
  prepareSources({
    output: process.argv[2] || "",
    local: process.env.FOUNDRY_RUNTIME_SOURCES || "",
    manifest: require("./" + kind + "-sources.json"),
    readme: fs.readFileSync(path.join(__dirname, "runtime-sources.md"), "utf8"),
  })
    .then(() =>
      console.log("Verified runtime source sidecar: " + process.argv[2]),
    )
    .catch((error) => {
      console.error(error.message);
      process.exitCode = 1;
    });
}
