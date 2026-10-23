"use strict";
const test = require("node:test"), assert = require("node:assert/strict"), fs = require("node:fs"), os = require("node:os"), path = require("node:path"), crypto = require("node:crypto"), cp = require("node:child_process");
const { writeVMManifest, sourceInventory, verifyBuiltWindowsRuntime, extractKernelRecipe } = require("../sandbox/build.cjs");
function fixture(t) {
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "foundry-vm-bundle-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  for (const name of ["runtime/foundry-vm-helper", "kernel", "initramfs.cpio.gz", "foundry-vm-guest", "legal/NOTICE.md", "legal/GPL-2.0.txt"]) {
    fs.mkdirSync(path.dirname(path.join(root, name)), { recursive: true }); fs.writeFileSync(path.join(root, name), name);
  }
  return root;
}
test("bundle manifest freezes exact runtime/kernel/rootfs/guest and complete legal inventory", t => {
  const root = fixture(t), manifest = writeVMManifest(root, "darwin/arm64");
  assert.equal(manifest.host_platform, "darwin/arm64"); assert.equal(manifest.guest_platform, "linux/arm64"); assert.equal(manifest.assets.length, 6);
  for (const e of manifest.assets) {
    const bytes = fs.readFileSync(path.join(root, e.path));
    assert.equal(e.size, bytes.length); assert.equal(e.sha256, crypto.createHash("sha256").update(bytes).digest("hex"));
  }
  assert.throws(() => writeVMManifest(root, "darwin/arm64"), /existing/);
});
test("bundle inventory refuses symlinks, missing legal and unsupported platforms", t => {
  const root = fixture(t); fs.symlinkSync("kernel", path.join(root, "alias"));
  assert.throws(() => sourceInventory(root), /regular/);
  fs.unlinkSync(path.join(root, "alias")); fs.unlinkSync(path.join(root, "legal/GPL-2.0.txt"));
  assert.throws(() => writeVMManifest(root, "darwin/arm64"), /required/);
  assert.throws(() => writeVMManifest(root, "darwin/amd64"), /platform/);
});
function pe() {
  const b = Buffer.alloc(2048); b.write("MZ"); b.writeUInt32LE(128, 60); b.write("PE\0\0", 128);
  b.writeUInt16LE(0x8664, 132); b.writeUInt16LE(1, 134); b.writeUInt16LE(240, 148);
  b.writeUInt16LE(0x20b, 152); b.writeUInt32LE(16, 260);
  b.writeUInt32LE(4096, 272); b.writeUInt32LE(40, 276);
  b.write(".rdata", 392); b.writeUInt32LE(1536, 400); b.writeUInt32LE(4096, 404); b.writeUInt32LE(1536, 408); b.writeUInt32LE(512, 412);
  b.writeUInt32LE(4608, 524); b.write("KERNEL32.dll\0", 1024); return b;
}
test("Windows bundle re-derives PE imports instead of trusting build receipt claims", t => {
  assert.equal(typeof verifyBuiltWindowsRuntime, "function");
  const root = fixture(t), runtime = path.join(root, "windows"); fs.mkdirSync(runtime);
  for (const [name, bytes] of [["qemu-system-x86_64.exe", pe()], ["share/qemu/bios-256k.bin", Buffer.from("bios")], ["share/qemu/linuxboot_dma.bin", Buffer.from("boot")]]) {
    fs.mkdirSync(path.dirname(path.join(runtime, name)), { recursive: true }); fs.writeFileSync(path.join(runtime, name), bytes);
  }
  const proof = { files: sourceInventory(runtime).map(e => ({ path: "runtime/" + e.path, bytes: e.size, sha256: e.sha256 })), imports: [{ file: "qemu-system-x86_64.exe", imports: ["kernel32.dll"] }] };
  const lock = { windows: { allowed_runtime_dlls: [] } };
  assert.deepEqual(verifyBuiltWindowsRuntime(runtime, proof, lock), proof.imports);
  assert.throws(() => verifyBuiltWindowsRuntime(runtime, { ...proof, imports: [{ file: "qemu-system-x86_64.exe", imports: [] }] }, lock), /imports/);
  const changed = pe(); changed.write("evilcore.dll\0", 1024); fs.writeFileSync(path.join(runtime, "qemu-system-x86_64.exe"), changed);
  const modified = { ...proof, files: sourceInventory(runtime).map(e => ({ path: "runtime/" + e.path, bytes: e.size, sha256: e.sha256 })) };
  assert.throws(() => verifyBuiltWindowsRuntime(runtime, modified, lock), /Unresolved/);
});
test("corresponding kernel recipe includes only regular linux-lts files from the pinned subtree", t => {
  assert.equal(typeof extractKernelRecipe, "function");
  const root = fixture(t), prefix = "aports-e9951ece7106a5c898b0812cfa7b47d56379b361", input = path.join(root, "input");
  fs.mkdirSync(path.join(input, prefix, "main/linux-lts"), { recursive: true }); fs.mkdirSync(path.join(input, prefix, "unrelated"));
  for (const name of ["APKBUILD", "virt.aarch64.config", "virt.x86_64.config"]) fs.writeFileSync(path.join(input, prefix, "main/linux-lts", name), name);
  fs.writeFileSync(path.join(input, prefix, "unrelated/COPYING"), "unrelated component");
  const archive = path.join(root, "recipe.tar.gz"); cp.execFileSync("tar", ["-czf", archive, "-C", input, prefix]);
  const output = path.join(root, "kernel-recipe"); extractKernelRecipe(archive, output, "tar");
  assert.deepEqual(fs.readdirSync(output).sort(), ["APKBUILD", "virt.aarch64.config", "virt.x86_64.config"]);
  assert.equal(fs.readFileSync(path.join(output, "APKBUILD"), "utf8"), "APKBUILD");
  assert.throws(() => extractKernelRecipe(archive, output, "tar"), /exist/);
});
