// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sandbox constructs explicit Bubblewrap policies. It never falls back
// to an unrestricted process when confinement was requested.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Base exposes runtime libraries, private temporary space and minimal devices.
// The server keeps the host network for HTTP, mail and image hosts.
func Base() ([]string, error) {
	args := []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--cap-drop", "ALL", "--new-session"}
	mounts, err := runtimeMounts([]string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc/ld.so.cache", "/etc/alternatives"})
	if err != nil {
		return nil, err
	}
	args = append(args, mounts...)
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--dir", "/app", "--dir", "/run")
	return args, nil
}

// runtimeMounts binds each existing path read-only. On merged-/usr systems
// /bin, /lib and similar paths are symlinks into /usr; they are recreated as
// the same symlinks instead of separate mounts, provided they resolve inside a
// path bound earlier. Anything else is bound as before.
func runtimeMounts(paths []string) ([]string, error) {
	var args, bound []string
	for _, path := range paths {
		real, err := filepath.EvalSymlinks(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 && within(real, bound) {
			target, err := os.Readlink(path)
			if err != nil {
				return nil, err
			}
			args = append(args, "--symlink", target, path)
			continue
		}
		args = append(args, "--ro-bind", path, path)
		bound = append(bound, real)
	}
	return args, nil
}

func within(path string, roots []string) bool {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

// Check verifies that the requested namespaces actually work by running the
// given command, normally the bound server binary, inside them. The caller must
// treat an error as fatal, including when kernel or service policy forbids them.
func Check(ctx context.Context, binary string, args []string, env []string, command ...string) error {
	if len(command) == 0 {
		return errors.New("sandbox check needs a command to run")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, append(append(append([]string{}, args...), "--"), command...)...)
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("bubblewrap sandbox unavailable: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// RuntimeEnv intentionally excludes inherited loader settings and credentials.
func RuntimeEnv() []string {
	return []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C", "HOME=/tmp", "TMPDIR=/tmp"}
}

// Binary rejects the historical setuid installation mode. These policies
// require unprivileged user namespaces rather than a privileged launcher.
func Binary(path string) (string, error) {
	binary, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("bubblewrap is required: %w", err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(binary)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSetuid != 0 {
		return "", fmt.Errorf("setuid Bubblewrap is unsupported; use unprivileged user namespaces")
	}
	return binary, nil
}

// Service builds a server policy. The only host directory it can write is the
// existing data directory.
type Service struct {
	Prefix, DataDir, Executable string
	Env                         []string
}

func (s Service) Policy() ([]string, []string, error) {
	args, err := Base()
	if err != nil {
		return nil, nil, err
	}
	data, err := writableDir(s.DataDir)
	if err != nil {
		return nil, nil, err
	}
	args = append(args, "--bind", data, data)
	for _, path := range systemFiles() {
		args = append(args, "--ro-bind", path, path)
	}
	// The CA directory is needed by the SMTP and Imvault clients.
	if _, err := os.Stat("/etc/ssl/certs"); err == nil {
		args = append(args, "--ro-bind", "/etc/ssl/certs", "/etc/ssl/certs")
	}
	args = append(args, "--ro-bind", s.Executable, "/app/server", "--chdir", data)
	env := RuntimeEnv()
	bundle, err := certificateBundle(s.Env)
	if err != nil {
		return nil, nil, err
	}
	if bundle != "" {
		args = append(args, "--ro-bind", bundle, "/app/ca-bundle.crt")
		env = append(env, "SSL_CERT_FILE=/app/ca-bundle.crt")
	}
	for _, entry := range s.Env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, s.Prefix) && key != s.Prefix+"DATA_DIR" {
			env = append(env, entry)
		}
	}
	env = append(env, s.Prefix+"DATA_DIR="+data)
	return args, env, nil
}

func systemFiles() []string {
	var files []string
	for _, path := range []string{"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/localtime", "/etc/ssl/cert.pem"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			files = append(files, path)
		}
	}
	return files
}

func writableDir(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") {
		return "", fmt.Errorf("sandbox writable directory must be a clean absolute path: %q", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("sandbox directory must already exist: %w", err)
	}
	if err := validateWriteMount(path); err != nil {
		return "", err
	}
	if err := validateWriteMount(real); err != nil {
		return "", err
	}

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("sandbox writable directory is not a directory: %q", path)
	}
	return path, nil
}

// certificateBundle picks the CA bundle the server trusts. An SSL_CERT_FILE in
// the service environment names a custom bundle, for example one including a
// private CA for the mail relay; otherwise the system bundle is used. Binding
// it to a stable name also handles distributions whose CA paths are symlinks
// into directories which are otherwise hidden by the sandbox.
func certificateBundle(environment []string) (string, error) {
	custom := ""
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, "SSL_CERT_FILE="); ok {
			custom = value
		}
	}
	if custom != "" {
		if !filepath.IsAbs(custom) || strings.ContainsAny(custom, "\x00\r\n") {
			return "", fmt.Errorf("SSL_CERT_FILE must be an absolute path: %q", custom)
		}
		if info, err := os.Stat(custom); err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("SSL_CERT_FILE must name an existing certificate file: %q", custom)
		}
		return custom, nil
	}
	for _, path := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/ssl/cert.pem", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/ca-bundle.pem"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", nil
}

func validateWriteMount(name string) error {
	if name == "/" || name == "/var" || name == "/var/lib" || name == "/home" || name == "/root" || name == "/srv" || name == "/opt" || name == "/tmp" {
		return fmt.Errorf("sandbox writable directory is too broad: %q", name)
	}
	for _, reserved := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/proc", "/dev", "/run", "/app"} {
		if name == reserved || strings.HasPrefix(name, reserved+"/") {
			return fmt.Errorf("sandbox writable directory overlaps runtime files: %q", name)
		}
	}
	return nil
}
