package herdr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	binPathEnvVar = "HERDR_BIN_PATH"
	fallbackBin   = "herdr"
)

// Client execs the herdr CLI and parses its JSON responses. Every method
// mirrors one herdr invocation toggle.sh makes.
type Client struct {
	bin string
}

// NewClient resolves the herdr binary from $HERDR_BIN_PATH, falling back to
// a herdr found on PATH when the env var is unset.
func NewClient() *Client {
	bin := os.Getenv(binPathEnvVar)
	if bin == "" {
		bin = fallbackBin
	}
	return &Client{bin: bin}
}

// run execs the herdr binary with args, returning stdout and stderr
// separately so callers can parse stdout on success and report both on
// failure.
//
//nolint:gosec // c.bin is the plugin-configured herdr binary (HERDR_BIN_PATH or PATH lookup), the exact indirection this wrapper exists to perform.
func (c *Client) run(args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.Command(c.bin, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}

// capturedOutput joins stdout and stderr for error messages, mirroring
// toggle.sh's `2>&1` capture.
func capturedOutput(stdout, stderr []byte) string {
	return strings.TrimSpace(string(stdout) + string(stderr))
}

// PluginPaneOpen runs `plugin pane open` and returns the opened pane's id.
// It errors, with the captured output, on a non-zero exit or a response
// missing .result.plugin_pane.pane.pane_id.
func (c *Client) PluginPaneOpen(pluginID, entrypoint, cwd string) (string, error) {
	stdout, stderr, err := c.run(
		"plugin", "pane", "open",
		"--plugin", pluginID,
		"--entrypoint", entrypoint,
		"--placement", "overlay",
		"--cwd", cwd,
		"--focus",
	)
	if err != nil {
		return "", fmt.Errorf("herdr plugin pane open: %s", capturedOutput(stdout, stderr))
	}

	var resp struct {
		Result struct {
			PluginPane struct {
				Pane struct {
					PaneID string `json:"pane_id"`
				} `json:"pane"`
			} `json:"plugin_pane"`
		} `json:"result"`
	}
	if jsonErr := json.Unmarshal(stdout, &resp); jsonErr != nil || resp.Result.PluginPane.Pane.PaneID == "" {
		return "", fmt.Errorf("herdr plugin pane open: could not determine the opened pane's id: %s", capturedOutput(stdout, stderr))
	}
	return resp.Result.PluginPane.Pane.PaneID, nil
}

// PaneExists reports whether `pane get <id>` exits successfully.
func (c *Client) PaneExists(paneID string) bool {
	_, _, err := c.run("pane", "get", paneID)
	return err == nil
}

// TabSibling returns a pane_id sharing paneID's tab (any pane other than
// itself), or "" when it is alone in its tab, `pane layout` fails, or the
// response is malformed.
func (c *Client) TabSibling(paneID string) string {
	stdout, _, err := c.run("pane", "layout", "--pane", paneID)
	if err != nil {
		return ""
	}

	var resp struct {
		Result struct {
			Layout struct {
				Panes []struct {
					PaneID string `json:"pane_id"`
				} `json:"panes"`
			} `json:"layout"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout, &resp); err != nil {
		return ""
	}
	for _, pane := range resp.Result.Layout.Panes {
		if pane.PaneID != paneID {
			return pane.PaneID
		}
	}
	return ""
}

// ZoomOn runs `pane zoom <id> --on`, reporting failure via the returned error.
func (c *Client) ZoomOn(paneID string) error {
	_, stderr, err := c.run("pane", "zoom", paneID, "--on")
	if err != nil {
		return fmt.Errorf("herdr pane zoom: %s", strings.TrimSpace(string(stderr)))
	}
	return nil
}

// PluginPaneFocus runs `plugin pane focus <id>`, reporting failure via the
// returned error.
func (c *Client) PluginPaneFocus(paneID string) error {
	_, stderr, err := c.run("plugin", "pane", "focus", paneID)
	if err != nil {
		return fmt.Errorf("herdr plugin pane focus: %s", strings.TrimSpace(string(stderr)))
	}
	return nil
}

// PaneResize runs `pane resize --direction <direction> --amount <amount>
// --pane <id>`. Best-effort: callers are expected to ignore the returned
// error, since sizing is cosmetic and must never fail the toggle.
func (c *Client) PaneResize(paneID, direction, amount string) error {
	_, stderr, err := c.run("pane", "resize", "--direction", direction, "--amount", amount, "--pane", paneID)
	if err != nil {
		return fmt.Errorf("herdr pane resize: %s", strings.TrimSpace(string(stderr)))
	}
	return nil
}

// PluginPaneClose runs `plugin pane close <id>`. Best-effort: callers are
// expected to ignore the returned error and clear their registry entry
// regardless of whether the close call succeeds.
func (c *Client) PluginPaneClose(paneID string) error {
	_, stderr, err := c.run("plugin", "pane", "close", paneID)
	if err != nil {
		return fmt.Errorf("herdr plugin pane close: %s", strings.TrimSpace(string(stderr)))
	}
	return nil
}
