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

// sourceRepository makes a small checkout with the files the source check
// names, committed so git ls-files lists them, and returns its directory.
func sourceRepository(t *testing.T) string {
	t.Helper()
	script, err := os.ReadFile("release/check-source.sh")
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":                          "module witmoot\n",
		".goreleaser.yaml":                "version: 2\n",
		"cmd/witmoot/main.go":             "package main\n",
		"README.md":                       "A board with spaces in its docs\n",
		"docs/a file with spaces.md":      "Spacing\n",
		"scripts/release/check-source.sh": string(script),
	} {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "init", "-q")
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Witmoot Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "Source")
	return repo
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s (%v)", args, out, err)
	}
}

// The release check accepts the source archive GoReleaser makes with git
// archive, and refuses one that differs from the tracked files in any way.
func TestSourceArchiveMustMatchTrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	repo := sourceRepository(t)
	check := func(archive string) (string, error) {
		cmd := exec.Command("sh", filepath.Join(repo, "scripts/release/check-source.sh"), archive)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	archive := filepath.Join(t.TempDir(), "witmoot_1.0.0_source.tar.gz")
	git(t, repo, "archive", "--format=tar.gz", "--prefix=witmoot_1.0.0_source/", "-o", archive, "HEAD")
	if out, err := check(archive); err != nil {
		t.Fatalf("an exact source archive was refused: %s (%v)", out, err)
	}
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, tree string)
		want   string
	}{
		{"changed file", func(t *testing.T, tree string) { write(t, filepath.Join(tree, "README.md"), "Something else\n") }, "differs: README.md"},
		{"changed file with spaces", func(t *testing.T, tree string) {
			write(t, filepath.Join(tree, "docs/a file with spaces.md"), "Changed\n")
		}, "differs: docs/a file with spaces.md"},
		{"missing file", func(t *testing.T, tree string) { remove(t, filepath.Join(tree, "README.md")) }, "differs: README.md"},
		{"untracked file", func(t *testing.T, tree string) { write(t, filepath.Join(tree, "notes.txt"), "Stray\n") }, "not tracked"},
		{"untracked hidden file", func(t *testing.T, tree string) { write(t, filepath.Join(tree, ".env"), "SECRET=1\n") }, "not tracked"},
		{"stale release configuration", func(t *testing.T, tree string) { write(t, filepath.Join(tree, ".goreleaser.yaml"), "version: 1\n") }, "release configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			staging := t.TempDir()
			tree := filepath.Join(staging, "witmoot_1.0.0_source")
			if out, err := exec.Command("sh", "-c", `mkdir "$1" && tar -xzf "$2" -C "$1" --strip-components=1`, "sh", tree, archive).CombinedOutput(); err != nil {
				t.Fatalf("extract: %s (%v)", out, err)
			}
			tc.change(t, tree)
			changed := filepath.Join(staging, "changed.tar.gz")
			if out, err := exec.Command("tar", "-czf", changed, "-C", staging, "witmoot_1.0.0_source").CombinedOutput(); err != nil {
				t.Fatalf("archive: %s (%v)", out, err)
			}
			out, err := check(changed)
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("a source archive with a %s passed or explained itself poorly: %s (%v)", tc.name, out, err)
			}
		})
	}
	// A checkout that changed after the archive was made is caught too.
	write(t, filepath.Join(repo, "cmd/witmoot/main.go"), "package main\n\nfunc main() {}\n")
	if out, err := check(archive); err == nil || !strings.Contains(out, "differs: cmd/witmoot/main.go") {
		t.Fatalf("an archive older than the checkout passed: %s (%v)", out, err)
	}
	if out, err := check(filepath.Join(repo, "missing.tar.gz")); err == nil || !strings.Contains(out, "Usage") {
		t.Fatalf("a missing archive passed: %s (%v)", out, err)
	}
}

// The artifact check hands the source archive to the source check.
func TestArtifactCheckComparesTheSourceArchive(t *testing.T) {
	script, err := os.ReadFile("release/check-artifacts.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `sh scripts/release/check-source.sh "$1" || exit 1`) {
		t.Fatal("check-artifacts.sh does not compare the source archive with the tracked files")
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// Release builds stamp the version. A template inside a YAML flow list is a
// syntax error unless quoted, which broke a sibling project's release, so the
// stamp is a quoted string in a block list and no flow list holds a template.
func TestReleaseBuildsStampTheVersion(t *testing.T) {
	config, err := os.ReadFile("../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "    ldflags:\n      - \"-s -w -X main.version={{ .Version }}\"\n") {
		t.Fatal("release builds do not stamp main.version from a quoted block-list entry")
	}
	flow := regexp.MustCompile(`:\s*\[[^\]]*\{\{`)
	for i, line := range strings.Split(string(config), "\n") {
		if flow.MatchString(line) {
			t.Errorf(".goreleaser.yaml line %d puts a template in a flow list: %s", i+1, line)
		}
	}
}
