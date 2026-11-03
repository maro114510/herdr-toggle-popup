package sessions

import "io"

// RunCleanup implements the `cleanup` subcommand.
func RunCleanup(_ []string, _, _ io.Writer) int {
	return 1
}
