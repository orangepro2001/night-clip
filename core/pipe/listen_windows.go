//go:build windows

package pipe

import (
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// DefaultAddress is the well-known pipe name (docs/api-spec.md §8).
func DefaultAddress() string { return `\\.\pipe\clipbridge.v1` }

const (
	pipeAccessDuplex        = 0x00000003
	fileFlagFirstInstance   = 0x00080000
	pipeTypeByte            = 0x00000000
	pipeReadModeByte        = 0x00000000
	pipeWait                = 0x00000000
	pipeRejectRemoteClients = 0x00000008
	pipeUnlimitedInstances  = 255
	errorPipeConnected      = syscall.Errno(535)
	errorPipeBusy           = syscall.Errno(231)
	errorAccessDenied       = syscall.Errno(5)
)

var (
	advapi32             = syscall.NewLazyDLL("advapi32.dll")
	procConvertSDDL      = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree        = kernel32.NewProc("LocalFree")
	procWaitNamedPipe    = kernel32.NewProc("WaitNamedPipeW")
	procCreateNamedPipe  = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe = kernel32.NewProc("ConnectNamedPipe")
	sddlOwnerOnly        = "D:P(A;;GA;;;OW)" // protected DACL: only the pipe's owner (the user running the core)
)

type pipeListener struct {
	name    string
	sa      *syscall.SecurityAttributes
	sd      uintptr
	mu      sync.Mutex
	first   bool
	done    bool
	pending syscall.Handle // an instance created ahead of the first Accept
}

// Listen creates the named pipe. The first instance is created with
// FILE_FLAG_FIRST_PIPE_INSTANCE so that a process which grabbed the name before us
// makes Listen fail instead of silently receiving our clients; the access control
// list lets only the current user in; remote clients are refused.
func Listen(name string) (Listener, error) {
	sdw, err := syscall.UTF16PtrFromString(sddlOwnerOnly)
	if err != nil {
		return nil, err
	}
	var sd uintptr
	r, _, e := procConvertSDDL.Call(uintptr(unsafe.Pointer(sdw)), 1, uintptr(unsafe.Pointer(&sd)), 0)
	if r == 0 {
		return nil, e
	}
	l := &pipeListener{name: name, sd: sd, first: true}
	l.sa = &syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), SecurityDescriptor: sd}
	// Create the first instance now so that failure is reported to the caller.
	h, err := l.newInstance()
	if err != nil {
		l.free()
		return nil, err
	}
	l.pending = h
	return l, nil
}

func (l *pipeListener) free() {
	if l.sd != 0 {
		procLocalFree.Call(l.sd)
		l.sd = 0
	}
}

func (l *pipeListener) newInstance() (syscall.Handle, error) {
	name, err := syscall.UTF16PtrFromString(l.name)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	flags := uint32(pipeAccessDuplex)
	l.mu.Lock()
	if l.first {
		flags |= fileFlagFirstInstance
		l.first = false
	}
	l.mu.Unlock()
	h, _, e := procCreateNamedPipe.Call(uintptr(unsafe.Pointer(name)), uintptr(flags),
		uintptr(pipeTypeByte|pipeReadModeByte|pipeWait|pipeRejectRemoteClients), uintptr(pipeUnlimitedInstances),
		64<<10, 64<<10, 0, uintptr(unsafe.Pointer(l.sa)))
	if syscall.Handle(h) == syscall.InvalidHandle {
		return syscall.InvalidHandle, e
	}
	return syscall.Handle(h), nil
}

// Accept waits for one client. It returns an error after Close.
func (l *pipeListener) Accept() (io.ReadWriteCloser, error) {
	l.mu.Lock()
	h := l.pending
	l.pending = 0
	l.mu.Unlock()
	if h == 0 {
		var err error
		if h, err = l.newInstance(); err != nil {
			return nil, err
		}
	}
	if r, _, err := procConnectNamedPipe.Call(uintptr(h), 0); r == 0 && err != errorPipeConnected {
		_ = syscall.CloseHandle(h)
		return nil, err
	}
	l.mu.Lock()
	done := l.done
	l.mu.Unlock()
	if done {
		_ = disconnect(h)
		_ = syscall.CloseHandle(h)
		return nil, errors.New("listener closed")
	}
	return &pipeConn{File: os.NewFile(uintptr(h), l.name), h: h}, nil
}

// Close stops accepting. A blocked ConnectNamedPipe is woken by connecting once.
func (l *pipeListener) Close() error {
	l.mu.Lock()
	if l.done {
		l.mu.Unlock()
		return nil
	}
	l.done = true
	pending := l.pending
	l.pending = 0
	l.mu.Unlock()
	if pending != 0 {
		_ = syscall.CloseHandle(pending)
	}
	if f, err := os.OpenFile(l.name, os.O_RDWR, 0); err == nil { // wakes Accept
		_ = f.Close()
	}
	l.free()
	return nil
}

type pipeConn struct {
	*os.File
	h syscall.Handle
}

// Close ends the connection even when another goroutine is blocked reading from it.
//
// The handle is synchronous, so a pending ReadFile holds the file object: File.Close
// would wait for it forever (the read only returns when the client sends or leaves), and
// so would DisconnectNamedPipe. CancelIoEx aborts the pending call from outside; it is
// repeated until the file is closed because a read can start between two attempts.
// DisconnectNamedPipe is deliberately not used: it discards data the client has not read
// yet, which would lose the reply that precedes a close. Closing the server's handle lets
// the client read what is buffered and then see end of file.
func (c *pipeConn) Close() error {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop: // the handle may be gone (and its number reused): never touch it again
				return
			default:
			}
			procCancelIoEx.Call(uintptr(c.h), 0)
			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}()
	err := c.File.Close()
	close(stop)
	return err
}

var (
	procCancelIoEx = kernel32.NewProc("CancelIoEx")
	procDisconnect = kernel32.NewProc("DisconnectNamedPipe")
)

func disconnect(h syscall.Handle) error {
	r, _, e := procDisconnect.Call(uintptr(h))
	if r == 0 {
		return e
	}
	return nil
}

// Dial opens the pipe, waiting briefly if every instance is busy.
func Dial(name string) (io.ReadWriteCloser, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		f, err := os.OpenFile(name, os.O_RDWR, 0)
		if err == nil {
			return f, nil
		}
		var errno syscall.Errno
		if !errors.As(err, &errno) || errno != errorPipeBusy || time.Now().After(deadline) {
			return nil, err
		}
		p, _ := syscall.UTF16PtrFromString(name)
		procWaitNamedPipe.Call(uintptr(unsafe.Pointer(p)), 500)
	}
}
