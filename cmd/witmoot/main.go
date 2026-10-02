package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	netmail "net/mail"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"witmoot/internal/forum"
	"witmoot/internal/mail"
)

func main() {
	if handled, status, err := reexecProvisioningAsService(os.Args[1:]); handled {
		if err != nil {
			slog.Error("witmoot stopped", "error", err)
		}
		os.Exit(status)
	}
	if err := run(); err != nil {
		slog.Error("witmoot stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	return runCommand(os.Args[1:], os.Stdin, os.Stdout)
}

func runServer() error {
	dataDir := env("WITMOOT_DATA_DIR", "./data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	store, err := forum.OpenStore(filepath.Join(dataDir, "witmoot.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	secure := env("WITMOOT_SECURE_COOKIES", "false")
	if secure != "true" && secure != "false" {
		return errors.New("WITMOOT_SECURE_COOKIES must be true or false")
	}
	config := forum.Config{Name: env("WITMOOT_NAME", "Witmoot"), SourceURL: env("WITMOOT_SOURCE_URL", "https://github.com/airencracken/witmoot"), BaseURL: os.Getenv("WITMOOT_BASE_URL"), SecureCookies: secure == "true", ImvaultURL: os.Getenv("WITMOOT_IMVAULT_URL")}
	config.TrustedProxies, err = forum.ParseTrustedProxies(os.Getenv("WITMOOT_TRUSTED_PROXIES"))
	if err != nil {
		return err
	}
	config.Mail, err = mailConfig(os.Getenv)
	if err != nil {
		return err
	}
	if config.ImvaultURL != "" {
		config.ImageKey, err = forum.LoadImageKey(filepath.Join(dataDir, "imvault.key"))
		if err != nil {
			return err
		}
	}
	app, err := forum.New(store, config)
	if err != nil {
		return err
	}
	server := newHTTPServer(env("WITMOOT_ADDR", "127.0.0.1:8080"), app)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { slog.Info("Witmoot is ready", "address", server.Addr); done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

// newHTTPServer leaves body read and response write deadlines to the
// application, which sets them per request: uploads may take as long as the
// reverse proxies allow, while ordinary forms stay tightly bounded.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// mailConfig reads the optional SMTP relay through lookup, which returns one
// WITMOOT_* setting or "". An empty WITMOOT_SMTP_HOST leaves mail disabled;
// everything else is only validated when a relay is configured.
func mailConfig(lookup func(string) string) (mail.Config, error) {
	host := lookup("WITMOOT_SMTP_HOST")
	if host == "" {
		return mail.Config{}, nil
	}
	port, err := strconv.Atoi(cmp.Or(lookup("WITMOOT_SMTP_PORT"), "587"))
	if err != nil || port < 1 || port > 65535 {
		return mail.Config{}, errors.New("WITMOOT_SMTP_PORT must be a whole number from 1 to 65535")
	}
	mode := mail.TLSMode(cmp.Or(lookup("WITMOOT_SMTP_TLS"), string(mail.TLSStartTLS)))
	switch mode {
	case mail.TLSStartTLS, mail.TLSImplicit, mail.TLSNone:
	default:
		return mail.Config{}, errors.New("WITMOOT_SMTP_TLS must be starttls, implicit, or none")
	}
	from, err := mailSender(lookup)
	if err != nil {
		return mail.Config{}, err
	}
	return mail.Config{
		Host:     host,
		Port:     port,
		Username: lookup("WITMOOT_SMTP_USERNAME"),
		Password: lookup("WITMOOT_SMTP_PASSWORD"),
		From:     from,
		Mode:     mode,
	}, nil
}

// mailSender returns the From address. WITMOOT_SMTP_FROM wins; otherwise the
// board sends as no-reply at the domain in WITMOOT_BASE_URL, so relays see a
// real domain rather than localhost. With neither, mail cannot be enabled.
func mailSender(lookup func(string) string) (string, error) {
	if from := lookup("WITMOOT_SMTP_FROM"); from != "" {
		canonical, err := mail.ParseFrom(from)
		if err != nil {
			return "", fmt.Errorf("WITMOOT_SMTP_FROM: %w", err)
		}
		return canonical, nil
	}
	host := ""
	if base, err := url.Parse(lookup("WITMOOT_BASE_URL")); err == nil {
		host = base.Hostname()
	}
	if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return "", errors.New("set WITMOOT_SMTP_FROM, or a WITMOOT_BASE_URL with a domain name, when WITMOOT_SMTP_HOST is set")
	}
	return (&netmail.Address{Name: cmp.Or(lookup("WITMOOT_NAME"), "Witmoot"), Address: "no-reply@" + host}).String(), nil
}
