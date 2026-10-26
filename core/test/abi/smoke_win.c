/* Windows ABI smoke test. Loads clipbridge_core.dll at run time with
 * LoadLibrary (no import library needed) and exercises the five exports.
 * Cross-compiled with mingw-w64 and run under Wine by `make dll-wine`. */
#include <windows.h>
#include <stdio.h>
#include <string.h>

typedef int   (*init_fn)(const char *, char **);
typedef char *(*call_fn)(const char *, const char *);
typedef char *(*next_fn)(int);
typedef void  (*shutdown_fn)(void);
typedef void  (*free_fn)(char *);

#define CHECK(cond, msg) do { if (!(cond)) { fprintf(stderr, "FAIL: %s\n", msg); return 1; } } while (0)

int main(int argc, char **argv) {
    const char *path = argc > 1 ? argv[1] : "clipbridge_core.dll";
    HMODULE h = LoadLibraryA(path);
    CHECK(h, "LoadLibrary clipbridge_core.dll");

    init_fn cb_init = (init_fn)GetProcAddress(h, "cb_init");
    call_fn cb_call = (call_fn)GetProcAddress(h, "cb_call");
    next_fn cb_next_event = (next_fn)GetProcAddress(h, "cb_next_event");
    shutdown_fn cb_shutdown = (shutdown_fn)GetProcAddress(h, "cb_shutdown");
    free_fn cb_free = (free_fn)GetProcAddress(h, "cb_free");
    CHECK(cb_init && cb_call && cb_next_event && cb_shutdown && cb_free, "exports present");

    char *err = NULL;
    CHECK(cb_init("{\"device_name\":\"wine\",\"platform\":\"windows\",\"listen\":true}", &err) == 0, "cb_init");

    char *st = cb_call("status", "{}");
    CHECK(st && strstr(st, "\"ok\":true") && strstr(st, "dev_"), "status");
    printf("status: %s\n", st);
    cb_free(st);

    char *ev = cb_next_event(1000);
    CHECK(ev && strstr(ev, "core.started"), "core.started event");
    cb_free(ev);

    CHECK(cb_next_event(50) == NULL, "timeout returns NULL");

    /* UTF-8 round trip through the ABI. */
    char *bad = cb_call("\xE6\x97\xA0\xE6\x95\x88", "{}");
    CHECK(bad && strstr(bad, "METHOD_NOT_FOUND"), "unknown (non-ASCII) method");
    cb_free(bad);

    cb_shutdown();
    DWORD t0 = GetTickCount();
    CHECK(cb_next_event(5000) == NULL, "NextEvent after shutdown");
    CHECK(GetTickCount() - t0 < 1000, "NextEvent woke promptly after shutdown");

    printf("OK\n");
    return 0;
}
