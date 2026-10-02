// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeBubblewrap(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "arguments")
	fake := filepath.Join(dir, "bwrap")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + "'" + record + "'\n"
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return fake, record
}

func TestSandboxCheckRunsTheBoundServer(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the sandbox refuses to run as root")
	}
	fake, record := fakeBubblewrap(t)
	t.Setenv("WITMOOT_DATA_DIR", t.TempDir())
	var out bytes.Buffer
	if err := runSandbox([]string{"--check", "--bwrap", fake}, &out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(got), "\n--\n/app/server\n--help\n") || strings.Contains(string(got), "/usr/bin/true") {
		t.Fatalf("check did not run the bound server: %s", got)
	}
}

func TestSandboxRejectsRetiredMountsAndBadCertificateBundles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the sandbox refuses to run as root")
	}
	fake, record := fakeBubblewrap(t)
	t.Setenv("WITMOOT_DATA_DIR", t.TempDir())
	for _, args := range [][]string{
		{"--check", "--bwrap", fake, "--write-dir", t.TempDir()},
		{"--check", "--bwrap", fake, "--read-file", "/etc/hosts"},
	} {
		if err := runSandbox(args, &bytes.Buffer{}); err == nil {
			t.Errorf("accepted retired option %q", args[3])
		}
	}
	for _, bundle := range []string{"relative.pem", "/missing/ca.pem", t.TempDir(), "/tmp/ca\n.pem"} {
		t.Setenv("SSL_CERT_FILE", bundle)
		if err := runSandbox([]string{"--check", "--bwrap", fake}, &bytes.Buffer{}); err == nil {
			t.Errorf("accepted certificate bundle %q", bundle)
		}
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("invalid settings still launched Bubblewrap")
	}
}
