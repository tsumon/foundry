"use strict";
const test = require("node:test"), assert = require("node:assert/strict");
const fs = require("node:fs"), path = require("node:path"), os = require("node:os"), cp = require("node:child_process"), crypto = require("node:crypto");
const source = fs.readFileSync(path.join(__dirname, "fixtures/qemu-11.1.2-win-stdio.c"));
let patch;
try { patch = require("../sandbox/windows-stdio.cjs"); } catch (e) { if (e.code !== "MODULE_NOT_FOUND") throw e; }
// This executes the pinned source's actual pipe thread; only its OS read/event
// boundary is controlled. It detects corruption, not presence of a source line.
function runPipe(t, mode) {
  assert.equal(crypto.createHash("sha256").update(source).digest("hex"), "b318c3e4835be67b86f27a9d2003c9adfdf5069e17b3e531938a137d794f9030");
  const fixed = patch ? patch.applyStdioPatch(source) : source;
  const text = fixed.toString("utf8"), start = text.indexOf("static DWORD WINAPI win_stdio_thread("), end = text.indexOf("static void win_stdio_chr_set_echo(", start);
  assert.ok(start > 0 && end > start);
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "foundry-stdio-test-"));
  t.after(() => fs.rmSync(root, {recursive:true, force:true}));
  const input = path.join(root, "pipe.c"), output = path.join(root, process.platform === "win32" ? "pipe.exe" : "pipe");
  fs.writeFileSync(input, `#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <stdbool.h>
typedef unsigned long DWORD;
typedef void *LPVOID;
typedef void *HANDLE;
typedef void ObjectClass;
typedef void ChardevBackend;
typedef void Error;
typedef struct { int unused; } Chardev;
#define WINAPI
#define INFINITE -1
#define WAIT_OBJECT_0 0
${text.slice(text.indexOf("struct WinStdioChardev {"), text.indexOf("DECLARE_INSTANCE_CHECKER("))}
#define WIN_STDIO_CHARDEV(p) ((WinStdioChardev *)(p))
#define CHARDEV(p) ((Chardev *)(p))
typedef struct { bool (*chr_open)(Chardev*,ChardevBackend*,Error**); int (*chr_write)(Chardev*,const uint8_t*,int); void (*chr_set_echo)(Chardev*,bool); void (*chr_accept_input)(Chardev*); } ChardevClass;
#define CHARDEV_CLASS(p) ((ChardevClass *)(p))
static WinStdioChardev current;
static uint8_t received[256];
static unsigned position, count, acknowledged;
static int writable=1;
static void (*reenter)(Chardev*);
static void win_stdio_thread_wait_func(void *opaque);
static int ReadFile(void *h, void *b, int n, DWORD *size, void *o) { (void)h; (void)n; (void)o; if(position==256) return 0; *(uint8_t*)b=(uint8_t)position++; *size=1; return 1; }
static int SetEvent(void *h) { if(h==current.hInputReadyEvent) win_stdio_thread_wait_func(&current); else acknowledged++; return 1; }
static int WaitForSingleObject(void *h,int timeout) { (void)h; (void)timeout; return WAIT_OBJECT_0; }
static void qemu_del_wait_object(void *a, void *b, void *c) { (void)a; (void)b; (void)c; }
static int qemu_chr_be_can_write(Chardev *chr) { (void)chr; return writable; }
static void qemu_chr_be_write(Chardev *chr,const uint8_t *bytes,int n) { if(n==1 && count<256) received[count++]=*bytes; if(reenter) { void (*call)(Chardev*)=reenter; reenter=NULL; call(chr); } }
static bool win_stdio_chr_open(Chardev *a,ChardevBackend *b,Error **c) { (void)a; (void)b; (void)c; return true; }
static int win_stdio_chr_write(Chardev *a,const uint8_t *b,int c) { (void)a; (void)b; return c; }
static void win_stdio_chr_set_echo(Chardev *a,bool b) { (void)a; (void)b; }
${text.slice(start,end)}
${text.slice(text.indexOf("static void char_win_stdio_class_init("),text.indexOf("static const TypeInfo char_win_stdio_type_info"))}
int main(int argc,char **argv) {
  current.hInputReadyEvent=(void*)1; current.hInputDoneEvent=(void*)2;
  ChardevClass cls={0}; char_win_stdio_class_init((ObjectClass*)&cls,NULL);
  if(argc!=2) return 99;
  if(strcmp(argv[1],"binary")==0) {
    win_stdio_thread(&current); if(count!=256 || acknowledged!=256) return 1;
    for(unsigned i=0;i<256;i++) if(received[i]!=i) return 2;
  } else {
    writable=0; current.win_stdio_buf=13; win_stdio_thread_wait_func(&current);
    if(count || acknowledged) return 10;
    if(!cls.chr_accept_input) return 11;
    cls.chr_accept_input(CHARDEV(&current)); if(count || acknowledged) return 12;
    writable=1; reenter=cls.chr_accept_input; cls.chr_accept_input(CHARDEV(&current));
    if(count!=1 || acknowledged!=1 || received[0]!=13) return 13;
    cls.chr_accept_input(CHARDEV(&current)); if(count!=1 || acknowledged!=1) return 14;
    current.win_stdio_buf=13; win_stdio_thread_wait_func(&current);
    if(count!=2 || acknowledged!=2 || received[1]!=13) return 15;
  }
  puts("exact"); return 0;
}
`, {flag:"wx"});
  const compiler = process.platform === "win32" ? process.env.CC : "/usr/bin/cc";
  assert.ok(compiler && path.isAbsolute(compiler));
  cp.execFileSync(compiler,["-std=c11","-Wall","-Wextra","-Werror","-Wno-unused-parameter",input,"-o",output],{timeout:30000,stdio:"pipe"});
  const result=cp.spawnSync(output,[mode],{timeout:5000,encoding:"utf8"});
  assert.equal(result.status,0,"the actual pipe thread dropped or changed a byte");
  assert.equal(result.stdout.trim(),"exact");
}
test("Windows pipe transport preserves all 256 binary byte values", t => runPipe(t,"binary"));
test("Windows pipe holds blocked bytes until frontend readiness and never duplicates delivery", t => runPipe(t,"pressure"));
test("stdio patch rejects changed input and cannot silently be applied twice", () => {
  assert.equal(typeof patch?.applyStdioPatch,"function");
  const fixed=patch.applyStdioPatch(source);
  assert.notDeepEqual(fixed,source);
  assert.throws(()=>patch.applyStdioPatch(Buffer.concat([source,Buffer.from(" ")])),/integrity/i);
  assert.throws(()=>patch.applyStdioPatch(fixed),/integrity/i);
});
test("fresh source-tree patch writes only the exact pinned file and refuses changed or linked input", t => {
  assert.equal(typeof patch?.patchStdioSource,"function");
  const root=fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()),"foundry-stdio-tree-"));
  t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
  fs.mkdirSync(path.join(root,"chardev"));
  const file=path.join(root,"chardev/char-win-stdio.c");
  fs.writeFileSync(file,source);
  const identity=patch.patchStdioSource(root);
  assert.deepEqual(identity,patch.stdioPatchIdentity);
  assert.deepEqual(fs.readFileSync(file),patch.applyStdioPatch(source));
  assert.throws(()=>patch.patchStdioSource(root),/integrity/i);
  fs.unlinkSync(file);
  const other=path.join(root,"original.c"); fs.writeFileSync(other,source); fs.symlinkSync(other,file);
  assert.throws(()=>patch.patchStdioSource(root),/regular/i);
  assert.deepEqual(fs.readFileSync(other),source);
});
