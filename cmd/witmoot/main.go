package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"witmoot/internal/forum"
)

func main() {
	if err := run(); err != nil {
		slog.Error("witmoot stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	dataDir := env("WITMOOT_DATA_DIR", "./data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	store, err := forum.OpenStore(filepath.Join(dataDir, "witmoot.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	if len(os.Args) > 1 {
		if os.Args[1] != "create-owner" {
			return errors.New("usage: witmoot [create-owner --username NAME --password-stdin]")
		}
		flags := flag.NewFlagSet("create-owner", flag.ContinueOnError)
		username := flags.String("username", "", "owner username")
		stdin := flags.Bool("password-stdin", false, "read password from standard input")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		if !*stdin || flags.NArg() != 0 {
			return errors.New("use create-owner --username NAME --password-stdin")
		}
		password, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
		if err != nil {
			return err
		}
		value := strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r")
		if message := forum.ValidateCredentials(*username, value); message != "" {
			return errors.New(message)
		}
		hash, err := forum.HashPassword(value)
		if err != nil {
			return err
		}
		if err := store.CreateOwner(context.Background(), *username, hash); err != nil {
			return err
		}
		fmt.Println("Owner created. Start Witmoot, sign in, and invite your people.")
		return nil
	}
	secure := env("WITMOOT_SECURE_COOKIES", "false")
	if secure != "true" && secure != "false" {
		return errors.New("WITMOOT_SECURE_COOKIES must be true or false")
	}
	app, err := forum.New(store, forum.Config{Name: env("WITMOOT_NAME", "Witmoot"), SecureCookies: secure == "true"})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: env("WITMOOT_ADDR", "127.0.0.1:8080"), Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
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
