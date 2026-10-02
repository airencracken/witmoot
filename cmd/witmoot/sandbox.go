// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"witmoot/internal/sandbox"
)

func runSandbox(args []string, out io.Writer) error {
	flags := commandFlags("sandbox", out)
	check := flags.Bool("check", false, "verify the sandbox without starting the server")
	bwrap := flags.String("bwrap", env("WITMOOT_BWRAP", "bwrap"), "Bubblewrap executable")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected sandbox arguments; use witmoot sandbox --help")
	}
	if os.Geteuid() == 0 {
		return errors.New("run the sandbox as the unprivileged witmoot service user")
	}
	data, err := filepath.Abs(env("WITMOOT_DATA_DIR", "./data"))
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	environment := os.Environ()
	policy := sandbox.Service{Prefix: "WITMOOT_", DataDir: data, Executable: executable, Env: environment}
	mounts, environment, err := policy.Policy()
	if err != nil {
		return err
	}
	binary, err := sandbox.Binary(*bwrap)
	if err != nil {
		return fmt.Errorf("Bubblewrap is required for sandbox mode: %w", err)
	}
	// Running the bound server proves the namespaces, mounts and loader work
	// without depending on any particular host utility.
	if err := sandbox.Check(context.Background(), binary, mounts, environment, "/app/server", "--help"); err != nil {
		return err
	}
	if *check {
		_, err := fmt.Fprintln(out, "Bubblewrap sandbox is ready.")
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return sandbox.Run(ctx, binary, append(mounts, "--", "/app/server"), environment)
}
