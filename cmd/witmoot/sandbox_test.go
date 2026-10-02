// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
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

// sandboxCheckArguments runs the sandbox check against a fake Bubblewrap and
// returns the arguments it was given, one per element.
func sandboxCheckArguments(t *testing.T) []string {
	t.Helper()
	fake, record := fakeBubblewrap(t)
	if err := runSandbox([]string{"--check", "--bwrap", fake}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(got), "\n"), "\n")
}

// Witmoot never builds sandboxes of its own, so the confined server must not
// be able to create user namespaces either.
func TestSandboxDisablesNestedUserNamespaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the sandbox refuses to run as root")
	}
	t.Setenv("WITMOOT_DATA_DIR", t.TempDir())
	args := sandboxCheckArguments(t)
	separator := slices.Index(args, "--")
	if separator < 0 || !slices.Contains(args[:separator], "--disable-userns") {
		t.Fatalf("the service policy leaves user namespaces available: %q", args)
	}
	if !slices.Contains(args[:separator], "--unshare-user") {
		t.Fatalf("--disable-userns needs its own user namespace: %q", args)
	}
}

// A private CA bundle named by SSL_CERT_FILE reaches the server read-only at a
// fixed path, and the data directory is the only writable host path.
func TestSandboxBindsTheCustomCertificateBundle(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the sandbox refuses to run as root")
	}
	data := t.TempDir()
	bundle := filepath.Join(t.TempDir(), "private ca.pem")
	if err := os.WriteFile(bundle, []byte("-----BEGIN CERTIFICATE-----\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WITMOOT_DATA_DIR", data)
	t.Setenv("SSL_CERT_FILE", bundle)
	args := sandboxCheckArguments(t)
	joined := "\n" + strings.Join(args, "\n") + "\n"
	if !strings.Contains(joined, "\n--ro-bind\n"+bundle+"\n/app/ca-bundle.crt\n") {
		t.Fatalf("custom bundle not bound read-only: %q", args)
	}
	if strings.Count(joined, "\n--bind\n") != 1 || !strings.Contains(joined, "\n--bind\n"+data+"\n"+data+"\n") {
		t.Fatalf("the data directory is not the one writable mount: %q", args)
	}
}
