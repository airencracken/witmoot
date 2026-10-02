package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if cfg, err := mailConfig(os.Getenv); err != nil || cfg.Host != "" {
		t.Fatalf("mail should be disabled without a host: %+v %v", cfg, err)
	}
	t.Setenv("WITMOOT_SMTP_HOST", "relay.example.org")
	t.Setenv("WITMOOT_SMTP_FROM", "")
	t.Setenv("WITMOOT_BASE_URL", "https://board.example.org")
	t.Setenv("WITMOOT_NAME", "")
	t.Setenv("WITMOOT_SMTP_PORT", "not-a-port")
	if _, err := mailConfig(os.Getenv); err == nil || !strings.Contains(err.Error(), "WITMOOT_SMTP_PORT") {
		t.Fatalf("invalid port: %v", err)
	}
	t.Setenv("WITMOOT_SMTP_PORT", "587")
	t.Setenv("WITMOOT_SMTP_TLS", "bogus")
	if _, err := mailConfig(os.Getenv); err == nil || !strings.Contains(err.Error(), "WITMOOT_SMTP_TLS") {
		t.Fatalf("invalid TLS mode: %v", err)
	}
	t.Setenv("WITMOOT_SMTP_TLS", "implicit")
	cfg, err := mailConfig(os.Getenv)
	if err != nil || cfg.Host != "relay.example.org" || cfg.Port != 587 || string(cfg.Mode) != "implicit" || cfg.From != `"Witmoot" <no-reply@board.example.org>` {
		t.Fatalf("mail config: %+v %v", cfg, err)
	}
}

func TestMailSenderIsExplicitOrDerivedFromTheBaseURL(t *testing.T) {
	settings := map[string]string{"WITMOOT_SMTP_HOST": "relay.example.org"}
	lookup := func(key string) string { return settings[key] }
	for _, base := range []string{"", "http://localhost:8080", "https://192.0.2.10", "https://[2001:db8::1]", "not a url"} {
		settings["WITMOOT_BASE_URL"] = base
		if cfg, err := mailConfig(lookup); err == nil || !strings.Contains(err.Error(), "WITMOOT_SMTP_FROM") {
			t.Errorf("base URL %q gave sender %q (%v); want WITMOOT_SMTP_FROM required", base, cfg.From, err)
		}
	}
	settings["WITMOOT_BASE_URL"] = "https://board.example.org"
	settings["WITMOOT_NAME"] = "Café Crew"
	if cfg, err := mailConfig(lookup); err != nil || cfg.From != "=?utf-8?q?Caf=C3=A9_Crew?= <no-reply@board.example.org>" {
		t.Fatalf("derived sender = %q, %v", cfg.From, err)
	}
	settings["WITMOOT_SMTP_FROM"] = "Board Mail <mail@example.net>"
	if cfg, err := mailConfig(lookup); err != nil || cfg.From != `"Board Mail" <mail@example.net>` {
		t.Fatalf("explicit sender = %q, %v", cfg.From, err)
	}
	for _, from := range []string{"no-reply@localhost\r\nBcc: victim@example.org", "not an address", "Board <>", "a@b@c"} {
		settings["WITMOOT_SMTP_FROM"] = from
		if cfg, err := mailConfig(lookup); err == nil {
			t.Errorf("accepted sender %q as %q", from, cfg.From)
		}
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

// Uploads may take as long as the reverse proxies allow, so the server must not
// impose a shorter whole-request deadline; the application sets one per route.
func TestServerLeavesBodyDeadlinesToTheApplication(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if server.ReadTimeout != 0 || server.WriteTimeout != 0 {
		t.Fatalf("server-wide deadlines cut off uploads: read %s write %s", server.ReadTimeout, server.WriteTimeout)
	}
	if server.ReadHeaderTimeout <= 0 || server.ReadHeaderTimeout > 10*time.Second || server.IdleTimeout <= 0 || server.MaxHeaderBytes <= 0 {
		t.Fatalf("slow header and idle limits are missing: %+v", server)
	}
}
