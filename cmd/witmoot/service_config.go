package main

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type provisioningConfigPaths struct {
	openRCConfig    string
	openRCInstalled bool
	openRCActive    bool
	systemdUnit     string
	systemdActive   bool
	serviceDefault  string
}

func defaultProvisioningConfigPaths() provisioningConfigPaths {
	openRCConfig := "/etc/conf.d/witmoot"
	openRCInit := "/etc/init.d/witmoot"
	_, openRCErr := os.Stat(openRCConfig)
	_, initErr := os.Stat(openRCInit)
	openRCInstalled := openRCErr == nil || initErr == nil
	if openRCErr != nil && !errors.Is(openRCErr, os.ErrNotExist) {
		openRCInstalled = true
	}
	if initErr != nil && !errors.Is(initErr, os.ErrNotExist) {
		openRCInstalled = true
	}
	var systemdUnit string
	for _, path := range []string{
		"/etc/systemd/system/witmoot.service",
		"/run/systemd/system/witmoot.service",
		"/usr/local/lib/systemd/system/witmoot.service",
		"/usr/lib/systemd/system/witmoot.service",
		"/lib/systemd/system/witmoot.service",
	} {
		if _, err := os.Stat(path); err == nil {
			systemdUnit = path
			break
		}
	}
	_, openRCActiveErr := os.Stat("/run/openrc/softlevel")
	_, systemdActiveErr := os.Stat("/run/systemd/system")
	return provisioningConfigPaths{
		openRCConfig:    openRCConfig,
		openRCInstalled: openRCInstalled,
		openRCActive:    openRCActiveErr == nil,
		systemdUnit:     systemdUnit,
		systemdActive:   systemdActiveErr == nil,
		serviceDefault:  "/var/lib/witmoot",
	}
}

// serviceManagers records which service configuration the account commands
// follow: the running manager's when it is installed, otherwise every
// installed one.
type serviceManagers struct{ openRC, systemd bool }

func activeServiceManagers(paths provisioningConfigPaths) serviceManagers {
	openRC := paths.openRCInstalled || paths.openRCConfig != "" && fileExists(paths.openRCConfig)
	systemd := paths.systemdUnit != "" && fileExists(paths.systemdUnit)
	switch {
	case paths.openRCActive && openRC:
		return serviceManagers{openRC: true}
	case paths.systemdActive && systemd:
		return serviceManagers{systemd: true}
	}
	return serviceManagers{openRC: openRC, systemd: systemd}
}

func (m serviceManagers) managed() bool { return m.openRC || m.systemd }

// serviceSetting reads one setting as the installed service sees it, using
// fallback when the configuration leaves it unset. With both managers
// installed and neither running, they must agree; what names the setting in
// that error.
func serviceSetting(paths provisioningConfigPaths, key, fallback, what string) (string, error) {
	managers := activeServiceManagers(paths)
	var openRCValue, systemdValue string
	if managers.openRC {
		value, _, err := readShellConfigValue(paths.openRCConfig, key)
		if err != nil {
			return "", err
		}
		openRCValue = cmp.Or(value, fallback)
	}
	if managers.systemd {
		value, _, err := readSystemdEnvironment(paths.systemdUnit, key)
		if err != nil {
			return "", err
		}
		systemdValue = cmp.Or(value, fallback)
	}
	if managers.openRC && managers.systemd && openRCValue != systemdValue {
		return "", fmt.Errorf("OpenRC and systemd configure different %s (%q and %q); set %s explicitly or run under the active service manager", what, openRCValue, systemdValue, key)
	}
	if managers.openRC {
		return openRCValue, nil
	}
	return systemdValue, nil
}

func resolveProvisioningDataDir(paths provisioningConfigPaths) (string, error) {
	if value := os.Getenv("WITMOOT_DATA_DIR"); value != "" {
		return value, nil
	}
	if !activeServiceManagers(paths).managed() {
		return "./data", nil
	}
	value, err := serviceSetting(paths, "WITMOOT_DATA_DIR", paths.serviceDefault, "Witmoot data directories")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("the service's Witmoot data directory %q is not absolute; set WITMOOT_DATA_DIR explicitly", value)
	}
	return filepath.Clean(value), nil
}

// provisioningSettings resolves the given settings for an account command the
// way the running service sees them: the process environment first, then the
// service configuration. Unset settings are left out.
func provisioningSettings(paths provisioningConfigPaths, keys ...string) (map[string]string, error) {
	settings := make(map[string]string, len(keys))
	for _, key := range keys {
		value := os.Getenv(key)
		if value == "" && activeServiceManagers(paths).managed() {
			var err error
			if value, err = serviceSetting(paths, key, "", key+" values"); err != nil {
				return nil, fmt.Errorf("%w; set %s in the environment to override the service configuration", err, key)
			}
		}
		if value != "" {
			settings[key] = value
		}
	}
	return settings, nil
}

