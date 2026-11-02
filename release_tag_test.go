package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Test list for scripts/verify-release-tag.sh:
// - a tag signed by a key committed on main and reachable from main is accepted
// - an unsigned tag is rejected
// - a tag signed by a key that main does not commit is rejected
// - a tag on a commit that main does not reach is rejected
// - a missing .github/release-signing-key.asc on main is reported as maintainer setup
// - a tag signed before a key rotation still verifies after the keyring gains a new key

const (
	verifyReleaseTagScript = "scripts/verify-release-tag.sh"
	releaseSigningKeyFile  = ".github/release-signing-key.asc"
	firstReleaseTag        = "v1.2.3"
	rotatedReleaseTag      = "v1.3.0"
)

// releaseFixture is a repository pair the verification script can run against: a bare origin the
// fixture pushes history and tags to, and a clone that plays the CI checkout.
type releaseFixture struct {
	workDir     string // clone the fixture commits, tags, and pushes from
	cloneDir    string // clone the verification script runs in
	signingHome string // GNUPGHOME holding the fixture's signing keys
	releaseKey  string // fingerprint of the release signing key
	rotatedKey  string // fingerprint of the key present only after a rotation
}

func TestVerifyReleaseTagAcceptsTagSignedByCommittedKey(t *testing.T) {

	f := newReleaseFixture(t)
	f.commitSigningKeys(t, f.releaseKey)
	f.createSignedTag(t, firstReleaseTag, f.releaseKey)
	f.pushTag(t, firstReleaseTag)

	output, code := f.runVerification(t, firstReleaseTag)

	if code != 0 {
		t.Fatalf("verify-release-tag.sh exit = %d, want 0\n%s", code, output)
	}
	if want := firstReleaseTag + " is signed by a trusted key and reachable from origin/main"; !strings.Contains(output, want) {
		t.Fatalf("output = %q, want it to contain %q", output, want)
	}
}

func TestVerifyReleaseTagRejectsUnsignedTag(t *testing.T) {

	f := newReleaseFixture(t)
	f.commitSigningKeys(t, f.releaseKey)
	f.createAnnotatedTag(t, firstReleaseTag)
	f.pushTag(t, firstReleaseTag)

	output, code := f.runVerification(t, firstReleaseTag)

	if code == 0 {
		t.Fatalf("verify-release-tag.sh exit = 0, want a rejection\n%s", output)
	}
	if want := firstReleaseTag + " is not signed by a key in " + releaseSigningKeyFile; !strings.Contains(output, want) {
		t.Fatalf("output = %q, want it to contain %q", output, want)
	}
}

func TestVerifyReleaseTagRejectsTagSignedByKeyOutsideCommittedKeyring(t *testing.T) {

	f := newReleaseFixture(t)
	f.commitSigningKeys(t, f.releaseKey)
	f.createSignedTag(t, firstReleaseTag, f.rotatedKey)
	f.pushTag(t, firstReleaseTag)

	output, code := f.runVerification(t, firstReleaseTag)

	if code == 0 {
		t.Fatalf("verify-release-tag.sh exit = 0, want a rejection\n%s", output)
	}
	if want := firstReleaseTag + " is not signed by a key in " + releaseSigningKeyFile; !strings.Contains(output, want) {
		t.Fatalf("output = %q, want it to contain %q", output, want)
	}
}

func TestVerifyReleaseTagRejectsTagOutsideMain(t *testing.T) {

	f := newReleaseFixture(t)
	f.commitSigningKeys(t, f.releaseKey)
	f.git(t, f.workDir, "switch", "-c", "side")
	f.git(t, f.workDir, "commit", "--allow-empty", "-m", "side work")
	f.createSignedTag(t, firstReleaseTag, f.releaseKey)
	f.pushTag(t, firstReleaseTag)

	output, code := f.runVerification(t, firstReleaseTag)

	if code == 0 {
		t.Fatalf("verify-release-tag.sh exit = 0, want a rejection\n%s", output)
	}
	if want := "is not reachable from origin/main"; !strings.Contains(output, want) {
		t.Fatalf("output = %q, want it to contain %q", output, want)
	}
}

