//go:build !windows

package pipe

import (
	"io"
	"net"
	"os"
	"time"
)

type unixListener struct {
	l    net.Listener
	path string
}

// Listen creates the control socket at path (mode 0600). A stale socket file from
// a crashed run is replaced; a live one makes Listen fail.
func Listen(path string) (Listener, error) {
	if c, err := net.DialTimeout("unix", path, 200*time.Millisecond); err == nil {
		_ = c.Close()
		return nil, &net.OpError{Op: "listen", Net: "unix", Err: os.ErrExist}
	}
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return nil, err
	}
	return &unixListener{l: l, path: path}, nil
}

func (u *unixListener) Accept() (io.ReadWriteCloser, error) { return u.l.Accept() }
func (u *unixListener) Close() error {
	err := u.l.Close()
	_ = os.Remove(u.path)
	return err
}

// Dial connects to a control socket.
func Dial(path string) (io.ReadWriteCloser, error) {
	return net.DialTimeout("unix", path, 2*time.Second)
}

// DefaultAddress is where the CLI looks when nothing else is given.
func DefaultAddress() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d + "/clipbridge.v1.sock"
	}
	return os.TempDir() + "/clipbridge.v1.sock"
}
