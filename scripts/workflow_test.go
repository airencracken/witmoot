package scripts_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Workflow steps run without an implicit -e, so each command must check its
// own status. A command that does not would let a failed step pass.
func TestWorkflowCommandsCheckTheirStatus(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	if !strings.Contains(text, "defaults:\n  run:\n    shell: bash --noprofile --norc {0}\n") {
		t.Fatal("workflow does not set an explicit shell without -e")
	}
	lines := strings.Split(text, "\n")
	checked := regexp.MustCompile(`(\|\| exit 1|\|\| \{.*exit 1; \}|exit 1 ;;|exit 1;? ?\}?)$`)
	structural := regexp.MustCompile(`^(if |then|else|fi|case |esac|;;|\S+\)$|refs/tags/v\*\)|\*\)|\*-\*\)|'$|docker run .*\\$|-e .*\\$|debian:.*'$|sh scripts/release/test-deb\.sh .*)`)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)
		if trimmed == "run: |" {
			for i++; i < len(lines); i++ {
				command := strings.TrimSpace(lines[i])
				if command == "" {
					continue
				}
				if len(lines[i])-len(strings.TrimLeft(lines[i], " ")) <= indent {
					i--
					break
				}
				if !checked.MatchString(command) && !structural.MatchString(command) {
					t.Errorf("line %d does not check its status: %s", i+1, command)
				}
			}
		} else if command, ok := strings.CutPrefix(trimmed, "run: "); ok && !strings.HasSuffix(command, "|| exit 1") {
			t.Errorf("line %d does not check its status: %s", i+1, command)
		}
	}
}

// make check validates the release configuration, so GoReleaser is installed
// first: building it from source needs a newer Go than the pinned toolchain.
func TestWorkflowInstallsGoReleaserBeforeTheChecks(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	install := strings.Index(text, "uses: goreleaser/goreleaser-action@")
	checks := strings.Index(text, "make check || exit 1")
	if install < 0 || checks < 0 || install > checks {
		t.Fatalf("GoReleaser is installed at %d, after make check at %d", install, checks)
	}
	if !strings.Contains(text[install:checks], "install-only: true") {
		t.Fatal("the GoReleaser step before the checks does not only install it")
	}
}

// The module, the container build, CI, and the documentation agree on the Go
// release, so the image is not built with a different Go than CI tests.
func TestGoVersionIsConsistent(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	find := func(name, text, pattern string) string {
		t.Helper()
		match := regexp.MustCompile(pattern).FindStringSubmatch(text)
		if match == nil {
			t.Fatalf("%s does not name a Go version matching %s", name, pattern)
		}
		return match[1]
	}
	module := read("../go.mod")
	want := find("go.mod", module, `(?m)^go (1\.\d+)\.\d+$`)
	for name, got := range map[string]string{
		"go.mod toolchain": find("go.mod", module, `(?m)^toolchain go(1\.\d+)\.\d+$`),
		"Dockerfile":       find("Dockerfile", read("../Dockerfile"), `(?m)^FROM golang:(1\.\d+)(?:\.\d+)?-alpine\b`),
		"release workflow": find("release.yml", read("../.github/workflows/release.yml"), `go-version: "(1\.\d+)\.x"`),
		"README":           find("README.md", read("../README.md"), `Requires Go (1\.\d+) or later`),
		"deployment guide": find("docs/deployment.md", read("../docs/deployment.md"), `Go (1\.\d+) or later`),
	} {
		if got != want {
			t.Errorf("%s uses Go %s, but go.mod requires Go %s", name, got, want)
		}
	}
}
