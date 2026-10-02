// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/airencracken/comfylib/svcconfig"
)

// testServicePaths describes no installed service, with Witmoot's names and
// defaults; tests fill in the configuration files they need.
func testServicePaths() svcconfig.Paths {
	detected := servicePaths()
	return svcconfig.Paths{Name: detected.Name, Prefix: detected.Prefix, DefaultDataDir: detected.DefaultDataDir}
}

func writeTestFile(t *testing.T, path, contents string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The account commands look for the service the packages install, under the
// names its documentation gives.
func TestServicePathsFindTheWitmootService(t *testing.T) {
	paths := servicePaths()
	if paths.Name != "Witmoot" || paths.Prefix != "WITMOOT_" || paths.DefaultDataDir != "/var/lib/witmoot" || paths.OpenRCConfig != "/etc/conf.d/witmoot" {
		t.Fatalf("service paths = %+v", paths)
	}
	if paths.SystemdUnit != "" && filepath.Base(paths.SystemdUnit) != "witmoot.service" {
		t.Fatalf("found another service's unit: %s", paths.SystemdUnit)
	}
}

func TestProvisioningRequestCoversTheDatabaseCommands(t *testing.T) {
	paths := testServicePaths()
	args := []string{"create-owner", "--username", "alex"}
	request := provisioningRequest(args, paths)
	want := []string{"admin", "create-owner", "list-users", "reset-link", "set-password"}
	if got := slices.Sorted(maps.Keys(request.Commands)); !slices.Equal(got, want) {
		t.Fatalf("re-run commands = %q, want %q", got, want)
	}
	for _, command := range []string{"serve", "sandbox", "proxy-config", "help"} {
		if request.Commands[command] {
			t.Errorf("%s would be re-run as the service user", command)
		}
	}
	if !slices.Equal(request.Args, args) || request.DefaultUser != "witmoot" || request.Paths.Prefix != "WITMOOT_" || request.Settings == nil {
		t.Fatalf("request = %+v", request)
	}
}

func TestServiceAccountDefaultsToWitmoot(t *testing.T) {
	openRC := writeTestFile(t, filepath.Join(t.TempDir(), "witmoot.confd"), "WITMOOT_USER=board-user\nWITMOOT_GROUP=board-group\n")
	paths := testServicePaths()
	paths.OpenRCConfig, paths.OpenRCInstalled, paths.OpenRCActive = openRC, true, true
	user, group, managed, err := paths.Account(serviceUser)
	if err != nil || !managed || user != "board-user" || group != "board-group" {
		t.Fatalf("service account = %q:%q managed=%t err=%v", user, group, managed, err)
	}
	paths = testServicePaths()
	paths.OpenRCInstalled = true
	user, group, managed, err = paths.Account(serviceUser)
	if err != nil || !managed || user != "witmoot" || group != "witmoot" {
		t.Fatalf("default service account = %q:%q managed=%t err=%v", user, group, managed, err)
	}
	user, group, managed, err = testServicePaths().Account(serviceUser)
	if err != nil || managed || user != "" || group != "" {
		t.Fatalf("portable install identity = %q:%q managed=%t err=%v", user, group, managed, err)
	}
}

func TestProvisioningDataDirectory(t *testing.T) {
	t.Setenv("WITMOOT_DATA_DIR", "")
	t.Run("portable default", func(t *testing.T) {
		work := t.TempDir()
		t.Chdir(work)
		got, err := testServicePaths().DataDir("WITMOOT_DATA_DIR")
		if want := filepath.Join(work, "data"); err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("service default", func(t *testing.T) {
		paths := testServicePaths()
		paths.OpenRCConfig = writeTestFile(t, filepath.Join(t.TempDir(), "witmoot.confd"), "WITMOOT_USER=witmoot\n")
		paths.OpenRCInstalled = true
		if got, err := paths.DataDir("WITMOOT_DATA_DIR"); err != nil || got != "/var/lib/witmoot" {
			t.Fatalf("resolve = %q, %v; want /var/lib/witmoot", got, err)
		}
	})
	t.Run("systemd environment file overrides unit", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "systemd data")
		envFile := writeTestFile(t, filepath.Join(t.TempDir(), "witmoot.env"), "WITMOOT_DATA_DIR='"+want+"'\n")
		paths := testServicePaths()
		paths.SystemdUnit = writeTestFile(t, filepath.Join(t.TempDir(), "witmoot.service"), "[Service]\nEnvironment=WITMOOT_DATA_DIR=/var/lib/witmoot\nEnvironmentFile=-"+envFile+"\n")
		paths.SystemdActive = true
		if got, err := paths.DataDir("WITMOOT_DATA_DIR"); err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("explicit environment wins", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "explicit")
		t.Setenv("WITMOOT_DATA_DIR", want)
		if got, err := testServicePaths().DataDir("WITMOOT_DATA_DIR"); err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
}

// A command re-run as the service user receives the site and mail settings it
// needs from the root-only configuration, and only the commands that make
// links or send mail receive them.
func TestProvisioningChildSettingsFollowTheServiceConfiguration(t *testing.T) {
	for _, key := range instanceSettings {
		t.Setenv(key, "")
	}
	envFile := writeTestFile(t, filepath.Join(t.TempDir(), "with space", "witmoot.env"), "WITMOOT_BASE_URL=https://board.example.org\nWITMOOT_SMTP_HOST=relay.example.org\nWITMOOT_SMTP_FROM='Board <mail@example.org>'\nWITMOOT_SMTP_PASSWORD=relay-secret\n")
	paths := testServicePaths()
	paths.SystemdUnit = writeTestFile(t, filepath.Join(t.TempDir(), "witmoot.service"), "[Service]\nEnvironment=\"WITMOOT_NAME=Our Board\"\nEnvironmentFile="+envFile+"\n")
	paths.SystemdActive = true
	settings := provisioningChildSettings(paths)
	want := map[string]string{"WITMOOT_BASE_URL": "https://board.example.org", "WITMOOT_SMTP_HOST": "relay.example.org", "WITMOOT_SMTP_FROM": "Board <mail@example.org>", "WITMOOT_SMTP_PASSWORD": "relay-secret", "WITMOOT_NAME": "Our Board"}
	for _, command := range []string{"reset-link", "admin"} {
		got, err := settings(command, "/var/lib/witmoot")
		if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s settings = %v, %v; want %v", command, got, err, want)
		}
	}
	for _, command := range []string{"create-owner", "set-password", "list-users"} {
		if got, err := settings(command, "/var/lib/witmoot"); err != nil || len(got) != 0 {
			t.Fatalf("%s received settings it does not need: %v %v", command, got, err)
		}
	}
	if got, err := provisioningChildSettings(testServicePaths())("admin", "./data"); err != nil || len(got) != 0 {
		t.Fatalf("portable install read service settings: %v %v", got, err)
	}
	t.Setenv("WITMOOT_BASE_URL", "https://override.example.org")
	if got, err := settings("reset-link", "/var/lib/witmoot"); err != nil || got["WITMOOT_BASE_URL"] != "https://override.example.org" {
		t.Fatalf("environment did not win: %v %v", got, err)
	}
	options, err := adminOptions(paths)
	if err != nil || options.BaseURL != "https://override.example.org" || options.SiteName != "Our Board" || !options.Mailer.Enabled() {
		t.Fatalf("admin options = %+v, %v", options, err)
	}
	// A configuration the commands cannot follow is an error, not a guess.
	paths.SystemdUnit = writeTestFile(t, filepath.Join(t.TempDir(), "witmoot.service"), "[Service]\nEnvironment=\"WITMOOT_NAME=unterminated\n")
	if _, err := provisioningChildSettings(paths)("admin", "/var/lib/witmoot"); err == nil || !strings.Contains(err.Error(), "WITMOOT_NAME") {
		t.Fatalf("unparsable configuration accepted: %v", err)
	}
}
