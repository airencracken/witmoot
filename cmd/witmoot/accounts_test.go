package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"witmoot/internal/forum"
)

// provisionOwner creates a board and an owner directly, the way a first run
// would, so the account commands have something to work on.
func provisionOwner(t *testing.T, data, username, password string) {
	t.Helper()
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := forum.OpenStore(filepath.Join(data, "witmoot.db"))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := forum.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOwner(context.Background(), username, hash); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSetPasswordCommandEndsSessions(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("WITMOOT_DATA_DIR", data)
	provisionOwner(t, data, "alex", "a long test password")

	store, err := forum.OpenStore(filepath.Join(data, "witmoot.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	user, _, err := store.Credentials(ctx, "alex")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NewSession(ctx, "session", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runCommand([]string{"set-password", "--username", "alex", "--password-stdin"}, strings.NewReader("a different long password"), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Password updated") {
		t.Fatalf("missing confirmation: %s", &output)
	}

	store, err = forum.OpenStore(filepath.Join(data, "witmoot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, hash, err := store.Credentials(ctx, "alex")
	if err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("a different long password")) != nil {
		t.Fatal("password was not replaced")
	}
	if session, err := store.Session(ctx, "session"); session != nil || err != nil {
		t.Fatalf("session survived a forced reset: %v %v", session, err)
	}
}

func TestResetLinkCommandStoresAHashedUsableToken(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("WITMOOT_DATA_DIR", data)
	t.Setenv("WITMOOT_BASE_URL", "https://board.example.org")
	provisionOwner(t, data, "alex", "a long test password")

	var output bytes.Buffer
	if err := runCommand([]string{"reset-link", "--username", "alex", "--expires", "1h"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`https://board\.example\.org/reset/([a-f0-9]{64})`).FindStringSubmatch(output.String())
	if len(match) != 2 {
		t.Fatalf("no complete reset link: %s", &output)
	}
	token := match[1]

	store, err := forum.OpenStore(filepath.Join(data, "witmoot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.AuthTokenValid(ctx, forum.TokenHash(token), forum.TokenPasswordReset, time.Now()); err != nil {
		t.Fatalf("issued link is not usable: %v", err)
	}
	// The stored key is the digest, not the token a person was shown.
	if _, err := store.AuthTokenValid(ctx, token, forum.TokenPasswordReset, time.Now()); err == nil {
		t.Fatal("the raw token should not be a stored key")
	}
}

func TestResetLinkWithoutBaseURLPrintsAPathAndHint(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("WITMOOT_DATA_DIR", data)
	t.Setenv("WITMOOT_BASE_URL", "")
	provisionOwner(t, data, "alex", "a long test password")

	var output bytes.Buffer
	if err := runCommand([]string{"reset-link", "--username", "alex"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "/reset/") || !strings.Contains(output.String(), "WITMOOT_BASE_URL") {
		t.Fatalf("missing path or hint: %s", &output)
	}
}

func TestAccountCommandsRefuseToCreateADatabase(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("WITMOOT_DATA_DIR", data)
	for _, args := range [][]string{
		{"set-password", "--username", "alex", "--password-stdin"},
		{"reset-link", "--username", "alex"},
		{"list-users"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if err := runCommand(args, strings.NewReader("a long enough password"), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no Witmoot database") {
				t.Fatalf("expected a missing-database error, got %v", err)
			}
		})
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("a command created the data directory: %v", err)
	}
}

func TestAccountCommandInputValidation(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("WITMOOT_DATA_DIR", data)
	provisionOwner(t, data, "alex", "a long test password")

	for _, tc := range []struct {
		name string
		args []string
		pass string
		want string
	}{
		{"unknown account", []string{"set-password", "--username", "nobody", "--password-stdin"}, "a long enough password", "no account named"},
		{"short password", []string{"set-password", "--username", "alex", "--password-stdin"}, "short", "at least 12 characters"},
		{"missing username", []string{"set-password", "--password-stdin"}, "a long enough password", "usage:"},
		{"both password sources", []string{"set-password", "--username", "alex", "--password-prompt", "--password-stdin"}, "x", "usage:"},
		{"unknown account link", []string{"reset-link", "--username", "nobody"}, "", "no account named"},
		{"bad expiry", []string{"reset-link", "--username", "alex", "--expires", "0s"}, "", "--expires"},
		{"extra argument", []string{"list-users", "extra"}, "", "usage:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runCommand(tc.args, strings.NewReader(tc.pass), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestListUsersCommand(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("WITMOOT_DATA_DIR", data)
	provisionOwner(t, data, "alex", "a long test password")

	store, err := forum.OpenStore(filepath.Join(data, "witmoot.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser(context.Background(), "jules", "hash"); err != nil {
		t.Fatal(err)
	}
	jules, _, err := store.Credentials(context.Background(), "jules")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetEmail(context.Background(), jules.ID, "jules@example.org"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runCommand([]string{"list-users"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ID", "USERNAME", "ROLE", "EMAIL", "alex", "owner", "jules", "member", "jules@example.org"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("list-users omits %q:\n%s", want, &output)
		}
	}
}
