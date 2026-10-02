// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestServiceHelper(t *testing.T) {
	if os.Getenv("TEST_SANDBOX_HELPER") != "1" {
		return
	}
	data := os.Getenv("TEST_DATA_DIR")
	for _, forbidden := range []string{os.Getenv("TEST_FORBIDDEN"), "/etc/shadow"} {
		if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "sandbox exposed", forbidden, err)
			os.Exit(10)
		}
	}
	if os.Getenv("UNRELATED_PASSWORD") != "" || os.Getenv("LD_PRELOAD") != "" {
		os.Exit(11)
	}
	response, err := http.Get(os.Getenv("TEST_HTTP"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(12)
	}
	if err := response.Body.Close(); err != nil || response.StatusCode != 200 {
		os.Exit(13)
	}
	// The private CA reaches the server only through SSL_CERT_FILE.
	response, err = http.Get(os.Getenv("TEST_HTTPS"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(16)
	}
	if err := response.Body.Close(); err != nil || response.StatusCode != 200 {
		os.Exit(17)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	if err := os.WriteFile(filepath.Join(data, "ready"), []byte("ready"), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(14)
	}
	<-signals
	if err := os.WriteFile(filepath.Join(data, "stopped"), []byte("graceful"), 0600); err != nil {
		os.Exit(15)
	}
	os.Exit(0)
}

func TestRealServiceBoundaryAndGracefulStop(t *testing.T) {
	if os.Getenv("COMFYWARE_SANDBOX_TEST") != "1" {
		t.Skip("set COMFYWARE_SANDBOX_TEST=1 to require real Linux namespace tests")
	}
	root := t.TempDir()
	data := filepath.Join(root, "data with spaces")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "unrelated-secret")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer host.Close()
	private := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer private.Close()
	bundle := filepath.Join(root, "private ca.pem")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: private.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args, env, err := (Service{Prefix: "TEST_", DataDir: data, Executable: executable, Env: []string{
		"TEST_SANDBOX_HELPER=1", "TEST_FORBIDDEN=" + secret, "TEST_HTTP=" + host.URL, "TEST_HTTPS=" + private.URL, "UNRELATED_PASSWORD=hidden", "SSL_CERT_FILE=" + bundle,
	}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, "bwrap", append(args, "--", "/app/server", "-test.run=^TestServiceHelper$"), env)
	}()
	deadline := time.After(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(data, "ready")); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("sandbox service exited before ready: %v", err)
		case <-deadline:
			t.Fatal("sandbox service did not start")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sandbox did not forward SIGTERM")
	}
	if got, err := os.ReadFile(filepath.Join(data, "stopped")); err != nil || string(got) != "graceful" {
		t.Fatalf("sandbox prevented graceful shutdown: %s %v", got, err)
	}
}
