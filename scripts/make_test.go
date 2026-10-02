package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestMakeDefaultsToHelpWithoutStartingGo(t *testing.T) {
	work := t.TempDir()
	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "Makefile"), makefile, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "go"), []byte("#!/bin/sh\necho 'help must not invoke go' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"help"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, "make", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "PATH="+work+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("make %v: %s (%v)", args, out, err)
		}
		for _, want := range []string{"demo", "run", "check", "build", "install-openrc", "release-snapshot", "PORT=9000", "DESTDIR"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("help is missing %q: %s", want, out)
			}
		}
	}
}

// makeDryRun prints the commands a target would run in a copy of the Makefile,
// with only dir on PATH for the shell commands make expands.
func makeDryRun(t *testing.T, path string, args ...string) string {
	t.Helper()
	makeBinary, err := exec.LookPath("make")
	if err != nil {
		t.Skip("needs make")
	}
	work := t.TempDir()
	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "Makefile"), makefile, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(makeBinary, append([]string{"-n", "--no-print-directory"}, args...)...)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "PATH="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n %v: %s (%v)", args, out, err)
	}
	return string(out)
}

// make check validates the release configuration with the installed
// GoReleaser, or else runs the release the workflow pins.
func TestMakeCheckValidatesTheReleaseConfiguration(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	pinned := regexp.MustCompile(`goreleaser-action@\S+ # v\d+\n\s+with:\n\s+version: (v[0-9.]+)\n`).FindStringSubmatch(string(workflow))
	if pinned == nil {
		t.Fatal("the workflow does not pin a GoReleaser version")
	}
	empty := t.TempDir()
	want := "GOWORK=off go run github.com/goreleaser/goreleaser/v2@" + pinned[1] + " check\n"
	if out := makeDryRun(t, empty, "release-check"); out != want {
		t.Fatalf("without GoReleaser installed, release-check runs %q, want %q", out, want)
	}
	if out := makeDryRun(t, empty, "check"); !strings.Contains(out, want) {
		t.Fatalf("make check does not validate the release configuration: %s", out)
	}
	tools := t.TempDir()
	goreleaser := filepath.Join(tools, "goreleaser")
	if err := os.WriteFile(goreleaser, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if out := makeDryRun(t, tools, "release-check"); out != "GOWORK=off "+goreleaser+" check\n" {
		t.Fatalf("with GoReleaser installed, release-check runs %q", out)
	}
	if out := makeDryRun(t, empty, "release-check", "GORELEASER=/opt/goreleaser"); out != "GOWORK=off /opt/goreleaser check\n" {
		t.Fatalf("GORELEASER is not honoured: %q", out)
	}
}

// Builds from the Makefile carry the version they report at startup.
func TestMakeBuildStampsTheVersion(t *testing.T) {
	path := os.Getenv("PATH")
	if out := makeDryRun(t, path, "build", "VERSION=v1.2.3-4-gabcdef"); !strings.Contains(out, `-ldflags "-X main.version=v1.2.3-4-gabcdef"`) {
		t.Fatalf("build does not stamp the version: %s", out)
	}
	// Outside a git checkout there is nothing to describe, and the binary
	// falls back to its module version or devel.
	if out := makeDryRun(t, path, "build"); !strings.Contains(out, `-ldflags "-X main.version="`) {
		t.Fatalf("build without a version: %s", out)
	}
}
