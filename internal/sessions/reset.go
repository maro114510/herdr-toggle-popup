package sessions

import "io"

// RunResetCurrent implements the `reset-current` subcommand.
func RunResetCurrent(_ []string, _, _ io.Writer) int {
	return 1
}
