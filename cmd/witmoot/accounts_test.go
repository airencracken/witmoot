package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/airencracken/comfylib/token"
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
	if err := store.CreateAuthToken(ctx, user.ID, forum.TokenPasswordReset, "reset-hash", time.Now().Add(time.Hour)); err != nil {
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
	defer closeTest(t, store)
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
	if _, err := store.AuthTokenValid(ctx, "reset-hash", forum.TokenPasswordReset, time.Now()); err == nil {
		t.Fatal("reset link survived a forced password change")
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
	secret := match[1]

	store, err := forum.OpenStore(filepath.Join(data, "witmoot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTest(t, store)
	ctx := context.Background()
	if _, err := store.AuthTokenValid(ctx, token.Hash(secret), forum.TokenPasswordReset, time.Now()); err != nil {
		t.Fatalf("issued link is not usable: %v", err)
	}
	// The stored key is the digest, not the token a person was shown.
	if _, err := store.AuthTokenValid(ctx, secret, forum.TokenPasswordReset, time.Now()); err == nil {
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
	owner, _, err := store.Credentials(context.Background(), "alex")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ChangeMember(context.Background(), owner.ID, jules.ID, 0, "suspend", "jules"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runCommand([]string{"list-users"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ID", "USERNAME", "ROLE", "EMAIL", "STATUS", "alex", "owner", "jules", "member", "jules@example.org", "suspended", "active"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("list-users omits %q:\n%s", want, &output)
		}
	}
}

func TestResetLinkReadsTheBaseURLFromTheServiceConfiguration(t *testing.T) {
	t.Setenv("WITMOOT_DATA_DIR", "")
	t.Setenv("WITMOOT_BASE_URL", "")
	data := filepath.Join(t.TempDir(), "service data")
	provisionOwner(t, data, "alex", "a long test password")
	config := writeTestFile(t, filepath.Join(t.TempDir(), "witmoot.confd"), "WITMOOT_DATA_DIR=\""+data+"\"\nWITMOOT_BASE_URL=https://board.example.org/\n")
	paths := testServicePaths()
	paths.OpenRCConfig, paths.OpenRCInstalled, paths.OpenRCActive = config, true, true
	var output bytes.Buffer
	if err := resetLinkWithConfigPaths([]string{"--username", "alex"}, &output, paths); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`\n  https://board\.example\.org/reset/[a-f0-9]{64}\n`).MatchString(output.String()) || strings.Contains(output.String(), "Set WITMOOT_BASE_URL") {
		t.Fatalf("reset link ignored the service's base URL: %s", &output)
	}
	writeTestFile(t, config, "WITMOOT_DATA_DIR=\""+data+"\"\nWITMOOT_BASE_URL=https://board.example.org/path?x=1\n")
	if err := resetLinkWithConfigPaths([]string{"--username", "alex"}, &bytes.Buffer{}, paths); err == nil || !strings.Contains(err.Error(), "WITMOOT_BASE_URL") {
		t.Fatalf("invalid base URL accepted: %v", err)
	}
}

// closeTest closes a test resource and reports a failure.
func closeTest(t testing.TB, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Error(err)
	}
}
