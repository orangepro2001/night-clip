package main

import (
	"io"
	"os"
	"path/filepath"

	"moonpassword/core/sshagent"
	"moonpassword/core/sshagenthost"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	var secret [32]byte
	if _, err := io.ReadFull(os.Stdin, secret[:]); err != nil {
		os.Exit(2)
	}
	_ = os.Stdin.Close()
	defer clear(secret[:])

	var known []string
	if home, err := os.UserHomeDir(); err == nil {
		known = append(known, filepath.Join(home, ".ssh", "known_hosts"), filepath.Join(home, ".ssh", "known_hosts2"))
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		known = append(known, filepath.Join(pd, "ssh", "ssh_known_hosts"))
	}
	host := sshagenthost.NewHost(sshagent.NewKnownHosts(known...))
	if err := host.WatchSession(os.Args[1]); err != nil {
		os.Exit(2)
	}
	listener, err := sshagenthost.ListenControl(os.Args[1])
	if err != nil {
		os.Exit(2)
	}
	if err := host.Serve(listener, secret[:]); err != nil {
		os.Exit(2)
	}
}
