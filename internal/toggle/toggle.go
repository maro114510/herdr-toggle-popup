// Package toggle opens the shell in a native full-screen Herdr popup.
package toggle

import (
	"context"
	"fmt"
	"io"

	"github.com/maro114510/herdr-toggle-popup/internal/config"
	"github.com/maro114510/herdr-toggle-popup/internal/herdr"
)

const pluginID = "maro114510.toggle-popup"

// Run opens a native Herdr popup for entrypoint.
// While it is visible, Herdr routes the close key to the attached tmux client.
// popupshell detaches only that client, which closes the popup and keeps its tmux session alive.
// Once closed, the close key reopens the same scoped session.
func Run(args []string, stdout, stderr io.Writer) int {
	_ = stdout
	if len(args) != 1 || args[0] == "" {
		_, _ = fmt.Fprintln(stderr, "usage: toggle-popup toggle <entrypoint>")
		return 1
	}

	cwd := herdr.ContextField("focused_pane_cwd")
	if cwd == "" {
		_, _ = fmt.Fprintln(stderr, "toggle: could not determine the focused pane's cwd")
		return 1
	}

	cfg := config.Load()
	for _, warning := range cfg.Warnings {
		_, _ = fmt.Fprintf(stderr, "toggle: %s\n", warning)
	}

	ctx := context.Background()
	client := herdr.NewClient()
	if err := client.PluginPopupOpen(ctx, pluginID, args[0], cwd, cfg.Width, cfg.Height); err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: failed to open popup: %v\n", err)
		return 1
	}
	return 0
}
