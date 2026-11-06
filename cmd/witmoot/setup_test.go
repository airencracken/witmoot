// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"witmoot/internal/forum"
)

func TestAccountDataDirectoryOverride(t *testing.T) {
	unused := filepath.Join(t.TempDir(), "unused")
	t.Setenv("WITMOOT_DATA_DIR", unused)
	selected := filepath.Join(t.TempDir(), "selected")
	args := []string{"--username", "alex", "--data-dir", selected, "--password-stdin"}
	if err := runCommand(append([]string{"create-owner"}, args...), strings.NewReader("a private initial password"), io.Discard); err != nil {
		t.Fatal(err)
	}
	secret := "a different private password"
	if err := runCommand(append([]string{"set-password"}, args...), strings.NewReader(secret), io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unused); !os.IsNotExist(err) {
		t.Fatal("override touched default directory", err)
	}
	st, err := forum.OpenCurrentStore(filepath.Join(selected, "witmoot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, st)
	_, hash, err := st.Credentials(t.Context(), "alex")
	if err != nil {
		t.Fatal(err)
	}
	if err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)); err != nil {
		t.Fatal(err)
	}
}
