package main

import (
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
)

// toggleBinary is the manifest-declared path to the built Go binary.
const toggleBinary = "./bin/toggle-popup"

// manifest mirrors the subset of herdr-plugin.toml this test asserts on. Field order matches the
// file so a failing assertion is easy to locate.
type manifest struct {
	ID              string   `toml:"id"`
	Version         string   `toml:"version"`
	MinHerdrVersion string   `toml:"min_herdr_version"`
	Platforms       []string `toml:"platforms"`
	Build           []struct {
		Command []string `toml:"command"`
	} `toml:"build"`
	Actions []struct {
		ID       string   `toml:"id"`
		Title    string   `toml:"title"`
		Contexts []string `toml:"contexts"`
		Command  []string `toml:"command"`
	} `toml:"actions"`
	Panes []struct {
		ID        string   `toml:"id"`
		Title     string   `toml:"title"`
		Placement string   `toml:"placement"`
		Command   []string `toml:"command"`
	} `toml:"panes"`
	Events []struct {
		On      string   `toml:"on"`
		Command []string `toml:"command"`
	} `toml:"events"`
}

type keybindings struct {
	Keys struct {
		Command []struct {
			Key         string `toml:"key"`
			Type        string `toml:"type"`
			Command     string `toml:"command"`
			Description string `toml:"description"`
		} `toml:"command"`
	} `toml:"keys"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()

	var m manifest
	if _, err := toml.DecodeFile("herdr-plugin.toml", &m); err != nil {
		t.Fatalf("decode herdr-plugin.toml: %v", err)
	}
	return m
}

func loadKeybindings(t *testing.T) keybindings {
	t.Helper()

	var k keybindings
	if _, err := toml.DecodeFile("keybindings.toml", &k); err != nil {
		t.Fatalf("decode keybindings.toml: %v", err)
	}
	return k
}

// TestManifestPluginMetadata ports the "manifest declares plugin id, version and
// min_herdr_version" and "manifest declares supported platforms" cases from tests/manifest.bats.
func TestManifestPluginMetadata(t *testing.T) {
	t.Parallel()

	m := loadManifest(t)

	if m.ID != "maro114510.toggle-popup" {
		t.Errorf("ID = %q, want %q", m.ID, "maro114510.toggle-popup")
	}
	if m.MinHerdrVersion != "0.7.0" {
		t.Errorf("MinHerdrVersion = %q, want %q", m.MinHerdrVersion, "0.7.0")
	}
	wantPlatforms := []string{"macos", "linux"}
	if !reflect.DeepEqual(m.Platforms, wantPlatforms) {
		t.Errorf("Platforms = %v, want %v", m.Platforms, wantPlatforms)
	}
}

// TestManifestBuildStep ports "manifest declares the build step" from tests/manifest.bats.
func TestManifestBuildStep(t *testing.T) {
	t.Parallel()

	m := loadManifest(t)

	if len(m.Build) != 1 {
		t.Fatalf("len(Build) = %d, want 1", len(m.Build))
	}
	want := []string{"sh", "scripts/build.sh"}
	if !reflect.DeepEqual(m.Build[0].Command, want) {
		t.Errorf("Build[0].Command = %v, want %v", m.Build[0].Command, want)
	}
}

// TestManifestToggleShellAction ports "manifest declares the toggle-shell action" from
// tests/manifest.bats, updated to expect the Go binary instead of bash.
func TestManifestToggleShellAction(t *testing.T) {
	t.Parallel()

	m := loadManifest(t)

	if len(m.Actions) != 1 {
		t.Fatalf("len(Actions) = %d, want 1", len(m.Actions))
	}
	a := m.Actions[0]
	if a.ID != "toggle-shell" {
		t.Errorf("ID = %q, want %q", a.ID, "toggle-shell")
	}
	if a.Title != "Toggle popup shell" {
		t.Errorf("Title = %q, want %q", a.Title, "Toggle popup shell")
	}
	wantContexts := []string{"workspace", "tab", "pane"}
	if !reflect.DeepEqual(a.Contexts, wantContexts) {
		t.Errorf("Contexts = %v, want %v", a.Contexts, wantContexts)
	}
	wantCommand := []string{toggleBinary, "toggle", "shell"}
	if !reflect.DeepEqual(a.Command, wantCommand) {
		t.Errorf("Command = %v, want %v", a.Command, wantCommand)
	}
}

// TestManifestShellPane ports "manifest declares the shell pane" from tests/manifest.bats.
func TestManifestShellPane(t *testing.T) {
	t.Parallel()

	m := loadManifest(t)

	if len(m.Panes) != 1 {
		t.Fatalf("len(Panes) = %d, want 1", len(m.Panes))
	}
	p := m.Panes[0]
	if p.ID != "shell" {
		t.Errorf("ID = %q, want %q", p.ID, "shell")
	}
	if p.Title != "Popup Shell" {
		t.Errorf("Title = %q, want %q", p.Title, "Popup Shell")
	}
	if p.Placement != "overlay" {
		t.Errorf("Placement = %q, want %q", p.Placement, "overlay")
	}
	wantCommand := []string{"sh", "-lc", `exec "${SHELL:-/bin/zsh}"`}
	if !reflect.DeepEqual(p.Command, wantCommand) {
		t.Errorf("Command = %v, want %v", p.Command, wantCommand)
	}
}

// TestManifestPaneClosedEvent ports "manifest declares the pane.closed event hook" from
// tests/manifest.bats, updated to expect the Go binary instead of bash.
func TestManifestPaneClosedEvent(t *testing.T) {
	t.Parallel()

	m := loadManifest(t)

	if len(m.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(m.Events))
	}
	e := m.Events[0]
	if e.On != "pane.closed" {
		t.Errorf("On = %q, want %q", e.On, "pane.closed")
	}
	wantCommand := []string{toggleBinary, "on-pane-closed"}
	if !reflect.DeepEqual(e.Command, wantCommand) {
		t.Errorf("Command = %v, want %v", e.Command, wantCommand)
	}
}

// TestKeybindingsAltLBinding ports "keybindings.toml declares the alt+l plugin_action binding"
// from tests/manifest.bats.
func TestKeybindingsAltLBinding(t *testing.T) {
	t.Parallel()

	k := loadKeybindings(t)

	if len(k.Keys.Command) != 1 {
		t.Fatalf("len(Keys.Command) = %d, want 1", len(k.Keys.Command))
	}
	c := k.Keys.Command[0]
	if c.Key != "alt+l" {
		t.Errorf("Key = %q, want %q", c.Key, "alt+l")
	}
	if c.Type != "plugin_action" {
		t.Errorf("Type = %q, want %q", c.Type, "plugin_action")
	}
	if c.Command != "maro114510.toggle-popup.toggle-shell" {
		t.Errorf("Command = %q, want %q", c.Command, "maro114510.toggle-popup.toggle-shell")
	}
}
