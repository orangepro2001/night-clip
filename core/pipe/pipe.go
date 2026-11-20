// Package pipe listens on a local endpoint that only the current user can reach:
// a Windows named pipe (owner-only DACL, FILE_FLAG_FIRST_PIPE_INSTANCE, remote
// clients refused) or a Unix domain socket (mode 0600). The SSH agent serves on it.
package pipe

import "io"

// Listener accepts connections on a pipe or socket.
type Listener interface {
	Accept() (io.ReadWriteCloser, error)
	Close() error
}
