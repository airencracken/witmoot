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
