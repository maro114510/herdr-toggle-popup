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

// Test list for scripts/publish-release.sh:
// - no arguments prints the usage and exits non-zero
// - missing gh fails before any release call
// - a release that cannot be read fails and publishes nothing
// - a release that is already published fails and publishes nothing
// - a draft missing a required asset fails, names the asset, and publishes nothing
// - a draft holding all nine required assets is published with gh release edit --draft=false
// - a failed publication fails the script
// - a published release that is not immutable fails and names the repository setting
// - a published release that is immutable succeeds

const (
	publishScriptName = "scripts/publish-release.sh"
	publishTag        = "v1.2.3"
	boolTrue          = "true"
	boolFalse         = "false"

	// darwinAMD64Asset is shared with reproducible_build_script_test.go.
	darwinAMD64Asset = "toggle-popup_darwin_amd64"
)

func publishRequiredAssets() []string {
	return []string{
		darwinAMD64Asset,
		"toggle-popup_darwin_arm64",
		"toggle-popup_linux_amd64",
		"toggle-popup_linux_arm64",
		"sbom_toggle-popup_darwin_amd64.cdx.json",
		"sbom_toggle-popup_darwin_arm64.cdx.json",
		"sbom_toggle-popup_linux_amd64.cdx.json",
		"sbom_toggle-popup_linux_arm64.cdx.json",
		"checksums.txt",
	}
}

// draftWithEveryAsset is a draft release that holds every required asset and will report itself
// immutable once published, so a test changes only the precondition it is about.
func draftWithEveryAsset() fakeRelease {
	return fakeRelease{draft: boolTrue, immutable: boolTrue, assets: publishRequiredAssets(), viewFails: false, editFails: false}
}

//nolint:paralleltest // Each test spawns shell processes; running them in parallel starves internal/doctor's 3s command timeout.
func TestPublishScriptWithoutArgumentsPrintsUsage(t *testing.T) {
	stderr, err := runPublishScript(t, "")

	if err == nil {
		t.Fatal("publish-release.sh succeeded without arguments; want failure")
	}
	want := "usage: sh scripts/publish-release.sh <tag>"
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
}

//nolint:paralleltest // Each test spawns shell processes; running them in parallel starves internal/doctor's 3s command timeout.
func TestPublishScriptWithoutGHFails(t *testing.T) {
	stderr, err := runPublishScript(t, newToolPath(t, nil), publishTag)

	if err == nil {
		t.Fatal("publish-release.sh succeeded without gh; want failure")
	}
	if !strings.Contains(stderr, "gh is required") {
		t.Fatalf("stderr = %q, want it to say gh is required", stderr)
	}
}

//nolint:paralleltest // Each test spawns shell processes; running them in parallel starves internal/doctor's 3s command timeout.
func TestPublishScriptWithUnreadableReleaseFailsAndPublishesNothing(t *testing.T) {
	fake := installFakeGH(t, withViewFailing(draftWithEveryAsset()))

	stderr, err := runPublishScript(t, fake.pathDir, publishTag)

	if err == nil {
		t.Fatal("publish-release.sh succeeded when the release could not be read; want failure")
	}
	if want := "cannot read release " + publishTag; !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
	assertNotPublished(t, fake)
}

//nolint:paralleltest // Each test spawns shell processes; running them in parallel starves internal/doctor's 3s command timeout.
func TestPublishScriptWithAlreadyPublishedReleaseFailsAndPublishesNothing(t *testing.T) {
	fake := installFakeGH(t, withDraft(draftWithEveryAsset(), boolFalse))

	stderr, err := runPublishScript(t, fake.pathDir, publishTag)

	if err == nil {
		t.Fatal("publish-release.sh succeeded on a published release; want failure")
	}
	if want := "release " + publishTag + " is not a draft"; !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
	assertNotPublished(t, fake)
}

//nolint:paralleltest // Each test spawns shell processes; running them in parallel starves internal/doctor's 3s command timeout.
func TestPublishScriptWithMissingAssetFailsAndPublishesNothing(t *testing.T) {
	for _, missing := range publishRequiredAssets() {
		t.Run(missing, func(t *testing.T) {
			present := make([]string, 0, len(publishRequiredAssets()))
			for _, asset := range publishRequiredAssets() {
				if asset != missing {
					present = append(present, asset)
				}
			}
			fake := installFakeGH(t, withAssets(draftWithEveryAsset(), present))

			stderr, err := runPublishScript(t, fake.pathDir, publishTag)

			if err == nil {
				t.Fatalf("publish-release.sh succeeded without %s; want failure", missing)
			}
			if want := "missing asset " + missing; !strings.Contains(stderr, want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
			}
			assertNotPublished(t, fake)
		})
	}
}

