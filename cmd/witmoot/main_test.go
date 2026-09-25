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

func TestMailConfigValidation(t *testing.T) {
	t.Setenv("WITMOOT_SMTP_HOST", "")
	if cfg, err := mailConfig(); err != nil || cfg.Host != "" {
		t.Fatalf("mail should be disabled without a host: %+v %v", cfg, err)
	}
	t.Setenv("WITMOOT_SMTP_HOST", "relay.example.org")
	t.Setenv("WITMOOT_SMTP_PORT", "not-a-port")
	if _, err := mailConfig(); err == nil || !strings.Contains(err.Error(), "WITMOOT_SMTP_PORT") {
		t.Fatalf("invalid port: %v", err)
	}
	t.Setenv("WITMOOT_SMTP_PORT", "587")
	t.Setenv("WITMOOT_SMTP_TLS", "bogus")
	if _, err := mailConfig(); err == nil || !strings.Contains(err.Error(), "WITMOOT_SMTP_TLS") {
		t.Fatalf("invalid TLS mode: %v", err)
	}
	t.Setenv("WITMOOT_SMTP_TLS", "implicit")
	cfg, err := mailConfig()
	if err != nil || cfg.Host != "relay.example.org" || cfg.Port != 587 || string(cfg.Mode) != "implicit" || cfg.From == "" {
		t.Fatalf("mail config: %+v %v", cfg, err)
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
