package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	binPathEnvVar         = "HERDR_BIN_PATH"
	commandTimeoutEnvVar  = "HERDR_COMMAND_TIMEOUT"
	fallbackBin           = "herdr"
	defaultCommandTimeout = 5 * time.Second
)

// Client execs the herdr CLI and parses its JSON responses. Every method
// mirrors one herdr invocation toggle.sh makes.
type Client struct {
	bin            string
	commandTimeout time.Duration
}

// NewClient resolves the herdr binary from $HERDR_BIN_PATH, falling back to
// a herdr found on PATH when the env var is unset.
func NewClient() *Client {
	bin := os.Getenv(binPathEnvVar)
	if bin == "" {
		bin = fallbackBin
	}
	return &Client{bin: bin, commandTimeout: commandTimeoutFromEnv()}
}

func commandTimeoutFromEnv() time.Duration {
	raw := os.Getenv(commandTimeoutEnvVar)
	if raw == "" {
		return defaultCommandTimeout
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return defaultCommandTimeout
	}
	return timeout
}

func (c *Client) timeout() time.Duration {
	if c.commandTimeout <= 0 {
		return defaultCommandTimeout
	}
	return c.commandTimeout
}

// run execs the herdr binary with args, returning stdout and stderr separately
// so callers can parse stdout on success and report both on failure. Each herdr
// subprocess gets its own bounded context derived from the caller's context.
//
//nolint:gosec // c.bin is the plugin-configured herdr binary (HERDR_BIN_PATH or PATH lookup), the exact indirection this wrapper exists to perform.
func (c *Client) run(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	timeout := c.timeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.bin, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("%w after %s", ctx.Err(), timeout)
	}
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
func (c *Client) PluginPaneOpen(ctx context.Context, pluginID, entrypoint, cwd string) (string, error) {
	stdout, stderr, err := c.run(ctx,
		"plugin", "pane", "open",
		"--plugin", pluginID,
		"--entrypoint", entrypoint,
		"--placement", "overlay",
		"--cwd", cwd,
		"--focus",
	)
	if err != nil {
		return "", herdrError("herdr plugin pane open", stdout, stderr, err)
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
func (c *Client) PaneExists(ctx context.Context, paneID string) bool {
	_, _, err := c.run(ctx, "pane", "get", paneID)
	return err == nil
}

// TabSibling returns a pane_id sharing paneID's tab (any pane other than
// itself), or "" when it is alone in its tab, `pane layout` fails, or the
// response is malformed.
func (c *Client) TabSibling(ctx context.Context, paneID string) string {
	stdout, _, err := c.run(ctx, "pane", "layout", "--pane", paneID)
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
func (c *Client) ZoomOn(ctx context.Context, paneID string) error {
	stdout, stderr, err := c.run(ctx, "pane", "zoom", paneID, "--on")
	if err != nil {
		return herdrError("herdr pane zoom", stdout, stderr, err)
	}
	return nil
}

// PluginPaneFocus runs `plugin pane focus <id>`, reporting failure via the
// returned error.
func (c *Client) PluginPaneFocus(ctx context.Context, paneID string) error {
	stdout, stderr, err := c.run(ctx, "plugin", "pane", "focus", paneID)
	if err != nil {
		return herdrError("herdr plugin pane focus", stdout, stderr, err)
	}
	return nil
}

// PaneResize runs `pane resize --direction <direction> --amount <amount>
// --pane <id>`. Best-effort: callers are expected to ignore the returned
// error, since sizing is cosmetic and must never fail the toggle.
func (c *Client) PaneResize(ctx context.Context, paneID, direction, amount string) error {
	stdout, stderr, err := c.run(ctx, "pane", "resize", "--direction", direction, "--amount", amount, "--pane", paneID)
	if err != nil {
		return herdrError("herdr pane resize", stdout, stderr, err)
	}
	return nil
}

// PluginPaneClose runs `plugin pane close <id>`. Best-effort: callers are
// expected to ignore the returned error and clear their registry entry
// regardless of whether the close call succeeds.
func (c *Client) PluginPaneClose(ctx context.Context, paneID string) error {
	stdout, stderr, err := c.run(ctx, "plugin", "pane", "close", paneID)
	if err != nil {
		return herdrError("herdr plugin pane close", stdout, stderr, err)
	}
	return nil
}

func herdrError(operation string, stdout, stderr []byte, err error) error {
	output := capturedOutput(stdout, stderr)
	if output == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %s", operation, output)
}
