package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"witmoot/internal/forum"
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
	server := &http.Server{Addr: env("WITMOOT_ADDR", "127.0.0.1:8080"), Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
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

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
