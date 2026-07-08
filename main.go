package main

import (
	"fmt"
	"io"
	"os"
)

const (
	cmdToggle       = "toggle"
	cmdOnPaneClosed = "on-pane-closed"
	cmdPopupShell   = "popup-shell"
)

const usage = `Usage: toggle-popup <command>

Commands:
  toggle           Toggle the popup pane
  on-pane-closed   Handle a pane-closed event
  popup-shell      Run the shell inside the popup pane
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 1
	}

	switch args[0] {
	case cmdToggle, cmdOnPaneClosed, cmdPopupShell:
		fmt.Fprintf(stderr, "%s: not implemented\n", args[0])
		return 1
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return 1
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
