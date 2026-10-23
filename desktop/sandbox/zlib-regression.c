/* Foundry zlib backport verification, Apache-2.0. Never part of the app payload. */
#ifdef FOUNDRY_DLL_ABI_TEST
#include <zlib.h>
#include <string.h>
int main(void) {
    /* UCRT64 MSYS2 consumers use 32-bit z_off_t and 64-bit pointers. */
    return strcmp(zlibVersion(), "1.3.2") || sizeof(z_off_t) != 4 ||
        (zlibCompileFlags() & 255) != (1 | (1 << 2) | (2 << 4) | (1 << 6));
}
#else
#include "gzguts.h"
/* gzwrite.c alone is compiled with write=foundry_stall_write. Exercise the
   real vulnerable branch deterministically, without a host pipe or network. */
#ifdef _WIN32
int foundry_stall_write(int fd, const void *buf, unsigned len)
#else
ssize_t foundry_stall_write(int fd, const void *buf, size_t len)
#endif
{
    (void)fd; (void)buf; (void)len; errno = EAGAIN; return -1;
}
int main(void) {
    gz_state state;
    unsigned char input[128];
    memset(&state, 0, sizeof(state)); memset(input, 'x', sizeof(input));
    state.mode = GZ_WRITE; state.want = 16; state.direct = 1;
    state.path = (char *)"nonblocking-regression";
    if (gzwrite((gzFile)&state, input, sizeof(input)) != 0 || !state.again)
        return 2;
    /* The unpatched implementation retains the caller's buffer here. */
    if (state.strm.avail_in != 0 || state.strm.next_in != state.in)
        return 1;
    if (gzprintf((gzFile)&state, "%s", "ok") != 2)
        return 3;
    free(state.in); free(state.msg); return 0;
}
#endif
