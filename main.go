package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/maro114510/herdr-toggle-popup/internal/onpaneclosed"
	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
	"github.com/maro114510/herdr-toggle-popup/internal/toggle"
)

const (
	cmdToggle       = "toggle"
	cmdOnPaneClosed = "on-pane-closed"
	cmdPopupShell   = "popup-shell"
	cmdVersion      = "version"
)

const usage = `Usage: toggle-popup <command>

Commands:
  toggle           Toggle the popup pane
  on-pane-closed   Handle a pane-closed event
  popup-shell      Run the shell inside the popup pane
  version          Print the toggle-popup version
`

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

// errDispatchFailed signals that a dispatched subcommand already reported its own error to
// stderr and returned non-zero; cobra only needs it to select a non-zero exit code, so the
// message itself is never printed (SilenceErrors/SilenceUsage on the dispatch commands suppress
// cobra's own error/usage output for it).
var errDispatchFailed = errors.New("dispatch failed")

// dispatchFunc is the signature shared by internal/toggle.Run, internal/onpaneclosed.Run, and
// internal/popupshell.Run.
type dispatchFunc func(args []string, stdout, stderr io.Writer) int

func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	root := new(cobra.Command)
	root.Use = "toggle-popup"
	root.Short = "herdr toggle-popup plugin CLI"
	root.SilenceUsage = true
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		// cobra's own usage-on-error output goes through OutOrStderr, which follows
		// SetOut once it's been called (see root.SetOut below), so it must be written
		// here via ErrOrStderr instead of relying on cmd.Usage()/SilenceUsage=false.
		_, _ = fmt.Fprint(cmd.ErrOrStderr(), usage)
		return errors.New("no command specified")
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(stdout)
	root.SetErr(stderr)

	root.AddCommand(
		newDispatchCmd(cmdToggle, "Toggle the popup pane", toggle.Run),
		newDispatchCmd(cmdOnPaneClosed, "Handle a pane-closed event", onpaneclosed.Run),
		newDispatchCmd(cmdPopupShell, "Run the shell inside the popup pane", popupshell.Run),
		newVersionCmd(),
	)

	return root
}

// newDispatchCmd wraps a dispatchFunc as a cobra command that forwards its raw args unchanged,
// leaving argument parsing and validation entirely to the wrapped function so behavior stays
// identical to the pre-cobra dispatch. SilenceErrors/SilenceUsage are set because the wrapped
// function already writes its own error message to stderr.
func newDispatchCmd(use, short string, run dispatchFunc) *cobra.Command {
	cmd := new(cobra.Command)
	cmd.Use = use
	cmd.Short = short
	cmd.DisableFlagParsing = true
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if code := run(args, cmd.OutOrStdout(), cmd.ErrOrStderr()); code != 0 {
			return errDispatchFailed
		}
		return nil
	}
	return cmd
}

func newVersionCmd() *cobra.Command {
	cmd := new(cobra.Command)
	cmd.Use = cmdVersion
	cmd.Short = "Print the toggle-popup version"
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), version)
		return nil
	}
	return cmd
}

func run(args []string, stdout, stderr io.Writer) int {
	root := newRootCmd(stdout, stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
