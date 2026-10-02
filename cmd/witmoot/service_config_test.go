package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProvisioningDataDir(t *testing.T) {
	t.Setenv("WITMOOT_DATA_DIR", "")
	t.Run("portable default", func(t *testing.T) {
		got, err := resolveProvisioningDataDir(provisioningConfigPaths{serviceDefault: "/var/lib/witmoot"})
		if err != nil || got != "./data" {
			t.Fatalf("resolve = %q, %v; want ./data", got, err)
		}
	})
	t.Run("OpenRC conf.d literal", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "service data")
		config := filepath.Join(t.TempDir(), "witmoot.confd")
		if err := os.WriteFile(config, []byte("WITMOOT_DATA_DIR=\""+want+"\" # comment\n"), 0600); err != nil {
			t.Fatal(err)
		}
		paths := provisioningConfigPaths{openRCConfig: config, openRCInstalled: true, openRCActive: true, serviceDefault: "/var/lib/witmoot"}
		got, err := resolveProvisioningDataDir(paths)
		if err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("systemd environment file overrides unit", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "systemd data")
		envFile := filepath.Join(t.TempDir(), "witmoot.env")
		if err := os.WriteFile(envFile, []byte("WITMOOT_DATA_DIR='"+want+"'\n"), 0600); err != nil {
			t.Fatal(err)
		}
		unit := filepath.Join(t.TempDir(), "witmoot.service")
		contents := "[Service]\nEnvironment=WITMOOT_DATA_DIR=/var/lib/witmoot\nEnvironmentFile=-" + envFile + "\n"
		if err := os.WriteFile(unit, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		paths := provisioningConfigPaths{systemdUnit: unit, systemdActive: true, serviceDefault: "/var/lib/witmoot"}
		got, err := resolveProvisioningDataDir(paths)
		if err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("explicit environment wins", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "explicit")
		t.Setenv("WITMOOT_DATA_DIR", want)
		got, err := resolveProvisioningDataDir(provisioningConfigPaths{serviceDefault: "/var/lib/witmoot"})
		if err != nil || got != want {
			t.Fatalf("resolve = %q, %v; want %q", got, err, want)
		}
	})
}

