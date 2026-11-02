package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Test list for scripts/generate-sbom.sh:
// - no arguments prints the usage and exits non-zero
// - missing syft fails before writing any SBOM
// - missing cyclonedx fails before writing any SBOM
// - no release binaries fails and names the directory
// - one SBOM is written per release binary as sbom_<asset>.cdx.json
// - syft scans each binary as file:<path> at the release version
// - cyclonedx validates each SBOM as CycloneDX 1.6 JSON
// - a syft failure fails the script and names the binary
// - a rejected SBOM fails the script and names the file

const (
	sbomScriptName = "scripts/generate-sbom.sh"
	sbomVersion    = "1.2.3"
	sbomDarwinARM  = "toggle-popup_darwin_arm64"
	sbomLinuxAMD64 = "toggle-popup_linux_amd64"
	sbomExtension  = ".cdx.json"
)

func TestSBOMScriptWithoutArgumentsPrintsUsage(t *testing.T) {
	t.Parallel()

	stderr, err := runSBOMScript(t, "")

	if err == nil {
		t.Fatal("generate-sbom.sh succeeded without arguments; want failure")
	}
	want := "usage: sh scripts/generate-sbom.sh <dist-dir> <version>"
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
}

func TestSBOMScriptWithoutSyftFailsAndWritesNoSBOM(t *testing.T) {
	t.Parallel()

	distDir := t.TempDir()
	writeReleaseBinary(t, distDir, sbomLinuxAMD64)

	stderr, err := runSBOMScript(t, newToolPath(t, nil), distDir, sbomVersion)

	if err == nil {
		t.Fatal("generate-sbom.sh succeeded without syft; want failure")
	}
	if !strings.Contains(stderr, "syft") {
		t.Fatalf("stderr = %q, want it to name syft", stderr)
	}
	assertNotExist(t, sbomPath(distDir, sbomLinuxAMD64))
}

func TestSBOMScriptWithoutValidatorFailsAndWritesNoSBOM(t *testing.T) {
	t.Parallel()

	tools := installFakeSBOMTools(t)
	removeFakeTool(t, tools.pathDir, "cyclonedx")
	distDir := t.TempDir()
	writeReleaseBinary(t, distDir, sbomLinuxAMD64)

	stderr, err := runSBOMScript(t, tools.pathDir, distDir, sbomVersion)

	if err == nil {
		t.Fatal("generate-sbom.sh succeeded without cyclonedx; want failure")
	}
	if !strings.Contains(stderr, "cyclonedx") {
		t.Fatalf("stderr = %q, want it to name cyclonedx", stderr)
	}
	assertNotExist(t, sbomPath(distDir, sbomLinuxAMD64))
}

func TestSBOMScriptWithoutReleaseBinariesFails(t *testing.T) {
	t.Parallel()

	tools := installFakeSBOMTools(t)
	distDir := t.TempDir()

	stderr, err := runSBOMScript(t, tools.pathDir, distDir, sbomVersion)

	if err == nil {
		t.Fatal("generate-sbom.sh succeeded without release binaries; want failure")
	}
	if !strings.Contains(stderr, distDir) {
		t.Fatalf("stderr = %q, want it to name %q", stderr, distDir)
	}
	if log := readLog(t, tools.syftLog); log != "" {
		t.Fatalf("syft ran without release binaries; log = %q", log)
	}
}

func TestSBOMScriptWritesOneSBOMPerReleaseBinary(t *testing.T) {
	t.Parallel()

	tools := installFakeSBOMTools(t)
	distDir := t.TempDir()
	writeReleaseBinary(t, distDir, sbomDarwinARM)
	writeReleaseBinary(t, distDir, sbomLinuxAMD64)
	writeFile(t, filepath.Join(distDir, "checksums.txt"), "not a binary\n")

	_, err := runSBOMScript(t, tools.pathDir, distDir, sbomVersion)
	if err != nil {
		t.Fatalf("generate-sbom.sh failed: %v", err)
	}

	for _, asset := range []string{sbomDarwinARM, sbomLinuxAMD64} {
		if got := readFile(t, sbomPath(distDir, asset)); got != "fake-sbom\n" {
			t.Errorf("%s = %q, want fake syft output", sbomPath(distDir, asset), got)
		}
	}
	assertNotExist(t, sbomPath(distDir, "checksums.txt"))

	if got := countInvocations(t, tools.syftLog); got != 2 {
		t.Errorf("syft invocations = %d, want 2", got)
	}
}

func TestSBOMScriptScansEachBinaryAtTheReleaseVersion(t *testing.T) {
	t.Parallel()

	tools := installFakeSBOMTools(t)
	distDir := t.TempDir()
	binaryPath := writeReleaseBinary(t, distDir, sbomLinuxAMD64)

	_, err := runSBOMScript(t, tools.pathDir, distDir, sbomVersion)
	if err != nil {
		t.Fatalf("generate-sbom.sh failed: %v", err)
	}

	log := readLog(t, tools.syftLog)
	for _, want := range []string{
		"scan",
		"file:" + binaryPath,
		"--source-name",
		sbomLinuxAMD64,
		"--source-version",
		sbomVersion,
		"cyclonedx-json@1.6=" + sbomPath(distDir, sbomLinuxAMD64),
	} {
		if !strings.Contains(log, want+"\n") {
			t.Errorf("syft log = %q, want it to contain %q", log, want)
		}
	}
}

