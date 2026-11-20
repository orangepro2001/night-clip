//go:build !windows

package sshagent

// ClientChain is only implemented on Windows so far; elsewhere it reports
// the process id alone.
func ClientChain(pid uint32) []Proc {
	if pid == 0 {
		return []Proc{}
	}
	return []Proc{{PID: pid}}
}