func TestVerifyReleaseTagReportsMissingSigningKeyOnMain(t *testing.T) {

	f := newReleaseFixture(t)
	f.createSignedTag(t, firstReleaseTag, f.releaseKey)
	f.pushTag(t, firstReleaseTag)

	output, code := f.runVerification(t, firstReleaseTag)

	if code == 0 {
		t.Fatalf("verify-release-tag.sh exit = 0, want a rejection\n%s", output)
	}
	if want := "origin/main has no " + releaseSigningKeyFile; !strings.Contains(output, want) {
		t.Fatalf("output = %q, want it to contain %q", output, want)
	}
}

func TestVerifyReleaseTagAcceptsHistoricalTagAfterKeyRotation(t *testing.T) {

	f := newReleaseFixture(t)
	f.commitSigningKeys(t, f.releaseKey, f.rotatedKey)
	f.createSignedTag(t, firstReleaseTag, f.releaseKey)
	f.pushTag(t, firstReleaseTag)
	f.createSignedTag(t, rotatedReleaseTag, f.rotatedKey)
	f.pushTag(t, rotatedReleaseTag)

	historicalOutput, historicalCode := f.runVerification(t, firstReleaseTag)
	rotatedOutput, rotatedCode := f.runVerification(t, rotatedReleaseTag)

	if historicalCode != 0 {
		t.Fatalf("verify-release-tag.sh exit = %d for the pre-rotation tag, want 0\n%s", historicalCode, historicalOutput)
	}
	if rotatedCode != 0 {
		t.Fatalf("verify-release-tag.sh exit = %d for the rotated tag, want 0\n%s", rotatedCode, rotatedOutput)
	}
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	requireGPG(t)

	root := t.TempDir()
	// gpg-agent refuses to start when its socket path exceeds the platform's sun_path limit, and
	// t.TempDir() embeds the test name, so the keyring gets a short directory of its own.
	signingHome, err := os.MkdirTemp("", "hgpg-")
	if err != nil {
		t.Fatalf("create GNUPGHOME: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("gpgconf", "--homedir", signingHome, "--kill", "all").Run()
		_ = os.RemoveAll(signingHome)
	})
	f := &releaseFixture{
		workDir:     filepath.Join(root, "work"),
		cloneDir:    filepath.Join(root, "clone"),
		signingHome: signingHome,
	}
	// Keep the fixture hermetic: the machine's own git identity, signing key, and hooks must not
	// decide whether these tests pass.
	emptyConfig := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(emptyConfig, nil, 0o600); err != nil {
		t.Fatalf("write empty git config: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", emptyConfig)

	f.releaseKey = generateSigningKey(t, f.signingHome, "Release Signer <release@example.com>")
	f.rotatedKey = generateSigningKey(t, f.signingHome, "Rotated Signer <rotated@example.com>")

	originDir := filepath.Join(root, "origin.git")
	f.git(t, root, "init", "--bare", "--initial-branch=main", originDir)
	f.git(t, root, "init", "--initial-branch=main", f.workDir)
	f.git(t, f.workDir, "config", "user.name", "Release Test")
	f.git(t, f.workDir, "config", "user.email", "release@example.com")
	readme := filepath.Join(f.workDir, "README.md")
	if err := os.WriteFile(readme, []byte("# fixture\n"), 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	f.git(t, f.workDir, "add", "README.md")
	f.git(t, f.workDir, "commit", "-m", "init")
	f.git(t, f.workDir, "remote", "add", "origin", originDir)
	f.git(t, f.workDir, "push", "origin", "main")
	f.git(t, root, "clone", originDir, f.cloneDir)
	return f
}

// commitSigningKeys commits an armored keyring holding every given key to main, where the
// release workflow reads the trusted keys from.
func (f *releaseFixture) commitSigningKeys(t *testing.T, fingerprints ...string) {
	t.Helper()

	args := append([]string{"--batch", "--homedir", f.signingHome, "--armor", "--export"}, fingerprints...)
	armored, err := exec.Command("gpg", args...).Output()
	if err != nil {
		t.Fatalf("export signing keys: %v", err)
	}
	if len(armored) == 0 {
		t.Fatal("exported signing keyring is empty")
	}

	path := filepath.Join(f.workDir, releaseSigningKeyFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create %s directory: %v", releaseSigningKeyFile, err)
	}
	if err := os.WriteFile(path, armored, 0o600); err != nil {
		t.Fatalf("write %s: %v", releaseSigningKeyFile, err)
	}
	f.git(t, f.workDir, "add", releaseSigningKeyFile)
	f.git(t, f.workDir, "commit", "-m", "commit the release signing key")
	f.git(t, f.workDir, "push", "origin", "main")
}

func (f *releaseFixture) createSignedTag(t *testing.T, name, fingerprint string) {
	t.Helper()

	cmd := exec.Command("git", "-c", "user.signingkey="+fingerprint, "tag", "-s", name, "-m", name)
	cmd.Dir = f.workDir
	cmd.Env = append(os.Environ(), "GNUPGHOME="+f.signingHome)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create signed tag %s: %v\n%s", name, err, output)
	}
}

func (f *releaseFixture) createAnnotatedTag(t *testing.T, name string) {
	t.Helper()

	f.git(t, f.workDir, "tag", "-a", name, "-m", name)
}

func (f *releaseFixture) pushTag(t *testing.T, name string) {
	t.Helper()

	f.git(t, f.workDir, "push", "origin", "refs/tags/"+name)
}

// runVerification runs the script from the clone, the way the release workflow does, and returns
// its combined output with the exit code.
func (f *releaseFixture) runVerification(t *testing.T, tag string) (string, int) {
	t.Helper()

	script, err := filepath.Abs(verifyReleaseTagScript)
	if err != nil {
		t.Fatalf("resolve %s: %v", verifyReleaseTagScript, err)
	}
	//nolint:gosec // The script path and tag come from this test package.
	cmd := exec.Command("sh", script, tag)
	cmd.Dir = f.cloneDir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err = cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return output.String(), 0
	case errors.As(err, &exitErr):
		return output.String(), exitErr.ExitCode()
	default:
		t.Fatalf("run %s: %v\n%s", verifyReleaseTagScript, err, output.String())
		return "", 0
	}
}

func (f *releaseFixture) git(t *testing.T, dir string, args ...string) {
	t.Helper()

	//nolint:gosec // The fixture only runs git against repositories under t.TempDir.
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, output)
	}
}

func generateSigningKey(t *testing.T, home, userID string) string {
	t.Helper()

	generate := exec.Command("gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase", "",
		"--homedir", home, "--quick-generate-key", userID, "default", "default", "0")
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate signing key %q: %v\n%s", userID, err, output)
	}

	list := exec.Command("gpg", "--batch", "--homedir", home, "--with-colons", "--list-keys", userID)
	output, err := list.Output()
	if err != nil {
		t.Fatalf("list signing key %q: %v", userID, err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, "fpr:") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) > 9 {
			return fields[9]
		}
	}
	t.Fatalf("no fingerprint in gpg output for %q:\n%s", userID, output)
	return ""
}

func requireGPG(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("gpg"); err != nil {
		if os.Getenv("HERDR_TEST_REQUIRE_GPG") == "1" {
			t.Fatal("HERDR_TEST_REQUIRE_GPG=1 but gpg is not on PATH")
		}
		t.Skip("gpg is not on PATH; the release tag verification tests need it")
	}
}
