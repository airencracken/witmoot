package openrc_test

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func runService(script string, env ...string) ([]byte, error) {
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "RC_SVCNAME=witmoot-test"}, env...)
	return cmd.CombinedOutput()
}

func TestDefaultsReachDaemon(t *testing.T) {
	out, err := runService(`
. ./witmoot || exit 1
checkpath() { return 0; }
start_pre || exit 1
sh -c 'printf "%s\n%s\n" "$WITMOOT_ADDR" "$WITMOOT_DATA_DIR"; umask'
`)
	if err != nil || string(out) != "127.0.0.1:8082\n/var/lib/witmoot\n0077\n" {
		t.Fatalf("daemon defaults: %s (%v)", out, err)
	}
}

func TestPlainConfigurationAssignmentsReachDaemon(t *testing.T) {
	out, err := runService(`
# These are deliberately not exported, just like plain conf.d assignments.
. ./witmoot.confd || exit 1
WITMOOT_DATA_DIR='/srv/our board'
WITMOOT_ADDR=127.0.0.1:9999
WITMOOT_NAME='Our little corner'
WITMOOT_BASE_URL=https://board.example.org
WITMOOT_SECURE_COOKIES=true
WITMOOT_TRUSTED_PROXIES=127.0.0.1/32,::1/128
WITMOOT_IMVAULT_URL=https://photos.example.org
. ./witmoot || exit 1
checkpath() { return 0; }
start_pre || exit 1
env
`)
	if err != nil {
		t.Fatalf("service: %s (%v)", out, err)
	}
	for _, setting := range []string{
		"WITMOOT_DATA_DIR=/srv/our board", "WITMOOT_ADDR=127.0.0.1:9999",
		"WITMOOT_NAME=Our little corner", "WITMOOT_BASE_URL=https://board.example.org",
		"WITMOOT_SECURE_COOKIES=true", "WITMOOT_TRUSTED_PROXIES=127.0.0.1/32,::1/128",
		"WITMOOT_IMVAULT_URL=https://photos.example.org",
	} {
		if !strings.Contains("\n"+string(out), "\n"+setting+"\n") {
			t.Errorf("daemon did not receive %s: %s", setting, out)
		}
	}
}

func TestRelativePathsRejectedBeforeFilesystemChanges(t *testing.T) {
	for _, setting := range []string{"WITMOOT_DATA_DIR=relative", "WITMOOT_BIN=relative", "WITMOOT_LOG_FILE=relative"} {
		out, err := runService(`
. ./witmoot || exit 1
eerror() { printf '%s\n' "$*"; }
checkpath() { echo UNEXPECTED_WRITE; }
start_pre
`, setting)
		if err == nil || !strings.Contains(string(out), "must be absolute") || strings.Contains(string(out), "UNEXPECTED_WRITE") {
			t.Fatalf("%s: %s (%v)", setting, out, err)
		}
	}
}

func TestDirectoryPreparationFailureStopsStartup(t *testing.T) {
	out, err := runService(`
. ./witmoot || exit 1
checkpath() { printf '%s\n' "$*"; return 1; }
start_pre
`)
	if err == nil || string(out) != "--directory --mode 0700 --owner witmoot:witmoot /var/lib/witmoot\n" {
		t.Fatalf("preparation failure was ignored: %s (%v)", out, err)
	}
}

// Every setting the CLI help documents must reach the daemon from a plain,
// unexported conf.d assignment.
func TestDocumentedSettingsReachDaemon(t *testing.T) {
	source, err := os.ReadFile("../../cmd/witmoot/commands.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "Server configuration uses environment variables:")
	end := strings.Index(string(source), "Examples:")
	if start < 0 || end < start {
		t.Fatal("could not find the documented settings in the CLI help")
	}
	settings := regexp.MustCompile(`(?m)^  ([A-Z][A-Z0-9_]+) `).FindAllStringSubmatch(string(source)[start:end], -1)
	if len(settings) < 10 {
		t.Fatalf("found only %d documented settings", len(settings))
	}
	var script strings.Builder
	for _, setting := range settings {
		fmt.Fprintf(&script, "%s='/value for %s'\n", setting[1], setting[1])
	}
	script.WriteString(". ./witmoot || exit 1\ncheckpath() { return 0; }\nstart_pre || exit 1\nenv\n")
	out, err := runService(script.String())
	if err != nil {
		t.Fatalf("service: %s (%v)", out, err)
	}
	for _, setting := range settings {
		if want := fmt.Sprintf("%s=/value for %s", setting[1], setting[1]); !strings.Contains("\n"+string(out), "\n"+want+"\n") {
			t.Errorf("daemon did not receive documented setting %s", setting[1])
		}
	}
}
