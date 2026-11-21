//go:build !windows

package sshagenthost

import "errors"

func Connect(executable, dataDir string, secret []byte) (*Client, error) {
	return nil, errors.New("the independent SSH Agent is available on Windows")
}
