// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"witmoot/internal/forum"
	"witmoot/internal/instance"

	"github.com/airencracken/comfylib/privdrop"
	"github.com/airencracken/comfylib/setup"
	"github.com/airencracken/comfylib/svcconfig"
)

func firstSetup(store *forum.Store, data, base, addr string) (*setup.Handler, error) {
	return setup.New(setup.Config{App: "witmoot", Directory: data, BaseURL: base, Address: addr, Ready: store.HasOwner, Create: store.CreateFirstOwner})
}
func setupCommand(args []string, out io.Writer, paths svcconfig.Paths) error {
	flags := commandFlags("setup", out)
	data := flags.String("data-dir", "", "private data directory (defaults to the installed service)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if err := privdrop.RefuseRoot(os.Geteuid(), "setup", "sudo -u witmoot witmoot setup"); err != nil {
		return err
	}
	var err error
	if *data == "" {
		*data, err = paths.DataDir("WITMOOT_DATA_DIR")
	} else {
		*data, err = filepath.Abs(*data)
	}
	if err != nil {
		return err
	}
	if err = os.MkdirAll(*data, 0700); err != nil {
		return err
	}
	path := filepath.Join(*data, "witmoot.db")
	open := forum.OpenCurrentStore
	if _, err = os.Stat(path); errors.Is(err, os.ErrNotExist) {
		open = forum.OpenStore
	}
	lock, err := instance.Acquire(path, false)
	if err != nil {
		return err
	}
	defer lock.Close()
	store, err := open(path)
	if err != nil {
		return err
	}
	defer closeStore(store)
	settings, err := paths.Settings("WITMOOT_BASE_URL", "WITMOOT_ADDR")
	if err != nil {
		return err
	}
	addr := settings["WITMOOT_ADDR"]
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	wizard, err := firstSetup(store, *data, settings["WITMOOT_BASE_URL"], addr)
	if err != nil {
		return err
	}
	return wizard.Enable(context.Background(), out)
}

func accountDataDirectory(paths svcconfig.Paths, selected string) (string, error) {
	if selected != "" {
		return filepath.Abs(selected)
	}
	return paths.DataDir("WITMOOT_DATA_DIR")
}
