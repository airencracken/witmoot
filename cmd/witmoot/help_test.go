package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"witmoot/internal/forum"
)

func TestCLIHelpAndProxyConfigDoNotTouchData(t *testing.T) {
	data := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("WITMOOT_DATA_DIR", data)
	t.Setenv("WITMOOT_SECURE_COOKIES", "invalid-on-purpose")
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"}, {"serve", "--help"}, {"help", "serve"},
		{"create-owner", "--help"}, {"help", "create-owner"},
		{"proxy-config", "--help"}, {"help", "proxy-config"},
		{"proxy-config", "nginx", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			if err := runCommand(args, strings.NewReader(""), &output); err != nil || !strings.Contains(output.String(), "Usage: witmoot") {
				t.Fatalf("help: %v\n%s", err, &output)
			}
		})
	}
	for _, server := range []string{"caddy", "nginx", "apache"} {
		var output bytes.Buffer
		if err := runCommand([]string{"proxy-config", server, "--domain", "img.internetrelay.chat", "--upstream", "127.0.0.1:9100"}, strings.NewReader(""), &output); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "img.internetrelay.chat") || !strings.Contains(output.String(), "127.0.0.1:9100") {
			t.Fatalf("wrong proxy config: %s", &output)
		}
	}
	for _, args := range [][]string{{"wat"}, {"help", "wat"}, {"serve", "extra"}, {"--help", "extra"}, {"create-owner"}, {"create-owner", "--username", "alex", "--password-prompt", "--password-stdin"}} {
		if err := runCommand(args, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Fatalf("invalid arguments succeeded: %v", args)
		}
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("help/config generation touched data: %v", err)
	}
}

func TestCreateOwnerPromptRequiresTerminalBeforeOpeningData(t *testing.T) {
	data := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("WITMOOT_DATA_DIR", data)
	oldStdin := os.Stdin
	stdin, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = oldStdin
		stdin.Close()
		writer.Close()
	})
	err = runCommand([]string{"create-owner", "--username", "alex", "--password-prompt"}, strings.NewReader("unused"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "use --password-stdin") {
		t.Fatalf("non-terminal prompt error = %v", err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("non-terminal prompt touched data: %v", err)
	}
}

func TestCreateOwnerCommand(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("WITMOOT_DATA_DIR", data)
	args := []string{"create-owner", "--username", "alex", "--password-stdin"}
	password := "a long test password"
	var output bytes.Buffer
	if err := runCommand(args, strings.NewReader(password+"\r\n"), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Owner created") {
		t.Fatalf("missing confirmation: %s", &output)
	}
	if err := runCommand(args, strings.NewReader("different password"), &output); err == nil {
		t.Fatal("existing account was accepted")
	}
	store, err := forum.OpenStore(filepath.Join(data, "witmoot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, hash, err := store.Credentials(context.Background(), "alex")
	if err != nil || user.Role != "owner" {
		t.Fatalf("created account: %+v, %v", user, err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		t.Fatalf("owner password was changed or line ending was included: %v", err)
	}
}

func TestCreateOwnerRejectsInvalidInputBeforeOpeningData(t *testing.T) {
	data := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("WITMOOT_DATA_DIR", data)
	for _, input := range []io.Reader{strings.NewReader("short"), strings.NewReader(strings.Repeat("x", 1025)), failedReader{}} {
		if err := runCommand([]string{"create-owner", "--username", "alex", "--password-stdin"}, input, &bytes.Buffer{}); err == nil {
			t.Fatal("invalid password input succeeded")
		}
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("invalid input touched data: %v", err)
	}
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errors.New("stdin failed") }

func TestHelpExplainsDeployment(t *testing.T) {
	var output bytes.Buffer
	if err := runCommand([]string{"--help"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"WITMOOT_ADDR", "WITMOOT_DATA_DIR", "WITMOOT_BASE_URL", "WITMOOT_SECURE_COOKIES", "/etc/conf.d/witmoot", "logrotate", "journald", "proxy-config", "password-prompt", "password-stdin"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("help omits %s", want)
		}
	}
}