func TestResolveProvisioningDataDirRefusesAmbiguousOrDynamicConfig(t *testing.T) {
	t.Setenv("WITMOOT_DATA_DIR", "")
	openRC := filepath.Join(t.TempDir(), "openrc.conf")
	unit := filepath.Join(t.TempDir(), "witmoot.service")
	if err := os.WriteFile(openRC, []byte("WITMOOT_DATA_DIR=/var/lib/openrc\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("[Service]\nEnvironment=WITMOOT_DATA_DIR=/var/lib/systemd\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := provisioningConfigPaths{openRCConfig: openRC, openRCInstalled: true, systemdUnit: unit, serviceDefault: "/var/lib/witmoot"}
	if _, err := resolveProvisioningDataDir(paths); err == nil || !strings.Contains(err.Error(), "different Witmoot data directories") {
		t.Fatalf("ambiguous config error = %v", err)
	}
	if err := os.WriteFile(openRC, []byte("WITMOOT_DATA_DIR=\"${ROOT}/data\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths.systemdUnit = ""
	if _, err := resolveProvisioningDataDir(paths); err == nil || !strings.Contains(err.Error(), "shell expression") {
		t.Fatalf("dynamic config error = %v", err)
	}
}

func TestResolveProvisioningServiceAccount(t *testing.T) {
	openRC := filepath.Join(t.TempDir(), "witmoot.confd")
	if err := os.WriteFile(openRC, []byte("WITMOOT_USER=board-user\nWITMOOT_GROUP=board-group\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := provisioningConfigPaths{openRCConfig: openRC, openRCInstalled: true, openRCActive: true}
	user, group, managed, err := resolveProvisioningServiceAccount(paths)
	if err != nil || !managed || user != "board-user" || group != "board-group" {
		t.Fatalf("service account = %q:%q managed=%t err=%v", user, group, managed, err)
	}

	paths = provisioningConfigPaths{openRCInstalled: true}
	user, group, managed, err = resolveProvisioningServiceAccount(paths)
	if err != nil || !managed || user != "witmoot" || group != "witmoot" {
		t.Fatalf("default service account = %q:%q managed=%t err=%v", user, group, managed, err)
	}

	user, group, managed, err = resolveProvisioningServiceAccount(provisioningConfigPaths{})
	if err != nil || managed || user != "" || group != "" {
		t.Fatalf("portable install identity = %q:%q managed=%t err=%v", user, group, managed, err)
	}
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

// systemd reads EnvironmentFile= as one literal path, spaces and all.
func TestSystemdEnvironmentFilePathsAreLiteral(t *testing.T) {
	t.Setenv("WITMOOT_DATA_DIR", "")
	root := t.TempDir()
	want := filepath.Join(root, "board data")
	for _, name := range []string{"witmoot env", "  leading and trailing  ", "quoted \"name\"", "semi;colon", "-dash", "日本語 設定"} {
		t.Run(name, func(t *testing.T) {
			envFile := writeTestFile(t, filepath.Join(root, name, "witmoot.env"), "WITMOOT_DATA_DIR='"+want+"'\n")
			unit := writeTestFile(t, filepath.Join(t.TempDir(), "witmoot.service"), "[Service]\nEnvironmentFile=-"+envFile+"   \n")
			got, err := resolveProvisioningDataDir(provisioningConfigPaths{systemdUnit: unit, systemdActive: true, serviceDefault: "/var/lib/witmoot"})
			if err != nil || got != want {
				t.Fatalf("resolve = %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestSystemdEnvironmentFileRejectsHostilePaths(t *testing.T) {
	t.Setenv("WITMOOT_DATA_DIR", "")
	root := t.TempDir()
	present := writeTestFile(t, filepath.Join(root, "present.env"), "WITMOOT_DATA_DIR=/srv/present\n")
	for _, tc := range []struct{ setting, want string }{
		{"relative/witmoot.env", "not a literal absolute path"},
		{"-relative.env", "not a literal absolute path"},
		{"%h/witmoot.env", "not a literal absolute path"},
		{"/etc/%i.env", "not a literal absolute path"},
		{`"` + present + `"`, "not a literal absolute path"},
		{present + " " + present, "no such file"},
		{filepath.Join(root, "missing.env"), "no such file"},
	} {
		unit := writeTestFile(t, filepath.Join(t.TempDir(), "witmoot.service"), "[Service]\nEnvironmentFile="+tc.setting+"\n")
		if got, err := resolveProvisioningDataDir(provisioningConfigPaths{systemdUnit: unit, systemdActive: true, serviceDefault: "/var/lib/witmoot"}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("EnvironmentFile=%s resolved to %q (%v); want an error containing %q", tc.setting, got, err, tc.want)
		}
	}
	// An optional missing file is skipped, and a later empty setting resets
	// the list the way systemd does.
	unit := writeTestFile(t, filepath.Join(t.TempDir(), "witmoot.service"), "[Service]\nEnvironment=WITMOOT_DATA_DIR=/srv/unit\nEnvironmentFile=-"+filepath.Join(root, "missing.env")+"\nEnvironmentFile="+present+"\nEnvironmentFile=\n")
	if got, err := resolveProvisioningDataDir(provisioningConfigPaths{systemdUnit: unit, systemdActive: true, serviceDefault: "/var/lib/witmoot"}); err != nil || got != "/srv/unit" {
		t.Fatalf("resolve = %q, %v; want /srv/unit", got, err)
	}
}

func TestProvisioningSettingsFollowTheServiceConfiguration(t *testing.T) {
	for _, key := range instanceSettings {
		t.Setenv(key, "")
	}
	envFile := writeTestFile(t, filepath.Join(t.TempDir(), "with space", "witmoot.env"), "WITMOOT_BASE_URL=https://board.example.org\nWITMOOT_SMTP_HOST=relay.example.org\nWITMOOT_SMTP_FROM='Board <mail@example.org>'\n")
	unit := writeTestFile(t, filepath.Join(t.TempDir(), "witmoot.service"), "[Service]\nEnvironment=\"WITMOOT_NAME=Our Board\"\nEnvironmentFile="+envFile+"\n")
	paths := provisioningConfigPaths{systemdUnit: unit, systemdActive: true, serviceDefault: "/var/lib/witmoot"}
	settings, err := provisioningSettings(paths, instanceSettings...)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"WITMOOT_BASE_URL": "https://board.example.org", "WITMOOT_SMTP_HOST": "relay.example.org", "WITMOOT_SMTP_FROM": "Board <mail@example.org>", "WITMOOT_NAME": "Our Board"}
	if fmt.Sprint(settings) != fmt.Sprint(want) {
		t.Fatalf("settings = %v, want %v", settings, want)
	}
	t.Setenv("WITMOOT_BASE_URL", "https://override.example.org")
	if settings, err := provisioningSettings(paths, "WITMOOT_BASE_URL"); err != nil || settings["WITMOOT_BASE_URL"] != "https://override.example.org" {
		t.Fatalf("environment did not win: %v %v", settings, err)
	}
	options, err := adminOptions(paths)
	if err != nil || options.BaseURL != "https://override.example.org" || options.SiteName != "Our Board" || !options.Mailer.Enabled() {
		t.Fatalf("admin options = %+v, %v", options, err)
	}
	if settings, err := provisioningSettings(provisioningConfigPaths{}, "WITMOOT_SMTP_HOST"); err != nil || len(settings) != 0 {
		t.Fatalf("portable install read service settings: %v %v", settings, err)
	}
}
