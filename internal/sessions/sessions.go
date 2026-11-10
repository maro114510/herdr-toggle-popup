// Package sessions lists saved toggle-popup tmux sessions.
package sessions

import "io"

// Run is the sessions subcommand.
func Run(_ []string, _ io.Writer, _ io.Writer) int { return 1 }