func TestSBOMScriptValidatesEachSBOMPerBinaryAsCycloneDX16JSON(t *testing.T) {
	t.Parallel()

	tools := installFakeSBOMTools(t)
	distDir := t.TempDir()
	writeReleaseBinary(t, distDir, sbomLinuxAMD64)

	_, err := runSBOMScript(t, tools.pathDir, distDir, sbomVersion)
	if err != nil {
		t.Fatalf("generate-sbom.sh failed: %v", err)
	}

	log := readLog(t, tools.cyclonedxLog)
	for _, want := range []string{
		"validate",
		"--input-file",
		sbomPath(distDir, sbomLinuxAMD64),
		"--input-format",
		"json",
		"--input-version",
		"1.6",
		"--fail-on-errors",
	} {
		if !strings.Contains(log, want+"\n") {
			t.Errorf("cyclonedx log = %q, want it to contain %q", log, want)
		}
	}
}

func TestSBOMScriptFailsWhenSyftFails(t *testing.T) {
	t.Parallel()

	tools := installFakeSBOMTools(t)
	writeFakeTool(t, tools.pathDir, "syft", "#!/bin/sh\nexit 1\n")
	distDir := t.TempDir()
	writeReleaseBinary(t, distDir, sbomLinuxAMD64)

	stderr, err := runSBOMScript(t, tools.pathDir, distDir, sbomVersion)

	if err == nil {
		t.Fatal("generate-sbom.sh succeeded when syft failed; want failure")
	}
	if want := "generate-sbom.sh: SBOM generation failed for " + sbomLinuxAMD64; !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
	if log := readLog(t, tools.cyclonedxLog); log != "" {
		t.Fatalf("cyclonedx ran on a missing SBOM; log = %q", log)
	}
}

func TestSBOMScriptFailsWhenValidationRejectsASBOM(t *testing.T) {
	t.Parallel()

	tools := installFakeSBOMTools(t)
	writeFakeTool(t, tools.pathDir, "cyclonedx", "#!/bin/sh\nexit 1\n")
	distDir := t.TempDir()
	writeReleaseBinary(t, distDir, sbomLinuxAMD64)

	stderr, err := runSBOMScript(t, tools.pathDir, distDir, sbomVersion)

	if err == nil {
		t.Fatal("generate-sbom.sh succeeded when validation failed; want failure")
	}
	if want := "generate-sbom.sh: SBOM validation failed for " + sbomPath(distDir, sbomLinuxAMD64); !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
}

type fakeSBOMTools struct {
	pathDir      string
	syftLog      string
	cyclonedxLog string
}

// installFakeSBOMTools installs stand-ins for syft and cyclonedx on a PATH that holds nothing
// else, so a test never reaches a tool installed on the machine. Each fake appends its arguments,
// one per line, to its log.
func installFakeSBOMTools(t *testing.T) fakeSBOMTools {
	t.Helper()

	tools := fakeSBOMTools{
		pathDir:      newToolPath(t, nil),
		syftLog:      filepath.Join(t.TempDir(), "syft.log"),
		cyclonedxLog: filepath.Join(t.TempDir(), "cyclonedx.log"),
	}
	writeFakeTool(t, tools.pathDir, "syft", fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
  printf '%%s\n' "$arg" >> %q
done
printf '%%s\n' '---' >> %q
out=""
for arg in "$@"; do
  case "$arg" in
    cyclonedx-json*=*) out="${arg#*=}" ;;
  esac
done
test -n "$out"
printf '%%s\n' fake-sbom > "$out"
`, tools.syftLog, tools.syftLog))
	writeFakeTool(t, tools.pathDir, "cyclonedx", fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
  printf '%%s\n' "$arg" >> %q
done
printf '%%s\n' '---' >> %q
`, tools.cyclonedxLog, tools.cyclonedxLog))

	return tools
}

func removeFakeTool(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatalf("remove fake %s: %v", name, err)
	}
}

// runSBOMScript runs the SBOM script with args on a PATH holding only pathDir. An empty pathDir
// keeps the test's own PATH.
func runSBOMScript(t *testing.T, pathDir string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command("sh", append([]string{sbomScriptName}, args...)...)
	if pathDir != "" {
		cmd.Env = append(os.Environ(), "PATH="+pathDir)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	return stderr.String(), err
}

func writeReleaseBinary(t *testing.T, distDir, asset string) string {
	t.Helper()

	path := filepath.Join(distDir, asset)
	writeFile(t, path, "fake-binary\n")

	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	//nolint:gosec // Test fixtures are only read by this package.
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sbomPath(distDir, asset string) string {
	return filepath.Join(distDir, "sbom_"+asset+sbomExtension)
}

func assertNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists or cannot be checked; stat error = %v", path, err)
	}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	//nolint:gosec // Test helper only reads paths created by this test package.
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(b)
}

func countInvocations(t *testing.T, logPath string) int {
	t.Helper()

	return strings.Count(readLog(t, logPath), "---\n")
}
