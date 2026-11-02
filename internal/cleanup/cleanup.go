// Package cleanup implements the `cleanup` subcommand: inspecting saved popup sessions on the
// dedicated tmux server and deleting only the ones a user selects explicitly.
package cleanup

import (
	"fmt"
	"io"
)

const usage = `usage: toggle-popup cleanup list
       toggle-popup cleanup delete [--force] <session>...`

// Run implements the `cleanup` subcommand.
func Run(args []string, stdout, stderr io.Writer) int {
	_ = args
	_ = stdout
	_, _ = fmt.Fprintln(stderr, usage)
	return 1
}
