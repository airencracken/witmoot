package main

import (
	"os"
	"strings"
	"testing"
)

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
