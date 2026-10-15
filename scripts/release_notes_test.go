package scripts_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseGuideMatchesNotesAndDownloadExamples(t *testing.T) {
	guide, err := os.ReadFile("../docs/releases.md")
	if err != nil {
		t.Fatal(err)
	}
	latest := regexp.MustCompile(`The latest release is \[([0-9]+\.[0-9]+\.[0-9]+)\]\(release-notes/([0-9]+\.[0-9]+\.[0-9]+)\.md\)`).FindStringSubmatch(string(guide))
	if len(latest) != 3 || latest[1] != latest[2] {
		t.Fatal("latest version and release-note link disagree")
	}
	notes, err := os.ReadFile(filepath.Join("../docs/release-notes", latest[1]+".md"))
	if err != nil || !strings.HasPrefix(string(notes), "# Witmoot "+latest[1]+"\n") {
		t.Fatal("release notes missing or titled for a different release", err)
	}
	for _, pattern := range []string{`release_version=([0-9]+\.[0-9]+\.[0-9]+)`, `witmoot_([0-9]+\.[0-9]+\.[0-9]+)_linux_amd64`} {
		matches := regexp.MustCompile(pattern).FindAllStringSubmatch(string(guide), -1)
		if len(matches) == 0 {
			t.Fatal("missing installation example", pattern)
		}
		for _, match := range matches {
			if match[1] != latest[1] {
				t.Fatalf("installation example uses %s, latest is %s", match[1], latest[1])
			}
		}
	}
	for _, match := range regexp.MustCompile(`Migration ([0-9]{3})`).FindAllStringSubmatch(string(notes), -1) {
		files, err := filepath.Glob("../internal/forum/migrations/" + match[1] + "_*.sql")
		if err != nil || len(files) != 1 {
			t.Fatal("release notes name an unknown migration", match[1], err)
		}
		if !strings.Contains(string(notes), "backup before upgrading") || !strings.Contains(string(notes), "older binary") {
			t.Fatal("migration notes must explain backups and downgrades")
		}
	}
}
