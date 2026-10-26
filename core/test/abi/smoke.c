/* ABI smoke test for the c-shared core. Built against the Linux .so by
 * `make dll-smoke`; the Windows DLL exports the identical functions. */
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <unistd.h>

#include "libclipbridge_core.h"

#define CHECK(cond, msg) do { if (!(cond)) { fprintf(stderr, "FAIL: %s\n", msg); return 1; } } while (0)

/* Test-only key: base64 of the 32 bytes 0x00..0x1f. Never reuse in a shell. */
#define TEST_KEY_B64 "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

int main(void) {
    char *err = NULL;
    char dir[] = "/tmp/clipbridge-smoke-XXXXXX";
    CHECK(mkdtemp(dir) != NULL, "mkdtemp");
    char config[512];
    snprintf(config, sizeof config,
             "{\"data_dir\":\"%s\",\"db_key_b64\":\"" TEST_KEY_B64 "\","
             "\"device_name\":\"smoke\",\"platform\":\"windows\",\"listen\":true}", dir);

    char *before = cb_call("status", "{}");
    CHECK(before && strstr(before, "NOT_INITIALIZED"), "status before init must fail");
    cb_free(before);

    /* A rejected config (relative data_dir) must fail and leave the core usable. */
    CHECK(cb_init("{\"data_dir\":\"relative\",\"db_key_b64\":\"" TEST_KEY_B64 "\","
                  "\"device_name\":\"smoke\",\"platform\":\"windows\"}", &err) != 0 && err != NULL,
          "relative data_dir must be rejected");
    cb_free(err);
    err = NULL;
    CHECK(cb_next_event(20) == NULL, "failed init must not emit core.started");

    CHECK(cb_init(config, &err) == 0, "cb_init");
    CHECK(err == NULL, "cb_init set error on success");

    char *st = cb_call("status", "{}");
    CHECK(st && strstr(st, "\"ok\":true") && strstr(st, "dev_"), "status after init");
    printf("status: %s\n", st);
    cb_free(st);

    char *ev = cb_next_event(1000);
    CHECK(ev && strstr(ev, "core.started"), "core.started event");
    printf("event: %s\n", ev);
    cb_free(ev);

    char *none = cb_next_event(50);
    CHECK(none == NULL, "timeout must return NULL");

    char *bad = cb_call("does.not.exist", "{}");
    CHECK(bad && strstr(bad, "METHOD_NOT_FOUND"), "unknown method");
    cb_free(bad);

    CHECK(cb_init("{}", &err) != 0 && err != NULL, "second/invalid init must fail with message");
    cb_free(err);

    cb_shutdown();
    CHECK(cb_next_event(5000) == NULL, "NextEvent after shutdown returns NULL promptly");

    char *after = cb_call("status", "{}");
    CHECK(after && strstr(after, "NOT_INITIALIZED"), "status after shutdown must fail");
    cb_free(after);

    /* Remove the database files so the temp dir does not accumulate. */
    char cmd[600];
    snprintf(cmd, sizeof cmd, "rm -rf '%s'", dir);
    CHECK(system(cmd) == 0, "cleanup");

    printf("OK\n");
    return 0;
}
