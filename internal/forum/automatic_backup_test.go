// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"bytes"
	"context"
	"fmt"
	"github.com/airencracken/comfylib/autobackup"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutomaticBackupPreservesConnectionsAndPrivateDatabase(t *testing.T) {
	s := testStore(t)
	user := testMember(t, s, "rory")
	topic, err := s.CreateTopic(t.Context(), 1, user, "Private dinner", "Only for members.", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	key := bytes.Repeat([]byte{37}, 32)
	cipher, err := imageCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, cipher.NonceSize())
	server := "https://images.example.org"
	sealed := cipher.Seal(nonce, nonce, []byte("private-api-token"), []byte(fmt.Sprintf("%d:%s", user, server)))
	if err := s.ConnectImages(t.Context(), user, Connection{Server: server, Username: "rory", Token: sealed}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "imvault.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "snapshot")
	if err := s.AutomaticSnapshot(t.Context(), data, dest, 10<<20); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "imvault.key"))
	if err != nil || !bytes.Equal(got, key) {
		t.Fatal("lost connection key", err)
	}
	restored, err := OpenCurrentStore(filepath.Join(dest, "witmoot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, restored)
	app, err := New(restored, Config{ImvaultURL: server, ImageKey: got})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := app.imageToken(t.Context(), user)
	if err != nil || plain != "private-api-token" {
		t.Fatal("restored connection cannot decrypt", plain, err)
	}
	var title string
	if err := restored.db.QueryRow("SELECT title FROM topics WHERE id=?", topic).Scan(&title); err != nil || title != "Private dinner" {
		t.Fatal(title, err)
	}
	var version int
	if err := restored.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != len(migrationFiles) {
		t.Fatal(version, err)
	}
	if err := s.AutomaticSnapshot(t.Context(), data, filepath.Join(t.TempDir(), "small"), 1); err == nil {
		t.Fatal("copy cap ignored")
	}
	if err := os.WriteFile(filepath.Join(data, "imvault.key"), bytes.Repeat([]byte{55}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AutomaticSnapshot(t.Context(), data, filepath.Join(t.TempDir(), "wrong-key"), 10<<20); err == nil {
		t.Fatal("incorrect encryption key accepted")
	}
	if err := os.WriteFile(filepath.Join(data, "imvault.key"), []byte("bad key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AutomaticSnapshot(t.Context(), data, filepath.Join(t.TempDir(), "bad"), 10<<20); err == nil {
		t.Fatal("corrupt key accepted")
	}
	if err := os.Remove(filepath.Join(data, "imvault.key")); err != nil {
		t.Fatal(err)
	}
	if err := s.AutomaticSnapshot(t.Context(), data, filepath.Join(t.TempDir(), "missing-key"), 10<<20); err == nil {
		t.Fatal("connections lost encryption key")
	}
	if err := s.DisconnectImages(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	if err := s.AutomaticSnapshot(t.Context(), data, filepath.Join(t.TempDir(), "without-key"), 10<<20); err != nil {
		t.Fatal("optional key required", err)
	}
}

func TestBackupStatusIsOwnerOnly(t *testing.T) {
	a, owner := newTestApp(t, false)
	signInTest(t, a, owner, true)
	cfg, err := autobackup.Load("witmoot", t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	a.config.Backups, err = autobackup.New(cfg, func(context.Context, string, int64) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := owner.request("GET", "/settings", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), cfg.Directory) || !strings.Contains(w.Body.String(), "No automatic backup has succeeded") {
		t.Fatal(w.Code, w.Body.String())
	}
	guest := &testClient{app: a, cookies: owner.cookies}
	delete(guest.cookies, a.cookieName("session"))
	for _, path := range []string{"/healthz", "/about"} {
		w := guest.request("GET", path, nil, nil)
		if strings.Contains(w.Body.String(), cfg.Directory) {
			t.Fatal("private backup status leaked", path)
		}
	}
	other, member := newTestApp(t, false)
	signInTest(t, other, member, false)
	other.config.Backups = a.config.Backups
	w = member.request("GET", "/settings", nil, nil)
	if w.Code != 403 || strings.Contains(w.Body.String(), cfg.Directory) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAutomaticBackupRejectsInvalidForeignKeys(t *testing.T) {
	s := testStore(t)
	user := testMember(t, s, "rory")
	if _, err := s.CreateTopic(t.Context(), 1, user, "Members only", "Private post", AudienceMembers); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DELETE FROM users WHERE id=?", user); err != nil {
		t.Fatal(err)
	}
	if err := s.AutomaticSnapshot(t.Context(), t.TempDir(), filepath.Join(t.TempDir(), "invalid"), 10<<20); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
}
