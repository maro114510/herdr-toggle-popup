// Package doctor prints safe, non-mutating diagnostics for support requests.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/maro114510/herdr-toggle-popup/internal/config"
	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

const (
	herdrBinPathEnvVar   = "HERDR_BIN_PATH"
	pluginRootEnvVar     = "HERDR_PLUGIN_ROOT"
	configDirEnvVar      = "HERDR_PLUGIN_CONFIG_DIR"
	stateDirEnvVar       = "HERDR_PLUGIN_STATE_DIR"
	defaultHerdrBin      = "herdr"
	tmuxBin              = "tmux"
	infocmpBin           = "infocmp"
	manifestFile         = "herdr-plugin.toml"
	configFile           = "config.toml"
	stateFile            = "popups.json"
	supportedStateFormat = 1

	diagnosticCommandTimeout = 3 * time.Second

	statusOK      = "ok"
	statusWarning = "warning"
	statusMissing = "missing"
	statusUnknown = "unknown"
	unsetValue    = "unset"
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
	herdrPath := diagnoseExecutable(stdout, "herdr", herdrCandidate())
	diagnoseToolVersion(stdout, "herdr version", herdrPath, "--version")
	tmuxPath := diagnoseExecutable(stdout, "tmux", tmuxBin)
	diagnoseToolVersion(stdout, "tmux version", tmuxPath, "-V")
	cfg := diagnoseConfig(stdout)
	diagnoseConnection(stdout, cfg.Scope)
	term := diagnoseTerminal(stdout)
	ms := newMsProbe()
	diagnoseMs(stdout, ms, term)
	diagnoseState(stdout)

	return 0
}

func herdrCandidate() string {
	if bin := os.Getenv(herdrBinPathEnvVar); bin != "" {
		return bin
	}
	return defaultHerdrBin
}

// diagnoseExecutable reports whether candidate resolves and returns the resolved path.
func diagnoseExecutable(stdout io.Writer, name, candidate string) string {
	path, err := exec.LookPath(candidate)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "%s: missing (%s not executable or not on PATH)\n", name, candidateName(candidate))
		return ""
	}
	_, _ = fmt.Fprintf(stdout, "%s: ok (%s)\n", name, path)
	return path
}

func candidateName(candidate string) string {
	return filepath.Base(candidate)
}

// diagnoseToolVersion prints the first line of a tool's version command, so a multi-line or
// chatty response never breaks the one-line diagnostic format.
func diagnoseToolVersion(stdout io.Writer, label, path string, args ...string) {
	if path == "" {
		_, _ = fmt.Fprintf(stdout, "%s: %s (tool not found)\n", label, statusUnknown)
		return
	}
	out, err := commandOutput(path, args...)
	if err != nil || out == "" {
		_, _ = fmt.Fprintf(stdout, "%s: %s (version command failed)\n", label, statusUnknown)
		return
	}
	value := strings.TrimPrefix(strings.TrimPrefix(firstLine(out), "herdr "), "tmux ")
	_, _ = fmt.Fprintf(stdout, "%s: %s (%s)\n", label, statusOK, sanitizeText(value))
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

// diagnoseConfig reports the file's status and returns the effective settings the plugin would
// use, including the documented fallbacks for an absent file or an invalid scope.
func diagnoseConfig(stdout io.Writer) config.Config {
	cfg := config.Load()
	if !config.IsValidScope(cfg.Scope) {
		_, _ = fmt.Fprintf(stdout, "scope: warning (%s is invalid; using workspace)\n", sanitizeText(cfg.Scope))
		cfg.Scope = "workspace"
	}

	dir := os.Getenv(configDirEnvVar)
	if dir == "" {
		_, _ = fmt.Fprintf(stdout, "config: missing (%s not set)\n", configDirEnvVar)
	} else {
		diagnoseConfigFile(stdout, dir)
	}

	_, _ = fmt.Fprintf(stdout, "popup size: width=%s, height=%s\n", cfg.Width, cfg.Height)
	return cfg
}

func diagnoseConfigFile(stdout io.Writer, dir string) {
	path := filepath.Clean(filepath.Join(dir, configFile))
	//nolint:gosec // path is HERDR_PLUGIN_CONFIG_DIR joined with the fixed configFile name.
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
	_, _ = fmt.Fprintf(stdout, "config: ok (scope=%s, popup_size entries=%d)\n", sanitizeText(parsed.Scope), len(parsed.PopupSize))
}

// diagnoseConnection reports the namespace, scope, and saved session the plugin would use and
// returns the session name, or "" when the identity cannot be resolved.
func diagnoseConnection(stdout io.Writer, scope string) string {
	identity, err := popupshell.ResolveIdentity(scope, popupshell.DefaultEntrypoint)
	namespace := identity.Namespace
	if namespace == "" {
		namespace = statusUnknown
	}
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "connection: %s (namespace=%s, scope=%s, session=unknown; %v)\n",
			statusWarning, namespace, sanitizeText(scope), err)
		return ""
	}

	_, _ = fmt.Fprintf(stdout, "connection: %s (namespace=%s, scope=%s, session=%s, entrypoint=%s)\n",
		statusOK, identity.Namespace, sanitizeText(scope), identity.Session, popupshell.DefaultEntrypoint)
	return identity.Session
}

