package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none for a missing file", cfg.Warnings)
	}
}

func TestLoad_EmptyFile_DefaultsToWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(configDirEnvVar, dir)

	cfg := Load()

	if cfg.Scope != defaultScope {
		t.Errorf("Scope = %q, want %q", cfg.Scope, defaultScope)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none for an empty file", cfg.Warnings)
	}
}

func TestLoad_ScopeDirectory(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, `scope = "directory"`+"\n"))

	cfg := Load()

	if cfg.Scope != "directory" {
		t.Errorf("Scope = %q, want directory", cfg.Scope)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none for a valid config", cfg.Warnings)
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

func TestIsValidScope_Workspace(t *testing.T) {
	t.Parallel()

	if !IsValidScope("workspace") {
		t.Error("IsValidScope(workspace) = false, want true")
	}
}

func TestIsValidScope_Directory(t *testing.T) {
	t.Parallel()

	if !IsValidScope("directory") {
		t.Error("IsValidScope(directory) = false, want true")
	}
}

func TestIsValidScope_Tab(t *testing.T) {
	t.Parallel()

	if !IsValidScope("tab") {
		t.Error("IsValidScope(tab) = false, want true")
	}
}

func TestIsValidScope_Bogus(t *testing.T) {
	t.Parallel()

	if IsValidScope("bogus-scope") {
		t.Error("IsValidScope(bogus-scope) = true, want false")
	}
}

func TestIsValidScope_Empty(t *testing.T) {
	t.Parallel()

	if IsValidScope("") {
		t.Error("IsValidScope(\"\") = true, want false")
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

// scope = secret-value is invalid TOML (an unquoted value), and BurntSushi's own message quotes
// the offending token. The warning must report the failure without echoing that fragment.
func TestLoad_MalformedTOML_WarnsAndFallsBackWithoutEchoingContents(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "scope = secret-value\n"))

	cfg := Load()

	if cfg.Scope != defaultScope {
		t.Errorf("Scope = %q, want %q on malformed config", cfg.Scope, defaultScope)
	}
	if got := cfg.PopupSizeSteps("shell"); got != "" {
		t.Errorf("PopupSizeSteps(shell) = %q, want empty on malformed config", got)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "could not parse") {
		t.Fatalf("Warnings = %v, want one parse warning", cfg.Warnings)
	}
	if !strings.Contains(cfg.Warnings[0], "line 1") {
		t.Errorf("warning = %q, want the failing line", cfg.Warnings[0])
	}
	if strings.Contains(cfg.Warnings[0], "secret") {
		t.Errorf("warning = %q, want it to not echo configuration contents", cfg.Warnings[0])
	}
}

func TestLoad_UnreadableConfigFile_WarnsAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	// A directory where config.toml is expected makes os.ReadFile fail with something other than
	// fs.ErrNotExist on every supported platform, and needs no permission bits.
	if err := os.Mkdir(filepath.Join(dir, "config.toml"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv(configDirEnvVar, dir)

	cfg := Load()

	if cfg.Scope != defaultScope {
		t.Errorf("Scope = %q, want %q on a read failure", cfg.Scope, defaultScope)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "could not read") {
		t.Errorf("Warnings = %v, want one read warning", cfg.Warnings)
	}
	if strings.Contains(cfg.Warnings[0], dir) {
		t.Errorf("warning = %q, want it to not echo the resolved config path", cfg.Warnings[0])
	}
}

// scope = 5 is valid TOML but the wrong type for the string field, so BurntSushi reports a plain
// decode error rather than a ParseError. The warning must name a decode failure, not a syntax one.
func TestLoad_WrongTypeValue_WarnsAndFallsBack(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "scope = 5\n"))

	cfg := Load()

	if cfg.Scope != defaultScope {
		t.Errorf("Scope = %q, want %q on a decode failure", cfg.Scope, defaultScope)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "could not decode") {
		t.Fatalf("Warnings = %v, want one decode warning", cfg.Warnings)
	}
	if strings.Contains(cfg.Warnings[0], "malformed TOML") {
		t.Errorf("warning = %q, want it to not report a type mismatch as malformed TOML", cfg.Warnings[0])
	}
}

func TestLoad_NoConfigDirEnv_DefaultsDimensionsToFullScreen(t *testing.T) {
	t.Setenv(configDirEnvVar, "")

	cfg := Load()

	if cfg.Width != defaultPopupDimension || cfg.Height != defaultPopupDimension {
		t.Errorf("dimensions = %q x %q, want %q x %q", cfg.Width, cfg.Height, defaultPopupDimension, defaultPopupDimension)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", cfg.Warnings)
	}
}