//nolint:paralleltest // Each test spawns shell processes; running them in parallel starves internal/doctor's 3s command timeout.
func TestPublishScriptPublishesDraftHoldingEveryRequiredAsset(t *testing.T) {
	extra := append([]string{"notes.txt"}, publishRequiredAssets()...)
	fake := installFakeGH(t, withAssets(draftWithEveryAsset(), extra))

	_, err := runPublishScript(t, fake.pathDir, publishTag)
	if err != nil {
		t.Fatalf("publish-release.sh failed: %v", err)
	}

	log := readLog(t, fake.log)
	if want := "release\nedit\n" + publishTag + "\n--draft=false\n"; !strings.Contains(log, want) {
		t.Errorf("gh log = %q, want it to contain %q", log, want)
	}
	if got := strings.Count(log, "edit\n"); got != 1 {
		t.Errorf("gh release edit invocations = %d, want 1", got)
	}
}

//nolint:paralleltest // Each test spawns shell processes; running them in parallel starves internal/doctor's 3s command timeout.
func TestPublishScriptFailsWhenPublicationFails(t *testing.T) {
	fake := installFakeGH(t, withEditFailing(draftWithEveryAsset()))

	stderr, err := runPublishScript(t, fake.pathDir, publishTag)

	if err == nil {
		t.Fatal("publish-release.sh succeeded when publication failed; want failure")
	}
	if want := "cannot publish release " + publishTag; !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
}

//nolint:paralleltest // Each test spawns shell processes; running them in parallel starves internal/doctor's 3s command timeout.
func TestPublishScriptFailsWhenPublishedReleaseIsNotImmutable(t *testing.T) {
	fake := installFakeGH(t, withImmutable(draftWithEveryAsset(), boolFalse))

	stderr, err := runPublishScript(t, fake.pathDir, publishTag)

	if err == nil {
		t.Fatal("publish-release.sh succeeded for a mutable release; want failure")
	}
	if want := "release " + publishTag + " is not immutable; enable immutable releases in the repository settings"; !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
}

type fakeRelease struct {
	draft     string
	immutable string
	assets    []string
	viewFails bool
	editFails bool
}

func withViewFailing(r fakeRelease) fakeRelease {
	r.viewFails = true

	return r
}

func withEditFailing(r fakeRelease) fakeRelease {
	r.editFails = true

	return r
}

func withDraft(r fakeRelease, draft string) fakeRelease {
	r.draft = draft

	return r
}

func withImmutable(r fakeRelease, immutable string) fakeRelease {
	r.immutable = immutable

	return r
}

func withAssets(r fakeRelease, assets []string) fakeRelease {
	r.assets = assets

	return r
}

type fakeGH struct {
	pathDir string
	log     string
}

// installFakeGH installs a stand-in for gh on a PATH that holds nothing else, so a test never
// reaches the gh installed on the machine or the network. It answers the release queries from
// release and appends every call's arguments, one per line, to its log.
func installFakeGH(t *testing.T, release fakeRelease) fakeGH {
	t.Helper()

	fake := fakeGH{
		pathDir: newToolPath(t, nil),
		log:     filepath.Join(t.TempDir(), "gh.log"),
	}
	writeFakeTool(t, fake.pathDir, "gh", fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
  printf '%%s\n' "$arg" >> %q
done
printf '%%s\n' '---' >> %q
case "$1 $2" in
  "release view")
    test %t = false || exit 1
    case "$*" in
      *isDraft*) printf '%%s\n' %q ;;
      *isImmutable*) printf '%%s\n' %q ;;
      *assets*) printf '%%s\n' %s ;;
    esac
    ;;
  "release edit")
    test %t = false || exit 1
    ;;
esac
`, fake.log, fake.log, release.viewFails, release.draft, release.immutable, shellWords(release.assets), release.editFails))

	return fake
}

func shellWords(words []string) string {
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		quoted = append(quoted, fmt.Sprintf("%q", word))
	}

	return strings.Join(quoted, " ")
}

func assertNotPublished(t *testing.T, fake fakeGH) {
	t.Helper()
	if log := readLog(t, fake.log); strings.Contains(log, "edit\n") {
		t.Fatalf("gh release edit ran; log = %q", log)
	}
}

// runPublishScript runs the publish script with args on a PATH holding only pathDir. An empty
// pathDir keeps the test's own PATH.
func runPublishScript(t *testing.T, pathDir string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command("sh", append([]string{publishScriptName}, args...)...) //nolint:gosec // Arguments come from the test, not from outside input.
	if pathDir != "" {
		cmd.Env = append(os.Environ(), "PATH="+pathDir)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	return stderr.String(), err
}
