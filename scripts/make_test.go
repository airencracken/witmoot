package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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
	cmd.Env = slices.DeleteFunc(os.Environ(), func(setting string) bool {
		name, _, _ := strings.Cut(setting, "=")
		switch name {
		case "GORELEASER", "VERSION", "MAKEFLAGS", "MAKEOVERRIDES", "MFLAGS":
			return true
		}
		return false
	})
	cmd.Env = append(cmd.Env, "PATH="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n %v: %s (%v)", args, out, err)
	}
	return string(out)
}

func TestMakeDryRunIsolatesCallerOverrides(t *testing.T) {
	t.Setenv("GORELEASER", "/caller/goreleaser")
	t.Setenv("VERSION", "caller-version")
	t.Setenv("MAKEFLAGS", "GORELEASER=/caller/makeflags")
	t.Setenv("MAKEOVERRIDES", "VERSION=caller-override")
	t.Setenv("MFLAGS", "-s")
	path := t.TempDir()
	if out := makeDryRun(t, path, "release-check"); !strings.HasPrefix(out, "GOWORK=off go run ") || strings.Contains(out, "caller") {
		t.Fatalf("caller environment changed tool discovery: %q", out)
	}
	if out := makeDryRun(t, path, "release-check", "GORELEASER=/selected/tool"); out != "GOWORK=off /selected/tool check\n" {
		t.Fatalf("explicit override was lost: %q", out)
	}
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
