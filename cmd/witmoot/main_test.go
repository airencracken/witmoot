package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpDoesNotCreateDatabase(t *testing.T) {
	args := os.Args
	os.Args = []string{"witmoot", "--help"}
	t.Cleanup(func() { os.Args = args })
	data := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("WITMOOT_DATA_DIR", data)
	if err := run(); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("help touched the data directory: %v", err)
	}
}

func TestStartupRejectsInvalidDeploymentConfiguration(t *testing.T) {
	args := os.Args
	os.Args = []string{"witmoot"}
	t.Cleanup(func() { os.Args = args })
	for _, name := range []string{"WITMOOT_TRUSTED_PROXIES", "WITMOOT_BASE_URL"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("WITMOOT_DATA_DIR", t.TempDir())
			t.Setenv("WITMOOT_SECURE_COOKIES", "false")
			t.Setenv("WITMOOT_IMVAULT_URL", "")
			t.Setenv("WITMOOT_TRUSTED_PROXIES", "")
			t.Setenv("WITMOOT_BASE_URL", "")
			t.Setenv("WITMOOT_ADDR", "invalid-listen-address")
			t.Setenv(name, "not-an-address")
			if err := run(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("invalid %s did not stop startup: %v", name, err)
			}
		})
	}
}
