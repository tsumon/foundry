"use strict";
const fs = require("node:fs"),
  path = require("node:path"),
  crypto = require("node:crypto");
function sandboxFixture(
  root,
  host = process.platform === "win32" ? "windows/amd64" : "darwin/arm64",
) {
  fs.mkdirSync(root, { recursive: true });
  const manifest = {
    schema_version: 1,
    host_platform: host,
    guest_platform: host === "windows/amd64" ? "linux/amd64" : "linux/arm64",
    protocol: "foundry-vm-v1",
    policy_version: "go-vm-v1",
    runtime:
      host === "windows/amd64"
        ? "runtime/qemu-system-x86_64.exe"
        : "runtime/foundry-vm-helper",
    kernel: "kernel",
    rootfs: "initramfs.cpio.gz",
    verifier: "foundry-vm-guest",
    assets: [],
  };
  for (const name of [
    manifest.runtime,
    manifest.kernel,
    manifest.rootfs,
    manifest.verifier,
    "legal/NOTICE.md",
    "legal/GPL-2.0.txt",
  ]) {
    const body = Buffer.from("inert fixture " + name);
    fs.mkdirSync(path.dirname(path.join(root, name)), { recursive: true });
    fs.writeFileSync(path.join(root, name), body, {
      mode: name === manifest.runtime ? 0o755 : 0o644,
    });
    manifest.assets.push({
      path: name,
      size: body.length,
      sha256: crypto.createHash("sha256").update(body).digest("hex"),
    });
  }
  fs.writeFileSync(path.join(root, "manifest.json"), JSON.stringify(manifest));
  return manifest;
}
module.exports = { sandboxFixture };
