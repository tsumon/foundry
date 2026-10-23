"use strict";
// Foundry's exact, build-only QEMU 11.1.2 pipe-transport correction.
// The original MIT notice is retained in the patched upstream file.
const crypto = require("node:crypto");
const digest = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const stdioPatchIdentity = Object.freeze({
  version: "qemu-11.1.2-foundry-pipe-v1",
  path: "chardev/char-win-stdio.c",
  before_sha256: "b318c3e4835be67b86f27a9d2003c9adfdf5069e17b3e531938a137d794f9030",
  sha256: "64893f9baa3b8740a13379c9ff469c1f8c8eea864d6b08802db8a53075441022"
});
const replacements = [
  [
    "    uint8_t win_stdio_buf;\n",
    "    uint8_t win_stdio_buf;\n    /* Foundry: BQL-protected; never accessed by the pipe reader thread. */\n    bool pipe_pending;\n"
  ],
  [
    "        /* Some terminal emulator returns \\r\\n for Enter, just pass \\n */\n        if (stdio->win_stdio_buf == '\\r') {\n            continue;\n        }\n\n",
    ""
  ],
  [
    "static void win_stdio_thread_wait_func(void *opaque)\n{\n    Chardev *chr = CHARDEV(opaque);\n    WinStdioChardev *stdio = WIN_STDIO_CHARDEV(opaque);\n\n    if (qemu_chr_be_can_write(chr)) {\n        qemu_chr_be_write(chr, &stdio->win_stdio_buf, 1);\n    }\n\n    SetEvent(stdio->hInputDoneEvent);\n}\n\n",
    "static void win_stdio_accept_input(Chardev *chr)\n{\n    WinStdioChardev *stdio = WIN_STDIO_CHARDEV(chr);\n\n    if (stdio->pipe_pending && qemu_chr_be_can_write(chr)) {\n        /* Clear before delivery: the frontend may reenter accept_input. */\n        stdio->pipe_pending = false;\n        qemu_chr_be_write(chr, &stdio->win_stdio_buf, 1);\n        SetEvent(stdio->hInputDoneEvent);\n    }\n}\n\nstatic void win_stdio_thread_wait_func(void *opaque)\n{\n    WinStdioChardev *stdio = WIN_STDIO_CHARDEV(opaque);\n\n    stdio->pipe_pending = true;\n    win_stdio_accept_input(CHARDEV(opaque));\n}\n\n"
  ],
  [
    "    cc->chr_set_echo = win_stdio_chr_set_echo;\n",
    "    cc->chr_set_echo = win_stdio_chr_set_echo;\n    cc->chr_accept_input = win_stdio_accept_input;\n"
  ]
];
function applyStdioPatch(source) {
  if (!Buffer.isBuffer(source) || digest(source) !== stdioPatchIdentity.before_sha256) throw Error("QEMU stdio source integrity failure");
  let text = source.toString("utf8");
  for (const [before, after] of replacements) {
    if (text.split(before).length !== 2) throw Error("QEMU stdio patch context mismatch");
    text = text.replace(before, after);
  }
  const fixed = Buffer.from(text);
  if (digest(fixed) !== stdioPatchIdentity.sha256) throw Error("QEMU stdio patched integrity failure");
  return fixed;
}
function patchStdioSource(root) {
  const fs=require("node:fs"), path=require("node:path");
  for(const dir of [root,path.join(root,"chardev")]) {
    const s=fs.lstatSync(dir); if(!s.isDirectory() || s.isSymbolicLink()) throw Error("Regular QEMU source directory required");
  }
  const file=path.join(root,stdioPatchIdentity.path), stat=fs.lstatSync(file);
  if(!stat.isFile() || stat.isSymbolicLink()) throw Error("Regular QEMU stdio source required");
  const fixed=applyStdioPatch(fs.readFileSync(file));
  fs.writeFileSync(file,fixed);
  if(digest(fs.readFileSync(file))!==stdioPatchIdentity.sha256) throw Error("QEMU stdio written integrity failure");
  return {...stdioPatchIdentity};
}
module.exports = { applyStdioPatch, patchStdioSource, stdioPatchIdentity };
