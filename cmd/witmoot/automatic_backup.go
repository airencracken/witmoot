// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/airencracken/comfylib/autobackup"
	"witmoot/internal/forum"
)

func automaticBackups(store *forum.Store, dataDir string) (*autobackup.Runner, error) {
	cfg, err := autobackup.Load("witmoot", dataDir, os.Getenv)
	if err != nil {
		return nil, err
	}
	return autobackup.New(cfg, func(ctx context.Context, dest string, limit int64) error {
		return store.AutomaticSnapshot(ctx, dataDir, dest, limit)
	}, slog.Default())
}
