// Package sessions implements the saved-shell management commands: sessions, reset-current, and
// cleanup. They read and change only the plugin's dedicated tmux server.
package sessions

import "io"

// RunList implements the `sessions` subcommand.
func RunList(_ []string, _, _ io.Writer) int {
	return 1
}
