// SPDX-License-Identifier: AGPL-3.0-or-later

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestServicePolicyKeepsSecretsOutOfArguments(t *testing.T) {
	data := t.TempDir()
	args, env, err := (Service{Prefix: "TEST_", DataDir: data, Executable: "/usr/bin/true", Env: []string{
		"TEST_SMTP_PASSWORD=secret-value", "TEST_DATA_DIR=wrong", "AWS_SECRET_ACCESS_KEY=unrelated", "LD_PRELOAD=host-loader", "TMPDIR=/host/tmp",
	}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	for _, required := range []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--cap-drop", "--new-session", "--tmpfs", "--ro-bind"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing confinement: %s", required)
		}
	}
	// The data directory is the one writable host path, and nothing else is.
	if !strings.Contains(joined, "\n--bind\n"+data+"\n"+data+"\n") {
		t.Errorf("data directory is not a writable bind: %s", joined)
	}
	if strings.Count("\n"+joined, "\n--bind\n") != 1 {
		t.Errorf("unexpected writable mounts: %s", joined)
	}
	for _, forbidden := range []string{"secret-value", "--unshare-net", "--die-with-parent", "--ro-bind\n/\n/", "--ro-bind\n" + data + "\n"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("unsafe service policy contains %s", forbidden)
		}
	}
	joined = strings.Join(env, "\n")
	for _, required := range []string{"TEST_SMTP_PASSWORD=secret-value", "TEST_DATA_DIR=" + data, "TMPDIR=/tmp"} {
		if !strings.Contains(joined, required) {
			t.Errorf("environment missing %s", required)
		}
	}
	for _, forbidden := range []string{"LD_PRELOAD", "AWS_SECRET", "=wrong", "/host/tmp", "PASSWORD=secret-value\nPASSWORD"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("environment leaks %s", forbidden)
		}
	}
	if strings.Contains(strings.Join(RuntimeEnv(), "\n"), "PASSWORD") {
		t.Fatal("runtime environment contains credentials")
	}
}

func TestWritableDirectoriesRejectBroadAndAdversarialPaths(t *testing.T) {
	for _, path := range []string{"/", "/var", "/var/lib", "/home", "/root", "/srv", "/tmp", "/usr", "/usr/local", "/etc", "/proc", "/dev", "/app", "relative", "/var/lib/../lib", "/var/lib/evil\nname", "/var/lib/evil\x00name"} {
		if _, err := writableDir(path); err == nil {
			t.Errorf("accepted unsafe directory %q", path)
		}
		if _, _, err := (Service{Prefix: "TEST_", DataDir: path, Executable: "/usr/bin/true"}).Policy(); err == nil {
			t.Errorf("policy accepted unsafe data directory %q", path)
		}
	}
	root := t.TempDir()
	link := filepath.Join(root, "looks-safe")
	if err := os.Symlink("/", link); err != nil {
		t.Fatal(err)
	}
	if _, err := writableDir(link); err == nil {
		t.Fatal("symlink exposed the entire host")
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(root, "missing")} {
		if _, err := writableDir(path); err == nil {
			t.Fatalf("accepted non-directory %s", path)
		}
	}
	for _, name := range []string{"space and & punctuation", "--option-looking", "日本語"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if got, err := writableDir(path); err != nil || got != path {
			t.Fatalf("safe directory %q: %s %v", path, got, err)
		}
	}
}

// mountFor returns how runtimeMounts exposed one path.
func mountFor(args []string, path string) []string {
	for i := 0; i+2 < len(args); i++ {
		if args[i+2] == path && (args[i] == "--ro-bind" || args[i] == "--symlink") {
			return args[i : i+3]
		}
	}
	return nil
}

func TestDynamicLoaderPathsAreReadOnly(t *testing.T) {
	args, err := Base()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc/ld.so.cache", "/etc/alternatives"} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		mount := mountFor(args, path)
		if mount == nil {
			t.Fatalf("runtime path is missing: %s", path)
		}
		if mount[0] == "--symlink" {
			if target, err := os.Readlink(path); err != nil || target != mount[1] {
				t.Fatalf("%s recreated as a different symlink: %v (%v)", path, mount, err)
			}
		} else if mount[1] != path {
			t.Fatalf("runtime path is not bound read-only in place: %v", mount)
		}
	}
	joined := strings.Join(args, "\n")
	if strings.Contains(joined, "--ro-bind\n/etc\n/etc") || strings.Contains(joined, "\n--bind\n") {
		t.Fatal("runtime libraries exposed the host configuration or a writable path")
	}
}

