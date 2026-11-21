package sshagenthost

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmQueryEndSession    = 0x11
	wmEndSession         = 0x16
	wmPowerBroadcast     = 0x218
	wmSessionChange      = 0x2b1
	wtsConsoleConnect    = 1
	wtsConsoleDisconnect = 2
	wtsRemoteConnect     = 3
	wtsRemoteDisconnect  = 4
	wtsLogoff            = 6
	wtsLock              = 7
	wtsUnlock            = 8
	pbtSuspend           = 4
	pbtResumeSuspend     = 7
	pbtResumeAutomatic   = 0x12
)

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	wtsapi32          = windows.NewLazySystemDLL("wtsapi32.dll")
	registerClass     = user32.NewProc("RegisterClassExW")
	createWindow      = user32.NewProc("CreateWindowExW")
	destroyWindow     = user32.NewProc("DestroyWindow")
	defWindowProc     = user32.NewProc("DefWindowProcW")
	getMessage        = user32.NewProc("GetMessageW")
	dispatchMessage   = user32.NewProc("DispatchMessageW")
	registerSession   = wtsapi32.NewProc("WTSRegisterSessionNotification")
	unregisterSession = wtsapi32.NewProc("WTSUnRegisterSessionNotification")
)

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, Name                     *uint16
	SmallIcon                          uintptr
}

type message struct {
	Window         uintptr
	Msg            uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}

const sessionWindowClass = "MoonPasswordSSHAgentSession"

// WatchSession registers a hidden top-level window in the user's logon session.
// Keep this running in the host even while both update and UI processes exit.
func (h *Host) WatchSession(windowTitle string) error {
	ready := make(chan error, 1)
	go h.watchSession(windowTitle, ready)
	return <-ready
}

func (h *Host) watchSession(windowTitle string, ready chan<- error) {
	runtime.LockOSThread()
	// Do not unlock this thread: GetMessage and callbacks own per-thread state.
	className, _ := windows.UTF16PtrFromString(sessionWindowClass)
	title, err := windows.UTF16PtrFromString(windowTitle)
	if err != nil {
		ready <- err
		return
	}
	var instance windows.Handle
	if err := windows.GetModuleHandleEx(windows.GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT, nil, &instance); err != nil {
		ready <- err
		return
	}
	wc := windowClass{Instance: uintptr(instance), Name: className}
	wc.Size = uint32(unsafe.Sizeof(wc))
	wc.Proc = syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		if result, handled := h.windowsMessage(msg, wp); handled {
			return result
		}
		res, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wp, lp)
		return res
	})
	if ret, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 {
		ready <- fmt.Errorf("register SSH Agent session window: %w", err)
		return
	}
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, uintptr(instance), 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("create SSH Agent session window: %w", err)
		return
	}
	defer destroyWindow.Call(hwnd)
	if ret, _, err := registerSession.Call(hwnd, 0 /* NOTIFY_FOR_THIS_SESSION */); ret == 0 {
		ready <- fmt.Errorf("register SSH Agent session events: %w", err)
		return
	}
	defer unregisterSession.Call(hwnd)
	ready <- nil
	var m message
	for {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			h.Close()
			return
		}
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (h *Host) windowsMessage(msg uint32, event uintptr) (uintptr, bool) {
	switch msg {
	case wmQueryEndSession:
		h.Session("session", true)
		return 1, true
	case wmEndSession:
		if event != 0 {
			h.Close()
		} else {
			h.Session("session", false)
		}
		return 0, true
	case wmSessionChange:
		switch event {
		case wtsConsoleDisconnect, wtsRemoteDisconnect:
			h.Session("disconnect", true)
		case wtsConsoleConnect, wtsRemoteConnect:
			h.Session("disconnect", false)
		case wtsLock:
			h.Session("session", true)
		case wtsUnlock:
			h.Session("session", false)
		case wtsLogoff:
			h.Close()
		}
		return 0, true
	case wmPowerBroadcast:
		switch event {
		case pbtSuspend:
			h.Session("suspend", true)
		case pbtResumeSuspend, pbtResumeAutomatic:
			h.Session("suspend", false)
		}
		return 1, true
	}
	return 0, false
}