func resolveProvisioningServiceAccount(paths provisioningConfigPaths) (string, string, bool, error) {
	managers := activeServiceManagers(paths)
	if !managers.managed() {
		return "", "", false, nil
	}
	var openRCUser, openRCGroup, systemdUser, systemdGroup string
	var err error
	if managers.openRC {
		if openRCUser, openRCGroup, err = openRCServiceAccount(paths); err != nil {
			return "", "", true, err
		}
	}
	if managers.systemd {
		if systemdUser, systemdGroup, err = systemdServiceAccount(paths); err != nil {
			return "", "", true, err
		}
	}
	if managers.openRC && managers.systemd && (openRCUser != systemdUser || openRCGroup != systemdGroup) {
		return "", "", true, fmt.Errorf("OpenRC and systemd configure different Witmoot service accounts (%s:%s and %s:%s); run under the active service manager", openRCUser, openRCGroup, systemdUser, systemdGroup)
	}
	if managers.openRC {
		return openRCUser, openRCGroup, true, nil
	}
	return systemdUser, systemdGroup, true, nil
}

func openRCServiceAccount(paths provisioningConfigPaths) (string, string, error) {
	user, _, err := readShellConfigValue(paths.openRCConfig, "WITMOOT_USER")
	if err != nil {
		return "", "", err
	}
	group, _, err := readShellConfigValue(paths.openRCConfig, "WITMOOT_GROUP")
	if err != nil {
		return "", "", err
	}
	return cmp.Or(user, "witmoot"), cmp.Or(group, "witmoot"), nil
}

func systemdServiceAccount(paths provisioningConfigPaths) (string, string, error) {
	var user, group string
	err := scanSystemdService(paths.systemdUnit, func(name, value, _ string, _ int) error {
		switch name {
		case "User":
			user = strings.Trim(value, "\"'")
		case "Group":
			group = strings.Trim(value, "\"'")
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return cmp.Or(user, "root"), group, nil
}

// scanSystemdService calls visit for each setting in the [Service] sections of
// a unit and its drop-ins, in the order systemd applies them.
func scanSystemdService(unitPath string, visit func(name, value, path string, line int) error) error {
	if unitPath == "" {
		return nil
	}
	configPaths, err := systemdUnitConfigFiles(unitPath)
	if err != nil {
		return err
	}
	for _, path := range configPaths {
		if err := scanSystemdFile(path, visit); err != nil {
			return err
		}
	}
	return nil
}

func scanSystemdFile(path string, visit func(name, value, path string, line int) error) (err error) {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read systemd unit %s: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, closeErr)
		}
	}()
	inService := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			inService = text == "[Service]"
			continue
		}
		if !inService || text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
			continue
		}
		if name, value, ok := strings.Cut(text, "="); ok {
			if err := visit(strings.TrimSpace(name), strings.TrimSpace(value), path, line); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func readShellConfigValue(path, key string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read %s: %w; run create-owner as a user that can read the service configuration, or set WITMOOT_DATA_DIR explicitly", path, err)
	}
	defer closeConfig(path, file)

	var value string
	found := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if strings.HasPrefix(text, "export ") {
			text = strings.TrimSpace(strings.TrimPrefix(text, "export "))
		}
		name, raw, ok := strings.Cut(text, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		parsed, err := parseConfigValue(strings.TrimSpace(raw), true)
		if err != nil {
			return "", false, fmt.Errorf("parse %s:%d: %w; set WITMOOT_DATA_DIR explicitly if the value uses shell expansion", path, line, err)
		}
		value, found = parsed, true
	}
	if err := scanner.Err(); err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	return value, found, nil
}

