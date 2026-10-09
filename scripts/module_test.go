// SPDX-License-Identifier: AGPL-3.0-or-later

package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// goMod is the part of `go mod edit -json` these tests read.
type goMod struct {
	Require []struct{ Path, Version string }
	Replace []struct {
		Old, New struct{ Path, Version string }
	}
}

func readGoMod(t *testing.T, path string) goMod {
	t.Helper()
	cmd := exec.Command("go", "mod", "edit", "-json", path)
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go mod edit -json %s: %v", path, err)
	}
	var mod goMod
	if err := json.Unmarshal(out, &mod); err != nil {
		t.Fatal(err)
	}
	return mod
}

// Release builds, the Gentoo ebuilds and Docker images resolve comfylib
// through the module proxy. A replace directive would point them at a path
// that exists only on a developer's machine, or at an unreviewed fork.
func TestGoModHasNoReplaceDirectives(t *testing.T) {
	if mod := readGoMod(t, "../go.mod"); len(mod.Replace) != 0 {
		t.Fatalf("go.mod replaces modules: %+v; use an untracked go.work for local development", mod.Replace)
	}
}

// The test above must notice every form a replace can take.
func TestReplaceCheckSeesEveryForm(t *testing.T) {
	for _, directive := range []string{
		"replace github.com/airencracken/comfylib => ../comfylib\n",
		"replace github.com/airencracken/comfylib v0.1.0 => github.com/example/fork v0.1.3\n",
		"replace (\n\tgithub.com/airencracken/comfylib => ./vendor/comfylib\n)\n",
	} {
		path := t.TempDir() + "/go.mod"
		if err := os.WriteFile(path, []byte("module example.org/app\n\ngo 1.26\n\n"+directive), 0o600); err != nil {
			t.Fatal(err)
		}
		if mod := readGoMod(t, path); len(mod.Replace) != 1 {
			t.Errorf("missed %q", directive)
		}
	}
}

// Each app pins an exact comfylib release, never a pseudo-version of an
// unreleased commit.
func TestComfylibIsPinnedToARelease(t *testing.T) {
	for _, req := range readGoMod(t, "../go.mod").Require {
		if req.Path == "github.com/airencracken/comfylib" {
			if req.Version != "v0.1.4" {
				t.Fatalf("comfylib is pinned to %s, want v0.1.4", req.Version)
			}
			return
		}
	}
	t.Fatal("go.mod does not require comfylib")
}

// A workspace file is for one developer's checkout; committed, it would
// silently redirect every build that uses the tree.
func TestNoWorkspaceFileIsTracked(t *testing.T) {
	ignore, err := os.ReadFile("../.gitignore")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"/go.work", "/go.work.sum"} {
		if !strings.Contains("\n"+string(ignore)+"\n", "\n"+name+"\n") {
			t.Errorf(".gitignore does not ignore %s", name)
		}
	}
	if _, err := os.Stat("../.git"); err != nil {
		t.Skip("not a git checkout, so nothing is tracked")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("needs git")
	}
	out, err := exec.Command(git, "-C", "..", "ls-files", "--", "go.work", "go.work.sum", "*/go.work", "*/go.work.sum").Output()
	if err != nil {
		t.Fatal(err)
	}
	if tracked := strings.TrimSpace(string(out)); tracked != "" {
		t.Fatalf("workspace files are tracked: %s", tracked)
	}
}
