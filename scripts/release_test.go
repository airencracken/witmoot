package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleasePreparationWritesEachDefaultOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("lists dependencies for two platforms")
	}
	out := t.TempDir()
	cmd := exec.Command("sh", "release/prepare.sh")
	cmd.Env = append(os.Environ(), "RELEASE_DIR="+out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prepare: %s (%v)", output, err)
	}
	env, err := os.ReadFile(filepath.Join(out, "witmoot.env"))
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]int{}
	for _, match := range regexp.MustCompile(`(?m)^(WITMOOT_[A-Z_]+)=`).FindAllStringSubmatch(string(env), -1) {
		settings[match[1]]++
	}
	for name, count := range settings {
		if count != 1 {
			t.Errorf("packaged environment sets %s %d times", name, count)
		}
	}
	if settings["WITMOOT_ADDR"] != 1 || !strings.Contains(string(env), "WITMOOT_ADDR=127.0.0.1:8082\n") {
		t.Fatalf("packaged environment does not bind to loopback once: %s", env)
	}
	unit, err := os.ReadFile(filepath.Join(out, "witmoot.service"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unit), "/usr/local/bin") || !strings.Contains(string(unit), `ExecStart="/usr/bin/witmoot"`) || !strings.Contains(string(unit), "StateDirectoryMode=0700\n") {
		t.Fatalf("packaged unit is wrong: %s", unit)
	}
	notices, err := os.ReadFile(filepath.Join(out, "THIRD_PARTY_NOTICES.txt"))
	if err != nil || !strings.Contains(string(notices), "--- internal/forum/static/htmx.LICENSE ---") {
		t.Fatalf("notices are missing htmx: %v", err)
	}
}

// The package gate looks for this application's own startup message.
func TestPackageGateLooksForWitmootLogs(t *testing.T) {
	script, err := os.ReadFile("release/test-deb.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(script)), "imvault") || !strings.Contains(string(script), "Witmoot is ready") {
		t.Fatal("the sandbox journal check looks for another application's log line")
	}
}
