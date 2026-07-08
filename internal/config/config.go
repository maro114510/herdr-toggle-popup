// Package config reads the plugin's opt-in settings from
// $HERDR_PLUGIN_CONFIG_DIR/config.toml: popup scoping mode and per-entrypoint resize steps.
package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	configDirEnvVar = "HERDR_PLUGIN_CONFIG_DIR"
	defaultScope    = "workspace"

	// stepFields is the number of colon-separated fields a size step splits into: direction,
	// amount, and count (with any extra colons folded into count, matching bash's `read`).
	stepFields = 3
)

// Config is the subset of config.toml the plugin reads. Missing env var, directory, file, or key
// all degrade to the same defaults toggle.sh uses; a malformed file is treated as absent rather
// than surfaced as an error, since scoping/sizing is best-effort and must never fail the toggle.
type Config struct {
	Scope     string
	PopupSize map[string]string
}

// Load reads scope and popup_size.<entrypoint> from $HERDR_PLUGIN_CONFIG_DIR/config.toml.
func Load() Config {
	def := Config{Scope: defaultScope, PopupSize: nil}

	dir := os.Getenv(configDirEnvVar)
	if dir == "" {
		return def
	}

	var parsed struct {
		Scope     string            `toml:"scope"`
		PopupSize map[string]string `toml:"popup_size"`
	}
	path := filepath.Clean(filepath.Join(dir, "config.toml"))
	if _, err := toml.DecodeFile(path, &parsed); err != nil {
		return def
	}

	scope := parsed.Scope
	if scope == "" {
		scope = defaultScope
	}
	return Config{Scope: scope, PopupSize: parsed.PopupSize}
}

// PopupSizeSteps returns the raw popup_size.<entrypoint> value, or "" when absent.
func (c Config) PopupSizeSteps(entrypoint string) string {
	return c.PopupSize[entrypoint]
}

// SizeStep is one direction/amount/count resize step applied against a newly opened popup pane.
type SizeStep struct {
	Direction string
	Amount    string
	Count     int
}

// ParseSizeSteps splits raw (a popup_size.<entrypoint> value) into steps, mirroring
// _toggle_apply_size in toggle.sh: space-separated "direction:amount:count" entries, each
// validated independently. A malformed step is skipped; the rest still apply.
func ParseSizeSteps(raw string) []SizeStep {
	amountPattern := regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
	countPattern := regexp.MustCompile(`^[1-9][0-9]*$`)

	var steps []SizeStep
	for field := range strings.FieldsSeq(raw) {
		direction, amount, countStr := splitStep(field)

		switch direction {
		case "left", "right", "up", "down":
		default:
			continue
		}
		if !amountPattern.MatchString(amount) {
			continue
		}
		if !countPattern.MatchString(countStr) {
			continue
		}

		count, err := strconv.Atoi(countStr)
		if err != nil {
			continue
		}
		steps = append(steps, SizeStep{Direction: direction, Amount: amount, Count: count})
	}
	return steps
}

// splitStep mirrors bash's `IFS=':' read -r direction amount count <<<"${step}"`: fields beyond
// the third are folded into count, and fields past the end of the input are "".
func splitStep(field string) (direction, amount, count string) {
	parts := strings.SplitN(field, ":", stepFields)
	if len(parts) > 0 {
		direction = parts[0]
	}
	if len(parts) > 1 {
		amount = parts[1]
	}
	if len(parts) > stepFields-1 {
		count = parts[2]
	}
	return direction, amount, count
}
