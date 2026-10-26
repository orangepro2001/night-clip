/* ABI smoke test for the c-shared core. Built against the Linux .so by
 * `make dll-smoke`; the Windows DLL exports the identical functions. */
#include <stdio.h>
#include <string.h>
#include <stdlib.h>

#include "libclipbridge_core.h"

#define CHECK(cond, msg) do { if (!(cond)) { fprintf(stderr, "FAIL: %s\n", msg); return 1; } } while (0)

int main(void) {
    char *err = NULL;

    char *before = cb_call("status", "{}");
    CHECK(before && strstr(before, "NOT_INITIALIZED"), "status before init must fail");
    cb_free(before);

    CHECK(cb_init("{\"device_name\":\"smoke\",\"platform\":\"windows\",\"listen\":true}", &err) == 0, "cb_init");
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

    printf("OK\n");
    return 0;
}
