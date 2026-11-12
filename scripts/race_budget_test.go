package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseRaceBudgetAndFocusedTimeoutOverride(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?s)\n  build:\n    env:\n(?:      #[^\n]*\n)*      GOFLAGS: "([^"]+)"\n    runs-on:`).FindSubmatch(workflow)
	if match == nil || string(match[1]) != "-timeout=30m" {
		t.Fatal("the hosted full race suite needs a bounded thirty-minute package budget")
	}
	dir := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":    "module budget\n\ngo 1.26.0\n",
		"budget.go": "package budget\n",
		"budget_test.go": `package budget
import ("flag"; "os"; "testing"; "time")
func TestBudget(t *testing.T) {
 got, err := time.ParseDuration(flag.Lookup("test.timeout").Value.String())
 if err != nil { t.Fatal(err) }
 want, err := time.ParseDuration(os.Getenv("EXPECTED_TIMEOUT"))
 if err != nil || got != want { t.Fatalf("timeout=%s, want %s",got,want) }
}
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOFLAGS=") && !strings.HasPrefix(entry, "GOWORK=") && !strings.HasPrefix(entry, "EXPECTED_TIMEOUT=") {
			env = append(env, entry)
		}
	}
	env = append(env, "GOWORK=off", "GOFLAGS="+string(match[1]))
	for _, check := range []struct {
		args []string
		want string
	}{
		{[]string{"build", "./..."}, "30m"},
		{[]string{"test", "-count=1", "./..."}, "30m"},
		{[]string{"test", "-count=1", "-timeout=5s", "./..."}, "5s"},
	} {
		cmd := exec.Command("go", check.args...)
		cmd.Dir = dir
		cmd.Env = append(env, "EXPECTED_TIMEOUT="+check.want)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s", check.args, err, out)
		}
	}
}
