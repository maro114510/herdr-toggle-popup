// Package doctor prints safe, non-mutating diagnostics for support requests.
package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	herdrBinPathEnvVar   = "HERDR_BIN_PATH"
	pluginRootEnvVar     = "HERDR_PLUGIN_ROOT"
	configDirEnvVar      = "HERDR_PLUGIN_CONFIG_DIR"
	stateDirEnvVar       = "HERDR_PLUGIN_STATE_DIR"
	defaultHerdrBin      = "herdr"
	tmuxBin              = "tmux"
	manifestFile         = "herdr-plugin.toml"
	configFile           = "config.toml"
	stateFile            = "popups.json"
	supportedStateFormat = 1
)

type manifest struct {
	Version string `toml:"version"`
}

type configFileData struct {
	Scope     string            `toml:"scope"`
	PopupSize map[string]string `toml:"popup_size"`
}

type stateRegistry struct {
	Version int                       `json:"version"`
	Popups  map[string]map[string]any `json:"popups"`
}

// Run prints diagnostics and returns a process-style exit code. Missing tools or plugin
// environment values are reported as diagnostic statuses rather than command failures.
func Run(args []string, stdout, stderr io.Writer, version string) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "doctor: unexpected arguments")
		return 1
	}

	_, _ = fmt.Fprintln(stdout, "toggle-popup diagnostics")

	manifestVersion := diagnosePluginRoot(stdout)
	diagnoseVersion(stdout, version, manifestVersion)
	diagnoseExecutable(stdout, "herdr", herdrCandidate())
	diagnoseExecutable(stdout, "tmux", tmuxBin)
	diagnoseConfig(stdout)
	diagnoseState(stdout)

	return 0
}

func herdrCandidate() string {
	if bin := os.Getenv(herdrBinPathEnvVar); bin != "" {
		return bin
	}
	return defaultHerdrBin
}

func diagnoseExecutable(stdout io.Writer, name, candidate string) {
	path, err := exec.LookPath(candidate)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "%s: missing (%s not executable or not on PATH)\n", name, candidateName(candidate))
		return
	}
	_, _ = fmt.Fprintf(stdout, "%s: ok (%s)\n", name, path)
}

func candidateName(candidate string) string {
	if filepath.Base(candidate) == candidate {
		return candidate
	}
	return filepath.Base(candidate)
}

func diagnosePluginRoot(stdout io.Writer) string {
	root := os.Getenv(pluginRootEnvVar)
	if root == "" {
		_, _ = fmt.Fprintf(stdout, "plugin root: missing (%s not set)\n", pluginRootEnvVar)
		return ""
	}
	//nolint:gosec // HERDR_PLUGIN_ROOT is the plugin-owned install root that diagnostics are explicitly expected to inspect.
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		_, _ = fmt.Fprintln(stdout, "plugin root: error (directory not readable)")
		return ""
	}

	path := filepath.Clean(filepath.Join(root, manifestFile))
	var parsed manifest
	if _, err := toml.DecodeFile(path, &parsed); err != nil || parsed.Version == "" {
		_, _ = fmt.Fprintln(stdout, "plugin root: ok (manifest missing or unreadable)")
		return ""
	}

	_, _ = fmt.Fprintf(stdout, "plugin root: ok (%s present)\n", manifestFile)
	return parsed.Version
}

func diagnoseVersion(stdout io.Writer, binaryVersion, manifestVersion string) {
	switch {
	case manifestVersion == "":
		_, _ = fmt.Fprintf(stdout, "version: ok (binary=%s, manifest=unknown)\n", binaryVersion)
	case binaryVersion == "dev" || binaryVersion == manifestVersion:
		_, _ = fmt.Fprintf(stdout, "version: ok (binary=%s, manifest=%s)\n", binaryVersion, manifestVersion)
	default:
		_, _ = fmt.Fprintf(stdout, "version: warning (binary=%s, manifest=%s)\n", binaryVersion, manifestVersion)
	}
}

func diagnoseConfig(stdout io.Writer) {
	dir := os.Getenv(configDirEnvVar)
	if dir == "" {
		_, _ = fmt.Fprintf(stdout, "config: missing (%s not set)\n", configDirEnvVar)
		return
	}

	path := filepath.Clean(filepath.Join(dir, configFile))
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(stdout, "config: missing (%s not found)\n", configFile)
			return
		}
		_, _ = fmt.Fprintln(stdout, "config: error (file not readable)")
		return
	}

	var parsed configFileData
	if _, err := toml.DecodeFile(path, &parsed); err != nil {
		_, _ = fmt.Fprintln(stdout, "config: error (parse failed)")
		return
	}
	if parsed.Scope == "" {
		parsed.Scope = "workspace"
	}
	_, _ = fmt.Fprintf(stdout, "config: ok (scope=%s, popup_size entries=%d)\n", parsed.Scope, len(parsed.PopupSize))
}

func diagnoseState(stdout io.Writer) {
	dir := os.Getenv(stateDirEnvVar)
	if dir == "" {
		_, _ = fmt.Fprintf(stdout, "state: missing (%s not set)\n", stateDirEnvVar)
		return
	}

	path := filepath.Clean(filepath.Join(dir, stateFile))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(stdout, "state: missing (%s not found)\n", stateFile)
			return
		}
		_, _ = fmt.Fprintln(stdout, "state: error (file not readable)")
		return
	}

	var parsed stateRegistry
	if err := json.Unmarshal(data, &parsed); err != nil {
		_, _ = fmt.Fprintln(stdout, "state: error (parse failed)")
		return
	}
	if parsed.Version != supportedStateFormat || parsed.Popups == nil {
		_, _ = fmt.Fprintln(stdout, "state: error (unsupported format)")
		return
	}
	_, _ = fmt.Fprintf(stdout, "state: ok (version=%d, entries=%d)\n", parsed.Version, len(parsed.Popups))
}