// readSystemdEnvironment reads key from a unit's Environment= settings and
// EnvironmentFile= files, with later files taking precedence as in systemd.
func readSystemdEnvironment(unitPath, key string) (string, bool, error) {
	unitValues := make(map[string]string)
	var environmentFiles []string
	err := scanSystemdService(unitPath, func(name, raw, path string, line int) error {
		switch name {
		case "Environment":
			if raw == "" {
				clear(unitValues)
				return nil
			}
			words, err := splitConfigWords(raw)
			if err != nil {
				return fmt.Errorf("parse systemd unit %s:%d: %w", path, line, err)
			}
			for _, word := range words {
				if name, value, ok := strings.Cut(word, "="); ok {
					unitValues[name] = value
				}
			}
		case "EnvironmentFile":
			if raw == "" {
				environmentFiles = nil
				return nil
			}
			environmentFiles = append(environmentFiles, raw)
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	value, found := unitValues[key]
	for _, environmentFile := range environmentFiles {
		fileValue, fileFound, err := readSystemdEnvironmentFile(environmentFile, unitPath, key)
		if err != nil {
			return "", false, err
		}
		if fileFound {
			value, found = fileValue, true
		}
	}
	return value, found, nil
}

// readSystemdEnvironmentFile reads one EnvironmentFile= setting. systemd takes
// the whole value as one literal path, spaces included and without quotes,
// optionally prefixed with "-" when the file may be missing.
func readSystemdEnvironmentFile(setting, unitPath, key string) (string, bool, error) {
	path, optional := strings.CutPrefix(setting, "-")
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "%\x00") {
		return "", false, fmt.Errorf("systemd EnvironmentFile %q in %s is not a literal absolute path; set %s explicitly", setting, unitPath, key)
	}
	file, err := os.Open(path)
	if err != nil {
		if optional && errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read systemd environment file %s: %w", path, err)
	}
	defer closeConfig(path, file)
	var value string
	found := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
			continue
		}
		name, raw, ok := strings.Cut(text, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		parsed, err := parseConfigValue(strings.TrimSpace(raw), false)
		if err != nil {
			return "", false, fmt.Errorf("parse %s:%d: %w", path, line, err)
		}
		value, found = parsed, true
	}
	if err := scanner.Err(); err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	return value, found, nil
}

func systemdUnitConfigFiles(unitPath string) ([]string, error) {
	unitName := filepath.Base(unitPath)
	directories := []string{
		"/etc/systemd/system",
		"/run/systemd/system",
		"/usr/local/lib/systemd/system",
		"/usr/lib/systemd/system",
		"/lib/systemd/system",
	}
	selected := make(map[string]string)
	for _, directory := range directories {
		entries, err := os.ReadDir(filepath.Join(directory, unitName+".d"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read systemd overrides in %s: %w", filepath.Join(directory, unitName+".d"), err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".conf") {
				continue
			}
			if _, exists := selected[name]; !exists {
				selected[name] = filepath.Join(directory, unitName+".d", name)
			}
		}
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	files := []string{unitPath}
	for _, name := range names {
		files = append(files, selected[name])
	}
	return files, nil
}

func parseConfigValue(raw string, shell bool) (string, error) {
	var value strings.Builder
	var quote rune
	escaped := false
	for index, r := range raw {
		if escaped {
			value.WriteRune(r)
			escaped = false
			continue
		}
		if quote != '\'' && r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if shell && quote == '"' && (r == '$' || r == '`') {
				return "", fmt.Errorf("shell expression %q is not supported", raw)
			}
			if r == quote {
				quote = 0
			} else {
				value.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if shell && (r == '$' || r == '`' || r == ';') {
			return "", fmt.Errorf("shell expression %q is not supported", raw)
		}
		if r == '#' && (index == 0 || index > 0 && (raw[index-1] == ' ' || raw[index-1] == '\t')) {
			break
		}
		if shell && (r == ' ' || r == '\t') {
			remainder := strings.TrimSpace(raw[index:])
			if strings.HasPrefix(remainder, "#") {
				break
			}
			return "", fmt.Errorf("unquoted whitespace in %q", raw)
		}
		value.WriteRune(r)
	}
	if escaped || quote != 0 {
		return "", errors.New("unterminated quote or escape")
	}
	result := strings.TrimSpace(value.String())
	return result, nil
}

func splitConfigWords(raw string) ([]string, error) {
	var words []string
	var word strings.Builder
	var quote rune
	escaped := false
	wordStarted := false
	for _, r := range raw {
		if escaped {
			word.WriteRune(r)
			escaped = false
			wordStarted = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			wordStarted = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			wordStarted = true
			continue
		}
		if r == ' ' || r == '\t' {
			if wordStarted {
				words = append(words, word.String())
				word.Reset()
				wordStarted = false
			}
			continue
		}
		word.WriteRune(r)
		wordStarted = true
	}
	if escaped || quote != 0 {
		return nil, errors.New("unterminated quote or escape")
	}
	if wordStarted {
		words = append(words, word.String())
	}
	return words, nil
}

// closeConfig closes a configuration file that has been read in full; a
// failure cannot change what was read, so it is only logged.
func closeConfig(path string, file *os.File) {
	if err := file.Close(); err != nil {
		slog.Debug("close configuration file", "path", path, "error", err)
	}
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
