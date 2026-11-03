package sessions

import (
	"bytes"
	"strings"
	"testing"
)

// Test list:
//
// - cleanup: removes only the explicitly selected session
// - cleanup: rejects a session without ownership metadata
// - cleanup: rejects a session the plugin did not create
// - cleanup: refuses an attached target unless --force is given
// - cleanup: deletes nothing when any target cannot be validated
// - cleanup: rejects a call without a target

const (
	cleanupTarget     = "herdr-toggle-popup-00000000000000000000000000000021"
	cleanupBystander  = "herdr-toggle-popup-00000000000000000000000000000022"
	cleanupLegacy     = "herdr-toggle-popup-00000000000000000000000000000023"
	cleanupMissing    = "herdr-toggle-popup-00000000000000000000000000000024"
	cleanupNonPlugin  = "notes-00000000000000000000000000000025"
	cleanupUsageError = "usage: toggle-popup cleanup [--force] <session>...\n"
)

func runCleanup(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := RunCleanup(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunCleanupRemovesOnlySelectedSessions(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)
	newServerSession(t, cleanupTarget, ownership(namespaceAlpha, "workspace"))
	newServerSession(t, cleanupBystander, ownership(namespaceBeta, "workspace"))

	code, stdout, stderr := runCleanup(t, cleanupTarget)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if want := "cleanup: removed " + cleanupTarget + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	assertSessionMissing(t, cleanupTarget)
	assertSessionPresent(t, cleanupBystander)
}

func TestRunCleanupRejectsOwnershipUnknownSessions(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)
	newServerSession(t, cleanupLegacy, nil)
	newServerSession(t, cleanupBystander, ownership(namespaceAlpha, "workspace"))

	code, stdout, stderr := runCleanup(t, cleanupLegacy)

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for a session without ownership metadata")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown ownership") {
		t.Errorf("stderr = %q, want it to name the unknown ownership", stderr)
	}
	assertSessionPresent(t, cleanupLegacy)
	assertSessionPresent(t, cleanupBystander)
}

func TestRunCleanupRejectsNonPluginSessions(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)
	newServerSession(t, cleanupNonPlugin, nil)
	newServerSession(t, cleanupBystander, ownership(namespaceAlpha, "workspace"))

	code, stdout, stderr := runCleanup(t, cleanupNonPlugin)

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for a non-plugin session")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "not a plugin session") {
		t.Errorf("stderr = %q, want it to name the non-plugin target", stderr)
	}
	assertSessionPresent(t, cleanupNonPlugin)
	assertSessionPresent(t, cleanupBystander)
}

func TestRunCleanupRefusesAttachedSessionsWithoutForce(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)
	newServerSession(t, cleanupTarget, ownership(namespaceAlpha, "workspace"))
	newServerSession(t, cleanupBystander, ownership(namespaceBeta, "workspace"))
	attachDetachedServerClient(t, cleanupTarget)

	code, stdout, stderr := runCleanup(t, cleanupTarget)

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero while a client is attached")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{"attached", "--force"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to mention %q", stderr, want)
		}
	}
	assertSessionPresent(t, cleanupTarget)
	assertSessionPresent(t, cleanupBystander)

	code, stdout, stderr = runCleanup(t, cleanupTarget, "--force")

	if code != 0 {
		t.Fatalf("exit code = %d with --force, want 0; stderr=%q", code, stderr)
	}
	if want := "cleanup: removed " + cleanupTarget + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	assertSessionMissing(t, cleanupTarget)
	assertSessionPresent(t, cleanupBystander)
}

func TestRunCleanupDeletesNothingWhenATargetCannotBeValidated(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)
	newServerSession(t, cleanupTarget, ownership(namespaceAlpha, "workspace"))

	code, stdout, stderr := runCleanup(t, cleanupTarget, cleanupMissing)

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for a target that does not exist")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "no such saved session") {
		t.Errorf("stderr = %q, want it to name the missing target", stderr)
	}
	assertSessionPresent(t, cleanupTarget)
}

func TestRunCleanupRejectsMissingTargets(t *testing.T) {
	code, stdout, stderr := runCleanup(t)

	if code == 0 {
		t.Error("exit code = 0, want non-zero")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if stderr != cleanupUsageError {
		t.Errorf("stderr = %q, want %q", stderr, cleanupUsageError)
	}
}
