// Command cbridge is the Windows CLI. It will talk to the running tray app
// over the named pipe \\.\pipe\clipbridge.v1 (docs/api-spec.md §8–§9). The
// pipe server is not implemented yet, so only "version" works today.
package main

import (
	"fmt"
	"os"

	"nightclip/core/facade"
)

const (
	exitOK       = 0
	exitUsage    = 2
	exitNoServer = 3
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cbridge <version|status|peers|clip|file|inbox|transfers|doctor>")
		return exitUsage
	}
	switch args[0] {
	case "version":
		fmt.Printf("cbridge (core %s, protocol clipbridge/v1)\n", facade.Version)
		return exitOK
	case "status", "peers", "clip", "file", "inbox", "transfers", "doctor":
		fmt.Fprintln(os.Stderr, "cbridge: named-pipe client not implemented yet; the tray app is not reachable")
		return exitNoServer
	default:
		fmt.Fprintf(os.Stderr, "cbridge: unknown command %q\n", args[0])
		return exitUsage
	}
}
