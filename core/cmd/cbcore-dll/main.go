// Command cbcore-dll builds clipbridge_core.dll (Windows) or a shared object
// (Linux, used only by the C smoke test). It exports the five-function C ABI
// from docs/architecture.md §2.2.
//
//	int   cb_init(const char* config_json, char** err_out);
//	char* cb_call(const char* method, const char* params_json);
//	char* cb_next_event(int timeout_ms);
//	void  cb_shutdown(void);
//	void  cb_free(char* p);
//
// All strings are UTF-8. Returned strings must be released with cb_free.
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"unsafe"

	"nightclip/core/facade"
	"nightclip/core/proto"
)

var core = facade.New()

//export cb_init
func cb_init(configJSON *C.char, errOut **C.char) (rc C.int) {
	defer func() {
		if r := recover(); r != nil {
			setErr(errOut, "internal error")
			rc = 2
		}
	}()
	if err := core.Init(C.GoString(configJSON)); err != nil {
		setErr(errOut, err.Error())
		return 1
	}
	return 0
}

//export cb_call
func cb_call(method, paramsJSON *C.char) (out *C.char) {
	defer func() {
		if r := recover(); r != nil {
			out = C.CString(`{"ok":false,"error":{"code":"` + string(proto.CodeInternal) + `","message":"internal error","retryable":false}}`)
		}
	}()
	return C.CString(core.Call(C.GoString(method), C.GoString(paramsJSON)))
}

//export cb_next_event
func cb_next_event(timeoutMs C.int) (out *C.char) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
		}
	}()
	ev := core.NextEvent(int(timeoutMs))
	if ev == "" {
		return nil
	}
	return C.CString(ev)
}

//export cb_shutdown
func cb_shutdown() {
	defer func() { _ = recover() }()
	core.Shutdown()
}

//export cb_free
func cb_free(p *C.char) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

func setErr(out **C.char, msg string) {
	if out != nil {
		*out = C.CString(msg)
	}
}

// main is required by -buildmode=c-shared and is never called.
func main() {}
