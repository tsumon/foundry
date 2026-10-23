const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { execFileSync } = require("node:child_process");
const test = require("node:test");

test("compiled native preflight queries one fixed capability via System32 and releases the exact library", (t) => {
  const root = fs.mkdtempSync(
    path.join(os.tmpdir(), "foundry-probe-contract-"),
  );
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.writeFileSync(
    path.join(root, "windows.h"),
    `
#include <stdint.h>
#include <wchar.h>
#define WINAPI
#define LOAD_LIBRARY_SEARCH_SYSTEM32 0x800
typedef void *HMODULE;
typedef void *HANDLE;
typedef void *PVOID;
typedef uint32_t UINT32;
typedef uint32_t *PUINT32;
typedef int32_t HRESULT;
typedef void (*FARPROC)(void);
HMODULE LoadLibraryExW(const wchar_t *, HANDLE, uint32_t);
FARPROC GetProcAddress(HMODULE, const char *);
int FreeLibrary(HMODULE);
`,
  );
  fs.writeFileSync(
    path.join(root, "stub.c"),
    `
#include "windows.h"
#include <stdlib.h>
#include <string.h>
static int queries, freed;
static const char *scenario;
static int is(const char *s) { return strcmp(scenario,s)==0; }
static void check_exit(void) {
 if (queries != (is("library_missing")||is("symbol_missing")?0:1)) _Exit(91);
 if (freed != (is("library_missing")?0:1)) _Exit(92);
}
HMODULE LoadLibraryExW(const wchar_t *name, HANDLE file, uint32_t flags) {
 scenario=getenv("FOUNDRY_PROBE_CASE"); if (!scenario) exit(93);
 if (wcscmp(name,L"WinHvPlatform.dll") || file || flags!=0x800) exit(94);
 atexit(check_exit); return is("library_missing")?NULL:(HMODULE)(uintptr_t)1;
}
static HRESULT WINAPI query(UINT32 code, PVOID buffer, UINT32 size, PUINT32 written) {
 queries++; if (code || size!=8 || *(uint64_t*)buffer || *written) exit(95);
 if (is("query_failed")) return (HRESULT)0x80004005u;
 *(uint64_t*)buffer=is("absent")?0:1; *written=is("bad_size")?8:4; return 0;
}
FARPROC GetProcAddress(HMODULE module, const char *name) {
 if (module!=(HMODULE)(uintptr_t)1 || strcmp(name,"WHvGetCapability")) exit(96);
 if (is("symbol_missing")) return NULL;
 HRESULT (WINAPI *typed)(UINT32,PVOID,UINT32,PUINT32)=query; FARPROC generic;
 _Static_assert(sizeof(typed)==sizeof(generic),"pointer size");
 memcpy(&generic,&typed,sizeof(generic));return generic;
}
int FreeLibrary(HMODULE module) {if(module!=(HMODULE)(uintptr_t)1)exit(97);freed++;return 1;}
`,
  );
  const binary = path.join(
    root,
    process.platform === "win32" ? "probe.exe" : "probe",
  );
  const cc =
    process.platform === "win32" ? "C:\\mingw64\\bin\\gcc.exe" : "/usr/bin/cc";
  execFileSync(
    cc,
    [
      "-std=c11",
      "-Wall",
      "-Wextra",
      "-Werror",
      "-I",
      root,
      path.join(__dirname, "../windows-hypervisor-probe.c"),
      path.join(root, "stub.c"),
      "-o",
      binary,
    ],
    { stdio: "pipe", timeout: 60000 },
  );
  for (const [scenario, want] of [
    ["present", [1, 0, 4, 1]],
    ["absent", [1, 0, 4, 0]],
    ["library_missing", [0, 0, 0, 0]],
    ["symbol_missing", [0, 0, 0, 0]],
    ["query_failed", [1, 2147500037, 0, 0]],
    ["bad_size", [1, 0, 8, 1]],
  ]) {
    const out = execFileSync(binary, [], {
      encoding: "utf8",
      timeout: 5000,
      env: { FOUNDRY_PROBE_CASE: scenario },
      stdio: ["ignore", "pipe", "pipe"],
    });
    assert.deepEqual(JSON.parse(out), want);
  }
});
