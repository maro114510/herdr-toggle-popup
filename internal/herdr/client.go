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

// PluginPopupOpen opens a native Herdr popup sized to the outer width and height. Width and
// height are the values Herdr's --width and --height accept: a cell count or a percentage such as
// "80%"; the caller falls back to "100%" when configuration is absent or unusable. Native popups
// own an independent terminal surface instead of a zoomed pane, so Herdr sizes their PTY to the
// area inside its border before starting the command. The API intentionally returns a plain
// {"type":"ok"} response: a popup has no public pane ID and is not a plugin pane.
func (c *Client) PluginPopupOpen(ctx context.Context, pluginID, entrypoint, cwd, width, height string) error {
	stdout, stderr, err := c.run(ctx,
		"plugin", "pane", "open",
		"--plugin", pluginID,
		"--entrypoint", entrypoint,
		"--placement", "popup",
		"--width", width,
		"--height", height,
		"--cwd", cwd,
		"--focus",
	)
	if err != nil {
		return herdrError("herdr plugin pane open", stdout, stderr, err)
	}

	var resp struct {
		Result struct {
			Type string `json:"type"`
		} `json:"result"`
	}
	if jsonErr := json.Unmarshal(stdout, &resp); jsonErr != nil || resp.Result.Type != "ok" {
		return fmt.Errorf("herdr plugin pane open: expected native popup success response: %s", capturedOutput(stdout, stderr))
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
