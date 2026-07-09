package main

import (
	"fmt"
	"io"
	"os"

	"github.com/maro114510/herdr-toggle-popup/internal/onpaneclosed"
	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
	"github.com/maro114510/herdr-toggle-popup/internal/toggle"
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
		_, _ = fmt.Fprint(stderr, usage)
		return 1
	}

	switch args[0] {
	case cmdToggle:
		return toggle.Run(args[1:], stdout, stderr)
	case cmdOnPaneClosed:
		return onpaneclosed.Run(args[1:], stdout, stderr)
	case cmdPopupShell:
		return popupshell.Run(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		_, _ = fmt.Fprint(stderr, usage)
		return 1
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
