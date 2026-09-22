package scripts_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runMake(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("make", append([]string{"-C", ".."}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make %v: %v\n%s", args, err, out)
	}
}

func TestInstallServicesPreservesConfiguration(t *testing.T) {
	for _, prefix := range []string{"/usr/local", "/usr", "/opt/our board & photos"} {
		t.Run(prefix, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "stage with spaces")
			args := []string{"install-openrc", "install-systemd", "DESTDIR=" + dest, "PREFIX=" + prefix}
			runMake(t, args...)
			cmd := exec.Command("sh", "-c", `. "$1" || exit 1; printf '%s' "$command"`, "test", filepath.Join(dest, "etc/init.d/witmoot"))
			cmd.Env = []string{"PATH=/usr/bin:/bin", "RC_SVCNAME=witmoot"}
			if out, err := cmd.CombinedOutput(); err != nil || string(out) != prefix+"/bin/witmoot" {
				t.Fatalf("OpenRC binary: %s (%v)", out, err)
			}
			unit, err := os.ReadFile(filepath.Join(dest, "etc/systemd/system/witmoot.service"))
			if err != nil || !strings.Contains(string(unit), `ExecStart="`+prefix+`/bin/witmoot"`) || strings.Contains(string(unit), dest) {
				t.Fatalf("systemd binary path: %s (%v)", unit, err)
			}
			configs := map[string]os.FileMode{
				"etc/conf.d/witmoot":      0600,
				"etc/witmoot/witmoot.env": 0600,
				"etc/logrotate.d/witmoot": 0644,
			}
			for path, mode := range configs {
				path = filepath.Join(dest, path)
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("permissions on %s: %v (%v)", path, info, err)
				}
				if err := os.WriteFile(path, []byte("# local settings\n"), mode); err != nil {
					t.Fatal(err)
				}
			}
			runMake(t, args...)
			for path := range configs {
				path = filepath.Join(dest, path)
				if data, err := os.ReadFile(path); err != nil || string(data) != "# local settings\n" {
					t.Fatalf("reinstall overwrote %s (%v)", path, err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("managed-elsewhere", path); err != nil {
					t.Fatal(err)
				}
			}
			runMake(t, args...)
			for path := range configs {
				path = filepath.Join(dest, path)
				if target, err := os.Readlink(path); err != nil || target != "managed-elsewhere" {
					t.Fatalf("reinstall replaced symlink %s (%v)", path, err)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("reinstall followed dangling symlink %s", path)
				}
			}
		})
	}
}

func TestCustomConfigurationDirectories(t *testing.T) {
	dest := t.TempDir()
	runMake(t, "install-openrc", "install-systemd", "DESTDIR="+dest,
		"SYSCONFDIR=/custom config", "UNITDIR=/custom units", "LOGROTATEDIR=/custom rotation")
	for _, path := range []string{"custom config/init.d/witmoot", "custom config/conf.d/witmoot", "custom config/witmoot/witmoot.env", "custom rotation/witmoot"} {
		if _, err := os.Stat(filepath.Join(dest, path)); err != nil {
			t.Fatal(err)
		}
	}
	unit, err := os.ReadFile(filepath.Join(dest, "custom units/witmoot.service"))
	if err != nil || !strings.Contains(string(unit), `EnvironmentFile="-/custom config/witmoot/witmoot.env"`) {
		t.Fatalf("systemd config path: %s (%v)", unit, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "etc")); !os.IsNotExist(err) {
		t.Fatal("custom installation also wrote the default configuration path")
	}
}

func TestInstalledBinaryProvisionsOwner(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "stage with spaces")
	runMake(t, "install", "DESTDIR="+dest, "PREFIX=/usr")
	for _, name := range []string{"LICENSE", "README.md", "THIRD_PARTY.md"} {
		want, err := os.ReadFile(filepath.Join("..", name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dest, "usr/share/doc/witmoot", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("installed %s differs from the source: %v", name, err)
		}
	}
	dataDir := filepath.Join(t.TempDir(), "board data")
	cmd := exec.Command(filepath.Join(dest, "usr/bin/witmoot"), "create-owner", "--username", "owner", "--password-stdin")
	cmd.Env = append(os.Environ(), "WITMOOT_DATA_DIR="+dataDir)
	cmd.Stdin = strings.NewReader("owner-test-password\n")
	if out, err := cmd.CombinedOutput(); err != nil || !bytes.Contains(out, []byte("Owner created")) {
		t.Fatalf("installed binary: %s (%v)", out, err)
	}
	if info, err := os.Stat(dataDir); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("owner data directory permissions: %v (%v)", info, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "witmoot.db")); err != nil {
		t.Fatal(err)
	}
}
