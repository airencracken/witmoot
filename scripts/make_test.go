package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMakeDefaultsToHelpWithoutStartingGo(t *testing.T) {
	work := t.TempDir()
	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "Makefile"), makefile, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "go"), []byte("#!/bin/sh\necho 'help must not invoke go' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"help"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, "make", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "PATH="+work+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("make %v: %s (%v)", args, out, err)
		}
		for _, want := range []string{"demo", "run", "check", "build", "install-openrc", "release-snapshot", "PORT=9000", "DESTDIR"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("help is missing %q: %s", want, out)
			}
		}
	}
}
