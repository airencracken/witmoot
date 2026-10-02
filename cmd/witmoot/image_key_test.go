// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// An image key written by earlier releases, raw 32 bytes, loads as it is and
// is never rewritten.
func TestImageKeyFixtureLoadsUnchanged(t *testing.T) {
	fixture, err := os.ReadFile("../../internal/forum/testdata/imvault.key")
	if err != nil || len(fixture) != 32 {
		t.Fatalf("fixture: %d bytes, %v", len(fixture), err)
	}
	data := t.TempDir()
	path := filepath.Join(data, "imvault.key")
	if err := os.WriteFile(path, fixture, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		key, err := loadImageKey(data)
		if err != nil || !bytes.Equal(key, fixture) {
			t.Fatalf("loaded %x, %v; want the fixture", key, err)
		}
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("the key file was replaced: %v", err)
	}
	if contents, err := os.ReadFile(path); err != nil || !bytes.Equal(contents, fixture) {
		t.Fatalf("the key file changed: %x %v", contents, err)
	}
}

func TestImageKeyIsCreatedOnceAsRawBytes(t *testing.T) {
	data := t.TempDir()
	var wg sync.WaitGroup
	keys := make([][]byte, 8)
	errs := make([]error, 8)
	for i := range keys {
		wg.Go(func() { keys[i], errs[i] = loadImageKey(data) })
	}
	wg.Wait()
	for i := range keys {
		if errs[i] != nil || !bytes.Equal(keys[i], keys[0]) || len(keys[i]) != 32 {
			t.Fatalf("concurrent load %d = %x, %v; first %x", i, keys[i], errs[i], keys[0])
		}
	}
	contents, err := os.ReadFile(filepath.Join(data, "imvault.key"))
	if err != nil || !bytes.Equal(contents, keys[0]) {
		t.Fatalf("the file does not hold the raw key: %x %v", contents, err)
	}
	info, err := os.Stat(filepath.Join(data, "imvault.key"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("key permissions: %v %v", info.Mode(), err)
	}
	entries, err := os.ReadDir(data)
	if err != nil || len(entries) != 1 {
		t.Fatalf("creation left other files behind: %v %v", entries, err)
	}
}

// A damaged key is reported with what to do about it and left exactly as it
// was found: replacing it would disconnect every member's images.
func TestDamagedImageKeyIsNeverReplaced(t *testing.T) {
	for _, contents := range [][]byte{nil, []byte("broken"), bytes.Repeat([]byte{7}, 31), bytes.Repeat([]byte{7}, 33), []byte(strings.Repeat("ab", 32) + "\n")} {
		data := t.TempDir()
		path := filepath.Join(data, "imvault.key")
		if err := os.WriteFile(path, contents, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadImageKey(data); err == nil || !strings.Contains(err.Error(), "restore it from backup") || !strings.Contains(err.Error(), path) {
			t.Fatalf("%d-byte key: %v", len(contents), err)
		}
		if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, contents) {
			t.Fatalf("damaged key was changed to %x: %v", after, err)
		}
	}
}