func TestLoad_DimensionsAbsent_DefaultToFullScreen(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, `scope = "workspace"`+"\n"))

	cfg := Load()

	if cfg.Width != defaultPopupDimension || cfg.Height != defaultPopupDimension {
		t.Errorf("dimensions = %q x %q, want %q x %q", cfg.Width, cfg.Height, defaultPopupDimension, defaultPopupDimension)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", cfg.Warnings)
	}
}

func TestLoad_DimensionsAsPercentages(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "width = \"80%\"\nheight = \"40%\"\n"))

	cfg := Load()

	if cfg.Width != "80%" || cfg.Height != "40%" {
		t.Errorf("dimensions = %q x %q, want 80%% x 40%%", cfg.Width, cfg.Height)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", cfg.Warnings)
	}
}

func TestLoad_DimensionsAsCellIntegers(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "width = 120\nheight = 30\n"))

	cfg := Load()

	if cfg.Width != "120" || cfg.Height != "30" {
		t.Errorf("dimensions = %q x %q, want 120 x 30", cfg.Width, cfg.Height)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", cfg.Warnings)
	}
}

func TestLoad_DimensionAsCellString(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "width = \"120\"\n"))

	cfg := Load()

	if cfg.Width != "120" {
		t.Errorf("Width = %q, want 120", cfg.Width)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", cfg.Warnings)
	}
}

func TestLoad_InvalidWidth_WarnsAndFallsBack(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "width = \"wide\"\n"))

	cfg := Load()

	if cfg.Width != defaultPopupDimension {
		t.Errorf("Width = %q, want fallback %q", cfg.Width, defaultPopupDimension)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "invalid width") {
		t.Errorf("Warnings = %v, want one invalid width warning", cfg.Warnings)
	}
}

func TestLoad_InvalidHeight_WarnsAndFallsBack(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "height = \"0%\"\n"))

	cfg := Load()

	if cfg.Height != defaultPopupDimension {
		t.Errorf("Height = %q, want fallback %q", cfg.Height, defaultPopupDimension)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "invalid height") {
		t.Errorf("Warnings = %v, want one invalid height warning", cfg.Warnings)
	}
}

func TestLoad_OutOfRangeDimensions_WarnAndFallBack(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "width = 65536\nheight = \"101%\"\n"))

	cfg := Load()

	if cfg.Width != defaultPopupDimension || cfg.Height != defaultPopupDimension {
		t.Errorf("dimensions = %q x %q, want %q defaults", cfg.Width, cfg.Height, defaultPopupDimension)
	}
	if len(cfg.Warnings) != 2 {
		t.Errorf("Warnings = %v, want a warning for each out-of-range dimension", cfg.Warnings)
	}
}

func TestLoad_InvalidWidthKeepsValidHeight(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "width = \"wide\"\nheight = \"40%\"\n"))

	cfg := Load()

	if cfg.Width != defaultPopupDimension {
		t.Errorf("Width = %q, want fallback %q", cfg.Width, defaultPopupDimension)
	}
	if cfg.Height != "40%" {
		t.Errorf("Height = %q, want the valid value to survive", cfg.Height)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "invalid width") {
		t.Errorf("Warnings = %v, want only the invalid width warning", cfg.Warnings)
	}
}

func TestLoad_WrongTypeDimension_WarnsAndFallsBackWithoutEchoingValue(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, "width = 1.5\n"))

	cfg := Load()

	if cfg.Width != defaultPopupDimension {
		t.Errorf("Width = %q, want fallback %q", cfg.Width, defaultPopupDimension)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "invalid width") {
		t.Fatalf("Warnings = %v, want one invalid width warning", cfg.Warnings)
	}
	if strings.Contains(cfg.Warnings[0], "1.5") {
		t.Errorf("warning = %q, want it to not echo the unsupported TOML value", cfg.Warnings[0])
	}
}

func TestLoad_PopupSizeNotInterpretedAsDimensions(t *testing.T) {
	t.Setenv(configDirEnvVar, writeConfigFile(t, `popup_size.shell = "right:0.5:3"`+"\n"))

	cfg := Load()

	if cfg.Width != defaultPopupDimension || cfg.Height != defaultPopupDimension {
		t.Errorf("dimensions = %q x %q, want defaults when only popup_size is set", cfg.Width, cfg.Height)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none for legacy popup_size", cfg.Warnings)
	}
}

func TestValidateDimension(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value string
		want  bool
	}{
		{"100%", true},
		{"80%", true},
		{"1%", true},
		{"0%", false},
		{"101%", false},
		{"", false},
		{"80", true},
		{"0", true},
		{"65535", true},
		{"65536", false},
		{"-5", false},
		{"wide", false},
		{"1.5", false},
		{"80% ", false},
		{"+80", false},
		{"-0", false},
		{"007", true},
	}
	for _, tc := range cases {
		if got := validateDimension(tc.value); got != tc.want {
			t.Errorf("validateDimension(%q) = %v, want %v", tc.value, got, tc.want)
		}
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
