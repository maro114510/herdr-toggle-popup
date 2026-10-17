// Package config reads the plugin's opt-in settings from
// $HERDR_PLUGIN_CONFIG_DIR/config.toml: popup scoping mode, native popup dimensions, and
// per-entrypoint resize steps.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	configDirEnvVar = "HERDR_PLUGIN_CONFIG_DIR"
	configFile      = "config.toml"
	defaultScope    = "workspace"
	scopeDirectory  = "directory"
	scopeTab        = "tab"

	// stepFields is the number of colon-separated fields a size step splits into: direction,
	// amount, and count (with any extra colons folded into count, matching bash's `read`).
	stepFields = 3

	// defaultPopupDimension preserves the historical full-screen popup when width or height is
	// unset. Native popup dimensions follow Herdr's own parser: an integer cell count from
	// minPopupCells to maxPopupCells, or an integer percentage from minPopupPercent to
	// maxPopupPercent.
	defaultPopupDimension = "100%"
	minPopupPercent       = 1
	maxPopupPercent       = 100
	minPopupCells         = 0
	maxPopupCells         = 65535

	// baseTen, percentBitSize, and cellBitSize express the unsigned-integer widths Herdr parses
	// dimensions with (u8 for a percentage, u16 for a cell count).
	baseTen        = 10
	percentBitSize = 8
	cellBitSize    = 16
)

// Config is the subset of config.toml the plugin reads. A missing env var, directory, or file
// degrades silently to the same defaults toggle.sh uses, since that is the normal opt-out. A file
// that exists but cannot be read or decoded also degrades to the defaults, but records a warning
// in Warnings so the caller can tell the user why their settings were not applied; the failure
// never fails the toggle.
type Config struct {
	Scope     string
	PopupSize map[string]string

	// Width and Height are the native popup outer dimensions to pass to Herdr's --width and
	// --height, each defaulting to 100%. They are distinct from PopupSize, the legacy
	// resize-step configuration, and may be either a percentage ("80%") or a cell count ("20").
	Width  string
	Height string

	// Warnings lists configuration values or files that were unusable and replaced by a default,
	// for the caller to surface to the user.
	Warnings []string
}

// Load reads scope, width, height, and popup_size.<entrypoint> from
// $HERDR_PLUGIN_CONFIG_DIR/config.toml.
func Load() Config {
	def := Config{
		Scope:     defaultScope,
		PopupSize: nil,
		Width:     defaultPopupDimension,
		Height:    defaultPopupDimension,
		Warnings:  nil,
	}

	dir := os.Getenv(configDirEnvVar)
	if dir == "" {
		return def
	}

	var parsed struct {
		Scope     string            `toml:"scope"`
		PopupSize map[string]string `toml:"popup_size"`
		Width     any               `toml:"width"`
		Height    any               `toml:"height"`
	}
	path := filepath.Clean(filepath.Join(dir, configFile))
	//nolint:gosec // path is HERDR_PLUGIN_CONFIG_DIR joined with the fixed configFile name.
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			def.Warnings = []string{fmt.Sprintf("could not read %s: %v; using defaults", configFile, readFailureReason(err))}
		}
		return def
	}
	if err := toml.Unmarshal(data, &parsed); err != nil {
		def.Warnings = []string{decodeFailureWarning(err)}
		return def
	}

	scope := parsed.Scope
	if scope == "" {
		scope = defaultScope
	}
	width, widthWarning := resolveDimension("width", parsed.Width)
	height, heightWarning := resolveDimension("height", parsed.Height)
	cfg := Config{
		Scope:     scope,
		PopupSize: parsed.PopupSize,
		Width:     width,
		Height:    height,
		Warnings:  nil,
	}
	for _, warning := range []string{widthWarning, heightWarning} {
		if warning != "" {
			cfg.Warnings = append(cfg.Warnings, warning)
		}
	}
	return cfg
}

// readFailureReason unwraps the path from a file error, so the warning names config.toml once
// instead of repeating the resolved path the error already carries.
func readFailureReason(err error) error {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		return pathErr.Err
	}
	return err
}

// decodeFailureWarning describes a config.toml that could not be decoded. A syntax error is
// reported as "malformed TOML at line N"; any other decode failure, such as a value of the wrong
// type, keeps the same shape without a line. Neither branch echoes the parser message, which can
// quote a fragment of the file.
func decodeFailureWarning(err error) string {
	if parseErr, ok := errors.AsType[toml.ParseError](err); ok {
		if parseErr.Position.Line > 0 {
			return fmt.Sprintf("could not parse %s: malformed TOML at line %d; using defaults", configFile, parseErr.Position.Line)
		}
		return fmt.Sprintf("could not parse %s: malformed TOML; using defaults", configFile)
	}
	return fmt.Sprintf("could not decode %s: invalid value; using defaults", configFile)
}

// resolveDimension validates a decoded width or height value, returning the string to pass to
// Herdr and an optional warning. An absent value (a nil TOML value) falls back to the 100%
// default silently; a present but unusable value falls back with a warning naming key.
func resolveDimension(key string, raw any) (value, warning string) {
	switch v := raw.(type) {
	case nil:
		return defaultPopupDimension, ""
	case string:
		return resolveDimensionString(key, v)
	case int64:
		// config.toml may spell a cell count as a TOML integer (height = 20), matching the Herdr
		// manifest. An integer never ends in '%', so it is always interpreted as cells.
		return resolveDimensionString(key, strconv.FormatInt(v, 10))
	default:
		return defaultPopupDimension, fmt.Sprintf("invalid %s: %s; using %s", key, dimensionHint(), defaultPopupDimension)
	}
}

func resolveDimensionString(key, value string) (resolved, warning string) {
	if !validateDimension(value) {
		return defaultPopupDimension, fmt.Sprintf("invalid %s %q: %s; using %s", key, value, dimensionHint(), defaultPopupDimension)
	}
	return value, ""
}

// validateDimension reports whether value is a native popup dimension Herdr accepts. It parses an
// integer cell count in Herdr's u16 range (minPopupCells to maxPopupCells), or an integer
// percentage in the u8 range that is also within minPopupPercent to maxPopupPercent. Parsing
// unsigned rejects signs and stray characters, so the accepted set is a subset of what Herdr
// resolves and an accepted value is always one Herdr can resolve.
func validateDimension(value string) bool {
	if percent, ok := strings.CutSuffix(value, "%"); ok {
		n, err := strconv.ParseUint(percent, baseTen, percentBitSize)
		return err == nil && n >= minPopupPercent && n <= maxPopupPercent
	}
	n, err := strconv.ParseUint(value, baseTen, cellBitSize)
	return err == nil && n >= minPopupCells && n <= maxPopupCells
}

func dimensionHint() string {
	return fmt.Sprintf("expected a cell count from %d to %d or a percentage from %d%% to %d%%",
		minPopupCells, maxPopupCells, minPopupPercent, maxPopupPercent)
}

// IsValidScope reports whether scope is a recognized value: "workspace", "directory", or "tab".
func IsValidScope(scope string) bool {
	switch scope {
	case defaultScope, scopeDirectory, scopeTab:
		return true
	default:
		return false
	}
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
