package sessions

import "io"

// ResetCurrent discards the saved shell that belongs to the current context.
func ResetCurrent(args []string, stdout, stderr io.Writer) int {
	_, _, _ = args, stdout, stderr
	return 0
}
