package logrotate_test

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotationWithOpenWriter(t *testing.T) {
	tool, err := exec.LookPath("logrotate")
	if err != nil {
		t.Skip("install logrotate to exercise the rotation integration test")
	}
	rule, err := os.ReadFile("witmoot")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "logs with spaces")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "witmoot.log")
	config, state := filepath.Join(dir, "rule"), filepath.Join(dir, "state")
	// A system-wide dateext setting must not prevent repeated size rotations.
	if err := os.WriteFile(config, []byte("dateext\n"+strings.ReplaceAll(string(rule), "/var/log/witmoot.log", log)), 0600); err != nil {
		t.Fatal(err)
	}
	rotate := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(tool, append(args, "--state", state, config)...).CombinedOutput(); err != nil {
			t.Fatalf("logrotate: %s (%v)", out, err)
		}
	}
	rotate("--force") // Missing logs are harmless.
	writer, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	original, err := writer.Stat()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 15; i++ {
		if _, err := fmt.Fprintf(writer, "record %d\n", i); err != nil {
			t.Fatal(err)
		}
		rotate("--force")
		current, err := os.Stat(log)
		if err != nil || !os.SameFile(original, current) || current.Size() != 0 || current.Mode().Perm() != original.Mode().Perm() {
			t.Fatalf("rotation replaced the writer's file or permissions: %v", err)
		}
		if data, err := os.ReadFile(log + ".1"); err != nil || string(data) != fmt.Sprintf("record %d\n", i) {
			t.Fatalf("record %d was lost: %s (%v)", i, data, err)
		}
	}
	rotate("--force") // Empty logs must not consume an archive slot.
	archives, err := filepath.Glob(log + ".*")
	if err != nil || len(archives) != 14 {
		t.Fatalf("archive count: %v (%v)", archives, err)
	}
	oldest, err := os.Open(log + ".14.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer oldest.Close()
	compressed, err := gzip.NewReader(oldest)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	if data, err := io.ReadAll(compressed); err != nil || string(data) != "record 2\n" {
		t.Fatalf("oldest archive: %s (%v)", data, err)
	}

	if _, err := writer.WriteString("small\n"); err != nil {
		t.Fatal(err)
	}
	rotate()
	if data, err := os.ReadFile(log); err != nil || string(data) != "small\n" {
		t.Fatalf("small log rotated too soon: %s (%v)", data, err)
	}
	if err := writer.Truncate((10 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	rotate()
	if info, err := os.Stat(log + ".1"); err != nil || info.Size() != (10<<20)+1 {
		t.Fatalf("size threshold did not rotate: %v (%v)", info, err)
	}
	if _, err := writer.WriteString("daily\n"); err != nil {
		t.Fatal(err)
	}
	oldState := fmt.Sprintf("logrotate state -- version 2\n%q %s\n", log, time.Now().Add(-48*time.Hour).Format("2006-1-2-15:4:5"))
	if err := os.WriteFile(state, []byte(oldState), 0600); err != nil {
		t.Fatal(err)
	}
	rotate()
	if data, err := os.ReadFile(log + ".1"); err != nil || string(data) != "daily\n" {
		t.Fatalf("daily interval did not rotate: %s (%v)", data, err)
	}
}
