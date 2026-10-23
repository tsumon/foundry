/* CI diagnostic only: no partition creation, feature changes or fallback. */
#include <windows.h>
#include <inttypes.h>
#include <stdio.h>
#include <string.h>

typedef HRESULT (WINAPI *capability_query)(UINT32, PVOID, UINT32, PUINT32);

int main(void) {
    fputs("foundry-whpx:started\n", stderr);
    fflush(stderr);
    HMODULE module = LoadLibraryExW(L"WinHvPlatform.dll", NULL,
                                  LOAD_LIBRARY_SEARCH_SYSTEM32);
    if (!module) {
        puts("[0,0,0,0]");
        return 0;
    }
    FARPROC address = GetProcAddress(module, "WHvGetCapability");
    if (!address) {
        FreeLibrary(module);
        puts("[0,0,0,0]");
        return 0;
    }
    capability_query query;
    _Static_assert(sizeof(query) == sizeof(address), "native function pointer size");
    memcpy(&query, &address, sizeof(query));
    uint64_t value = 0;
    UINT32 written = 0;
    /* HypervisorPresent == 0. The BOOL is four bytes; retain a zeroed
       eight-byte capability buffer, just as the previous P/Invoke query. */
    HRESULT hr = query(0, &value, sizeof(value), &written);
    fputs("foundry-whpx:queried\n", stderr);
    FreeLibrary(module);
    printf("[1,%" PRIu32 ",%" PRIu32 ",%" PRIu64 "]\n",
           (uint32_t)hr, (uint32_t)written, value);
    return 0;
}
