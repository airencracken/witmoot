// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"witmoot/internal/forum"
)

func main() {
	port := flag.Int("port", 8082, "local demo port (0 chooses a free port)")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: demo [-port PORT]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *port, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}

// run deliberately ignores WITMOOT_* settings and owns only its fresh temp dir.
func run(ctx context.Context, port int, out io.Writer) (err error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("listen: %w; try make demo PORT=9000", err)
	}
	defer listener.Close()
	dataDir, err := os.MkdirTemp("", "witmoot-demo-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dataDir)) }()
	store, err := forum.OpenStore(filepath.Join(dataDir, "witmoot.db"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	if err := seed(ctx, store); err != nil {
		return fmt.Errorf("seed demo: %w", err)
	}
	base := "http://" + listener.Addr().String()
	app, err := forum.New(store, forum.Config{Name: "The little round table", BaseURL: base})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "\nOpen %s\nOwner: demo\nMembers: freya, jules\nPassword for all three: %s\n\nBrowse public conversations or sign in to post and explore the different audiences.\nThe owner can change site access under Settings and permissions under Manage boards.\nIn The planning nook: freya can post; jules has read-only access.\nDemo data: %s\nPress Ctrl-C to stop and remove this demo. Each run starts fresh.\n\n", base, demoPassword, dataDir); err != nil {
		return err
	}
	return serve(ctx, listener, app)
}

func serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := server.Shutdown(shutdown)
		if err != nil {
			server.Close()
		}
		<-done
		return err
	}
}
