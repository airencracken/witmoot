package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"witmoot/internal/forum"
	"witmoot/internal/instance"
)

func migrateDatabase(args []string, out io.Writer) error {
	flags := commandFlags("migrate", out)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: witmoot migrate")
	}
	directory, err := servicePaths().DataDir("WITMOOT_DATA_DIR")
	if err != nil {
		return err
	}
	path := filepath.Join(directory, "witmoot.db")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("open existing database: %w", err)
	}
	lock, err := instance.Acquire(path, true)
	if err != nil {
		return err
	}
	defer lock.Close()
	store, err := forum.OpenStore(path)
	if err != nil {
		return err
	}
	defer closeStore(store)
	_, err = fmt.Fprintln(out, "Database is current. Pre-migration snapshots, when needed, are beside witmoot.db.")
	return err
}
