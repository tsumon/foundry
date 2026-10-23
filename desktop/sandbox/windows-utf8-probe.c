/* Build-only native UCRT regression. No accounts, network or VM execution. */
#include <windows.h>
#include <stdio.h>
#include <string.h>
#include <io.h>
#include "foundry-utf8-expected.h"
static int manifest_matches(const char *file)
{
    WCHAR wide[32768];
    if (!MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, file, -1, wide, 32768)) return 0;
    /* DATAFILE never executes entry points or dependent DLL initializers. */
    HMODULE module = LoadLibraryExW(wide, NULL, LOAD_LIBRARY_AS_DATAFILE | LOAD_LIBRARY_AS_IMAGE_RESOURCE);
    if (!module) return 0;
    HRSRC resource = FindResourceW(module, MAKEINTRESOURCEW(1), MAKEINTRESOURCEW(24));
    DWORD size = resource ? SizeofResource(module, resource) : 0;
    HGLOBAL data = resource ? LoadResource(module, resource) : NULL;
    const void *bytes = data ? LockResource(data) : NULL;
    int ok = bytes && size == sizeof(expected_manifest) - 1 && memcmp(bytes, expected_manifest, size) == 0;
    FreeLibrary(module); return ok;
}
int main(int argc, char **argv)
{
    if (argc != 2 && argc != 3) return 2;
    const char *slash = strrchr(argv[1], '\\');
    const char *leaf = slash ? slash + 1 : argv[1];
    int argv_ok = strcmp(leaf, "\xe9\xaa\x8c\xe8\xaf\x81-\xf0\x9f\x98\x8a.bin") == 0;
    /* Same narrow CRT operations used by QEMU firmware lookup/loading. */
    int access_ok = _access(argv[1], 4) == 0, read_ok = 0;
    FILE *file = fopen(argv[1], "rb");
    if (file) {
        const char expected[] = "FOUNDRY_UTF8_FILE\n";
        char buffer[sizeof(expected)];
        size_t n = fread(buffer, 1, sizeof(buffer), file);
        read_ok = n == sizeof(expected) - 1 && !ferror(file) && feof(file) && memcmp(buffer, expected, n) == 0;
        if (fclose(file) != 0) read_ok = 0;
    }
    int manifest_ok = argc == 2 || manifest_matches(argv[2]);
    printf("{\"acp\":%u,\"argv_ok\":%s,\"access_ok\":%s,\"read_ok\":%s,\"manifest_ok\":%s}\n",
           GetACP(), argv_ok ? "true" : "false", access_ok ? "true" : "false", read_ok ? "true" : "false", manifest_ok ? "true" : "false");
    return 0;
}
