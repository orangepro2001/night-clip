/* Windows ABI smoke test. Loads clipbridge_core.dll at run time with
 * LoadLibrary (no import library needed) and exercises the five exports.
 * Cross-compiled with mingw-w64 and run under Wine by `make dll-wine`. */
#include <windows.h>
#include <stdio.h>
#include <string.h>
#include <direct.h>

/* Test-only key: base64 of the 32 bytes 0x00..0x1f. Never reuse in a shell. */
#define TEST_KEY_B64 "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

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

    /* Fresh data dir under %TEMP%; backslashes are doubled for JSON. */
    char tmp[MAX_PATH], dir[MAX_PATH + 40], jsonDir[2 * MAX_PATH + 80];
    DWORD n = GetTempPathA(sizeof tmp, tmp);
    CHECK(n > 0 && n < sizeof tmp, "GetTempPath");
    snprintf(dir, sizeof dir, "%sclipbridge-smoke-%lu", tmp, (unsigned long)GetCurrentProcessId());
    CHECK(_mkdir(dir) == 0 || GetFileAttributesA(dir) != INVALID_FILE_ATTRIBUTES, "create data dir");
    size_t j = 0;
    for (size_t i = 0; dir[i] && j + 2 < sizeof jsonDir; i++) {
        if (dir[i] == '\\') jsonDir[j++] = '\\';
        jsonDir[j++] = dir[i];
    }
    jsonDir[j] = 0;
    char config[1536];
    snprintf(config, sizeof config,
             "{\"data_dir\":\"%s\",\"db_key_b64\":\"" TEST_KEY_B64 "\","
             "\"device_name\":\"wine\",\"platform\":\"windows\",\"listen\":false}", jsonDir);

    CHECK(cb_init("{\"data_dir\":\"relative\",\"db_key_b64\":\"" TEST_KEY_B64 "\","
                  "\"device_name\":\"wine\",\"platform\":\"windows\"}", &err) != 0 && err != NULL,
          "relative data_dir must be rejected");
    cb_free(err);
    err = NULL;

    CHECK(cb_init(config, &err) == 0, "cb_init");

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

    char *after = cb_call("status", "{}");
    CHECK(after && strstr(after, "NOT_INITIALIZED"), "status after shutdown must fail");
    cb_free(after);

    /* Best effort: the DLL has closed the database, so the files can go. */
    char file[MAX_PATH + 80];
    const char *leftovers[] = {"clipbridge.db", "clipbridge.db-wal", "clipbridge.db-shm"};
    for (size_t i = 0; i < sizeof leftovers / sizeof leftovers[0]; i++) {
        snprintf(file, sizeof file, "%s\\%s", dir, leftovers[i]);
        DeleteFileA(file);
    }
    RemoveDirectoryA(dir);

    printf("OK\n");
    return 0;
}
