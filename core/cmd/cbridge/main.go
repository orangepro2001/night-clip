// Command cbridge controls a running ClipBridge over its local control channel
// (the named pipe \\.\pipe\clipbridge.v1 on Windows). Run "cbridge help".
package main

import (
	"io"
	"os"

	"nightclip/core/cli"
	"nightclip/core/pipe"
)

func main() {
	addr := os.Getenv("CBRIDGE_PIPE") // for tests and non-default setups
	if addr == "" {
		addr = pipe.DefaultAddress()
	}
	os.Exit(cli.Run(cli.Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Dial: func() (rwc io.ReadWriteCloser, err error) { return pipe.Dial(addr) },
	}, os.Args[1:]))
}