func TestRuntimeMountsRecreateMergedUsrSymlinks(t *testing.T) {
	root := t.TempDir()
	usr, outside := filepath.Join(root, "usr"), filepath.Join(root, "outside")
	for _, dir := range []string{filepath.Join(usr, "bin"), filepath.Join(usr, "lib"), outside} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{"bin": "usr/bin", "lib64": "usr/lib", "escape": "outside", "dangling": "usr/missing", "absolute": filepath.Join(usr, "lib")}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	path := func(name string) string { return filepath.Join(root, name) }
	args, err := runtimeMounts([]string{usr, path("bin"), path("lib64"), path("escape"), path("dangling"), path("absolute"), path("missing")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--ro-bind", usr, usr,
		"--symlink", "usr/bin", path("bin"),
		"--symlink", "usr/lib", path("lib64"),
		"--ro-bind", path("escape"), path("escape"),
		"--symlink", filepath.Join(usr, "lib"), path("absolute"),
	}
	if !slices.Equal(args, want) {
		t.Fatalf("runtime mounts:\n got %q\nwant %q", args, want)
	}
	// A symlink listed before the directory it points into is bound, not
	// recreated, because the sandbox would not contain its target yet.
	args, err = runtimeMounts([]string{path("bin"), usr})
	if err != nil {
		t.Fatal(err)
	}
	if mount := mountFor(args, path("bin")); mount == nil || mount[0] != "--ro-bind" {
		t.Fatalf("symlink into an unbound path was recreated: %q", args)
	}
}

func TestSandboxSetupFailureIsAnError(t *testing.T) {
	if err := Check(context.Background(), "/does-not-exist", nil, nil, "/app/server"); err == nil {
		t.Fatal("missing Bubblewrap accepted")
	}
	if err := Check(context.Background(), "/usr/bin/false", nil, nil, "/app/server"); err == nil {
		t.Fatal("failed confinement accepted")
	}
	if err := Check(context.Background(), "/usr/bin/true", nil, nil); err == nil {
		t.Fatal("check without a command accepted")
	}
}

// Check must run the command it is given inside the sandbox rather than a
// host utility that split-/usr systems keep elsewhere.
func TestCheckRunsTheGivenCommand(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "arguments")
	fake := filepath.Join(dir, "bwrap")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$RECORD\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Check(context.Background(), fake, []string{"--unshare-user"}, []string{"RECORD=" + record}, "/app/server", "--help"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "--unshare-user\n--\n/app/server\n--help\n" {
		t.Fatalf("check ran %q", got)
	}
}

func TestServicePolicyHonoursCustomCertificateBundle(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "private ca.pem")
	if err := os.WriteFile(bundle, []byte("-----BEGIN CERTIFICATE-----\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args, env, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", Env: []string{"SSL_CERT_FILE=" + bundle}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, "\n"), "--ro-bind\n"+bundle+"\n/app/ca-bundle.crt") {
		t.Fatalf("custom bundle not bound: %q", args)
	}
	if !slices.Contains(env, "SSL_CERT_FILE=/app/ca-bundle.crt") || strings.Contains(strings.Join(env, "\n"), bundle) {
		t.Fatalf("custom bundle not selected inside the sandbox: %q", env)
	}
}

func TestCustomCertificateBundleMustBeAnExistingFile(t *testing.T) {
	for _, path := range []string{t.TempDir(), "/missing-file", "relative.pem", "/tmp/file\nname", "/tmp/file\x00name"} {
		_, _, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: "/usr/bin/true", Env: []string{"SSL_CERT_FILE=" + path}}).Policy()
		if err == nil {
			t.Errorf("accepted certificate bundle %q", path)
		}
	}
}

func TestRealBubblewrapRuntimeBoundary(t *testing.T) {
	if os.Getenv("COMFYWARE_SANDBOX_TEST") != "1" {
		t.Skip("set COMFYWARE_SANDBOX_TEST=1 to require real Linux namespace tests")
	}
	binary, err := exec.LookPath("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	args, env, err := (Service{Prefix: "TEST_", DataDir: data, Executable: "/bin/sh", Env: []string{"UNRELATED_PASSWORD=hidden"}}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "host-secret")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	// Merged-/usr symlinks stay symlinks, so a path like /lib64/ld-linux keeps
	// resolving the same way it does on the host.
	var links []string
	for _, path := range []string{"/bin", "/sbin", "/lib", "/lib64"} {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 && mountFor(args, path)[0] == "--symlink" {
			links = append(links, path)
		}
	}
	script := `test ! -e "$1" && test ! -e /etc/shadow && test -z "$UNRELATED_PASSWORD" && test ! -w /usr && touch "$2/written" && shift 2 && for link; do test -L "$link" && test -d "$link/"; done`
	cmd := exec.Command(binary, append(args, append([]string{"--", "/app/server", "-c", script, "check", secret, data}, links...)...)...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("service confinement failed: %v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(data, "written")); err != nil {
		t.Fatalf("data directory was not writable: %v", err)
	}
}

// The check must not depend on host utilities being in /usr/bin; hiding that
// directory simulates a split-/usr host where /usr/bin/true does not exist.
func TestRealCheckDoesNotNeedHostUtilities(t *testing.T) {
	if os.Getenv("COMFYWARE_SANDBOX_TEST") != "1" {
		t.Skip("set COMFYWARE_SANDBOX_TEST=1 to require real Linux namespace tests")
	}
	binary, err := exec.LookPath("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args, env, err := (Service{Prefix: "TEST_", DataDir: t.TempDir(), Executable: executable}).Policy()
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, "--tmpfs", "/usr/bin")
	if err := Check(t.Context(), binary, args, env, "/app/server", "-test.run=^$"); err != nil {
		t.Fatal(err)
	}
}

func TestSetuidBubblewrapIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bwrap")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	if _, err := Binary(path); err == nil {
		t.Fatal("setuid launcher accepted")
	}
}
