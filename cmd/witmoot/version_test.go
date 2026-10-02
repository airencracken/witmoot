package main

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBuildVersionPrefersTheStampedVersion(t *testing.T) {
	stamped := version
	t.Cleanup(func() { version = stamped })
	version = "v1.2.3"
	if got := buildVersion(); got != "v1.2.3" {
		t.Fatalf("buildVersion() = %q, want the stamped version", got)
	}
	version = ""
	// A test binary has no module version, so it reports itself as devel.
	if got := buildVersion(); got != "devel" {
		t.Fatalf("unstamped buildVersion() = %q, want devel", got)
	}
}

// A binary stamped the way the Makefile and GoReleaser stamp it names its
// version in the startup log line operators look for.
func TestStampedServerLogsItsVersionAtStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the server")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "witmoot")
	const stamp = "v9.8.7-test"
	build := exec.Command("go", "build", "-buildvcs=false", "-ldflags", "-X main.version="+stamp, "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := exec.CommandContext(ctx, binary)
	server.Env = append(os.Environ(), "WITMOOT_DATA_DIR="+filepath.Join(root, "data"), "WITMOOT_ADDR=127.0.0.1:0")
	logs, err := server.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	ready := ""
	scanner := bufio.NewScanner(logs)
	for scanner.Scan() {
		if line := scanner.Text(); strings.Contains(line, "Witmoot is ready") {
			ready = line
			break
		}
	}
	if err := server.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// Keep reading so the server never blocks on a full log pipe.
	if _, err := io.Copy(io.Discard, logs); err != nil {
		t.Log(err)
	}
	if err := server.Wait(); err != nil {
		t.Fatalf("server did not stop cleanly: %v", err)
	}
	if !strings.Contains(ready, "version="+stamp) {
		t.Fatalf("startup line does not name the version: %q", ready)
	}
}