func diagnoseTerminal(stdout io.Writer) string {
	term := os.Getenv("TERM")
	_, _ = fmt.Fprintf(stdout, "terminal: TERM=%s, TERM_PROGRAM=%s\n",
		displayValue(term), displayValue(os.Getenv("TERM_PROGRAM")))
	return term
}

// msResult is the outcome of checking one terminal's Ms terminfo extension.
type msResult struct {
	status string
	detail string
}

// msProbe checks Ms with infocmp and caches one result per terminal, so a terminal shared by
// several clients costs one lookup.
type msProbe struct {
	path  string
	cache map[string]msResult
}

func newMsProbe() *msProbe {
	probe := &msProbe{path: "", cache: make(map[string]msResult)}
	if path, err := exec.LookPath(infocmpBin); err == nil {
		probe.path = path
	}
	return probe
}

func (p *msProbe) check(term string) msResult {
	if cached, ok := p.cache[term]; ok {
		return cached
	}
	result := p.probe(term)
	p.cache[term] = result
	return result
}

func (p *msProbe) probe(term string) msResult {
	if term == "" {
		return msResult{status: statusUnknown, detail: "TERM is not set"}
	}
	if p.path == "" {
		return msResult{status: statusUnknown, detail: "infocmp not found on PATH"}
	}
	out, err := commandOutput(p.path, "-x", term)
	if err != nil {
		return msResult{status: statusUnknown, detail: "no terminfo for TERM=" + sanitizeText(term)}
	}
	for field := range strings.SplitSeq(out, ",") {
		if strings.HasPrefix(strings.TrimSpace(field), "Ms=") {
			return msResult{status: statusOK, detail: "TERM=" + sanitizeText(term)}
		}
	}
	return msResult{status: statusMissing, detail: "TERM=" + sanitizeText(term) + " has no Ms; tmux cannot send OSC52 to the outer terminal"}
}

func diagnoseMs(stdout io.Writer, probe *msProbe, term string) {
	result := probe.check(term)
	_, _ = fmt.Fprintf(stdout, "Ms: %s (%s)\n", result.status, result.detail)
}

// diagnoseTmux reports server presence, the saved session, its effective settings, and client
// state. Every command it runs reads state; none creates a server, changes an option, or touches a
// buffer or the clipboard.
// commandOutput runs a bounded, read-only command and returns its trimmed stdout.
func commandOutput(path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), diagnosticCommandTimeout)
	defer cancel()

	//nolint:gosec // path comes from exec.LookPath for a fixed tool name or the plugin's configured herdr binary.
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(line)
}

// displayValue keeps each reported value on one line and never emits terminal control sequences.
func displayValue(value string) string {
	if value == "" {
		return unsetValue
	}
	return sanitizeText(value)
}

func sanitizeText(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, value)
}

func diagnoseState(stdout io.Writer) {
	dir := os.Getenv(stateDirEnvVar)
	if dir == "" {
		_, _ = fmt.Fprintf(stdout, "state: missing (%s not set)\n", stateDirEnvVar)
		return
	}

	path := filepath.Clean(filepath.Join(dir, stateFile))
	//nolint:gosec // path is HERDR_PLUGIN_STATE_DIR joined with the fixed stateFile name.
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
