package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const (
	testDirectionRight = "right"
	testAmountHalf     = "0.5"
)

// writeConfigFile creates config.toml with content under a fresh temp dir and returns the dir,
// for the caller to pass to t.Setenv directly (so paralleltest can see the env mutation).
func writeConfigFile(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	if content == "" {
		return dir
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoad_NoConfigDirEnv_DefaultsToWorkspace(t *testing.T) {
	t.Setenv(configDirEnvVar, "")

	cfg := Load()

	if cfg.Scope != defaultScope {
		t.Errorf("Scope = %q, want %q", cfg.Scope, defaultScope)
	}
}

func TestLoad_ConfigDirSetButFileMissing_DefaultsToWorkspace(t *testing.T) {
	t.Setenv(configDirEnvVar, t.TempDir())

	cfg := Load()

	if cfg.Scope != defaultScope {
		t.Errorf("Scope = %q, want %q", cfg.Scope, defaultScope)
	}
}

func TestLoad_EmptyFile_DefaultsToWorkspace(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, ""))

	cfg := Load()

	if cfg.Scope != defaultScope {
		t.Errorf("Scope = %q, want %q", cfg.Scope, defaultScope)
	}
}

func TestLoad_ScopeDirectory(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, `scope = "directory"`+"\n"))

	cfg := Load()

	if cfg.Scope != "directory" {
		t.Errorf("Scope = %q, want directory", cfg.Scope)
	}
}

func TestLoad_ScopeAbsent_DefaultsToWorkspace(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, `other = "value"`+"\n"))

	cfg := Load()

	if cfg.Scope != defaultScope {
		t.Errorf("Scope = %q, want %q", cfg.Scope, defaultScope)
	}
}

func TestLoad_ScopeExplicitWorkspace(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, `scope = "workspace"`+"\n"))

	cfg := Load()

	if cfg.Scope != defaultScope {
		t.Errorf("Scope = %q, want %q", cfg.Scope, defaultScope)
	}
}

func TestLoad_PopupSizeDottedKey(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, `popup_size.shell = "right:0.5:3 down:0.5:3"`+"\n"))

	cfg := Load()

	if got := cfg.PopupSizeSteps("shell"); got != "right:0.5:3 down:0.5:3" {
		t.Errorf("PopupSizeSteps(shell) = %q, want %q", got, "right:0.5:3 down:0.5:3")
	}
}

func TestLoad_PopupSizeTableForm(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "[popup_size]\nshell = \"right:0.5:3 down:0.5:3\"\n"))

	cfg := Load()

	if got := cfg.PopupSizeSteps("shell"); got != "right:0.5:3 down:0.5:3" {
		t.Errorf("PopupSizeSteps(shell) = %q, want %q", got, "right:0.5:3 down:0.5:3")
	}
}

func TestLoad_PopupSizeAbsent_ReturnsEmpty(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, `scope = "workspace"`+"\n"))

	cfg := Load()

	if got := cfg.PopupSizeSteps("shell"); got != "" {
		t.Errorf("PopupSizeSteps(shell) = %q, want empty", got)
	}
}

func TestLoad_PopupSizeDifferentEntrypointNotApplied(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, `popup_size.git = "right:0.5:2"`+"\n"))

	cfg := Load()

	if got := cfg.PopupSizeSteps("shell"); got != "" {
		t.Errorf("PopupSizeSteps(shell) = %q, want empty when only git is configured", got)
	}
}

func TestLoad_MalformedTOML_TreatedAsAbsent(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "not valid toml{{{\n"))

	cfg := Load()

	if cfg.Scope != defaultScope {
		t.Errorf("Scope = %q, want %q on malformed config", cfg.Scope, defaultScope)
	}
	if got := cfg.PopupSizeSteps("shell"); got != "" {
		t.Errorf("PopupSizeSteps(shell) = %q, want empty on malformed config", got)
	}
}

func TestParseSizeSteps_Empty(t *testing.T) {
	t.Parallel()

	if got := ParseSizeSteps(""); got != nil {
		t.Errorf("ParseSizeSteps(\"\") = %v, want nil", got)
	}
}

func TestParseSizeSteps_SingleStep(t *testing.T) {
	t.Parallel()

	want := []SizeStep{{Direction: testDirectionRight, Amount: testAmountHalf, Count: 3}}
	if got := ParseSizeSteps("right:0.5:3"); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSizeSteps() = %+v, want %+v", got, want)
	}
}

func TestParseSizeSteps_MultipleSteps(t *testing.T) {
	t.Parallel()

	want := []SizeStep{
		{Direction: testDirectionRight, Amount: testAmountHalf, Count: 2},
		{Direction: "down", Amount: "0.25", Count: 1},
	}
	if got := ParseSizeSteps("right:0.5:2 down:0.25:1"); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSizeSteps() = %+v, want %+v", got, want)
	}
}

func TestParseSizeSteps_MalformedDirectionSkipped(t *testing.T) {
	t.Parallel()

	if got := ParseSizeSteps("sideways:0.5:2"); got != nil {
		t.Errorf("ParseSizeSteps() = %+v, want nil", got)
	}
}

func TestParseSizeSteps_MalformedAmountSkipped(t *testing.T) {
	t.Parallel()

	if got := ParseSizeSteps("right:notanumber:2"); got != nil {
		t.Errorf("ParseSizeSteps() = %+v, want nil", got)
	}
}

func TestParseSizeSteps_MalformedCountSkipped(t *testing.T) {
	t.Parallel()

	if got := ParseSizeSteps("down:0.5:0"); got != nil {
		t.Errorf("ParseSizeSteps() = %+v, want nil for a zero count", got)
	}
}

func TestParseSizeSteps_MixedMalformedAndValid(t *testing.T) {
	t.Parallel()

	// Mirrors tests/toggle.bats: "a malformed step is skipped, other valid steps in the same value
	// still run".
	got := ParseSizeSteps("sideways:0.5:2 right:notanumber:2 down:0.5:0 up:0.5:1")
	want := []SizeStep{{Direction: "up", Amount: testAmountHalf, Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSizeSteps() = %+v, want %+v", got, want)
	}
}
