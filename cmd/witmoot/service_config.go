package main

import (
	"bufio"
	"errors"
	"fmt"
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

func resolveProvisioningDataDir(paths provisioningConfigPaths) (string, error) {
	if value := os.Getenv("WITMOOT_DATA_DIR"); value != "" {
		return value, nil
	}
	openRCInstalled := paths.openRCInstalled || paths.openRCConfig != "" && fileExists(paths.openRCConfig)
	systemdInstalled := paths.systemdUnit != "" && fileExists(paths.systemdUnit)
	if !openRCInstalled && !systemdInstalled {
		return "./data", nil
	}
	if paths.openRCActive && openRCInstalled {
		return openRCProvisioningDataDir(paths)
	}
	if paths.systemdActive && systemdInstalled {
		return systemdProvisioningDataDir(paths)
	}
	if openRCInstalled && systemdInstalled {
		openRCDir, err := openRCProvisioningDataDir(paths)
		if err != nil {
			return "", err
		}
		systemdDir, err := systemdProvisioningDataDir(paths)
		if err != nil {
			return "", err
		}
		if filepath.Clean(openRCDir) != filepath.Clean(systemdDir) {
			return "", fmt.Errorf("OpenRC and systemd configure different Witmoot data directories (%q and %q); set WITMOOT_DATA_DIR explicitly or run under the active service manager", openRCDir, systemdDir)
		}
		return openRCDir, nil
	}
	if openRCInstalled {
		return openRCProvisioningDataDir(paths)
	}
	return systemdProvisioningDataDir(paths)
}

func resolveProvisioningServiceAccount(paths provisioningConfigPaths) (string, string, bool, error) {
	openRCInstalled := paths.openRCInstalled || paths.openRCConfig != "" && fileExists(paths.openRCConfig)
	systemdInstalled := paths.systemdUnit != "" && fileExists(paths.systemdUnit)
	if !openRCInstalled && !systemdInstalled {
		return "", "", false, nil
	}
	if paths.openRCActive && openRCInstalled {
		user, group, err := openRCServiceAccount(paths)
		return user, group, true, err
	}
	if paths.systemdActive && systemdInstalled {
		user, group, err := systemdServiceAccount(paths)
		return user, group, true, err
	}
	if openRCInstalled && systemdInstalled {
		openRCUser, openRCGroup, err := openRCServiceAccount(paths)
		if err != nil {
			return "", "", true, err
		}
		systemdUser, systemdGroup, err := systemdServiceAccount(paths)
		if err != nil {
			return "", "", true, err
		}
		if openRCUser != systemdUser || openRCGroup != systemdGroup {
			return "", "", true, fmt.Errorf("OpenRC and systemd configure different Witmoot service accounts (%s:%s and %s:%s); run under the active service manager", openRCUser, openRCGroup, systemdUser, systemdGroup)
		}
		return openRCUser, openRCGroup, true, nil
	}
	if openRCInstalled {
		user, group, err := openRCServiceAccount(paths)
		return user, group, true, err
	}
	user, group, err := systemdServiceAccount(paths)
	return user, group, true, err
}

func openRCServiceAccount(paths provisioningConfigPaths) (string, string, error) {
	user, userSet, err := readShellConfigValue(paths.openRCConfig, "WITMOOT_USER")
	if err != nil {
		return "", "", err
	}
	group, groupSet, err := readShellConfigValue(paths.openRCConfig, "WITMOOT_GROUP")
	if err != nil {
		return "", "", err
	}
	if !userSet || user == "" {
		user = "witmoot"
	}
	if !groupSet || group == "" {
		group = "witmoot"
	}
	return user, group, nil
}

func systemdServiceAccount(paths provisioningConfigPaths) (string, string, error) {
	user, userSet, err := readSystemdServiceSetting(paths.systemdUnit, "User")
	if err != nil {
		return "", "", err
	}
	group, groupSet, err := readSystemdServiceSetting(paths.systemdUnit, "Group")
	if err != nil {
		return "", "", err
	}
	if !userSet || user == "" {
		user = "root"
	}
	if !groupSet {
		group = ""
	}
	return user, group, nil
}

func readSystemdServiceSetting(unitPath, key string) (string, bool, error) {
	if unitPath == "" {
		return "", false, nil
	}
	configPaths, err := systemdUnitConfigFiles(unitPath)
	if err != nil {
		return "", false, err
	}
	var value string
	found := false
	for _, path := range configPaths {
		file, err := os.Open(path)
		if err != nil {
			return "", false, fmt.Errorf("read systemd unit %s: %w", path, err)
		}
		inService := false
		scanner := bufio.NewScanner(file)
		for line := 1; scanner.Scan(); line++ {
			text := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
				inService = text == "[Service]"
				continue
			}
			if !inService || text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
				continue
			}
			name, raw, ok := strings.Cut(text, "=")
			if ok && strings.TrimSpace(name) == key {
				value, found = strings.Trim(strings.TrimSpace(raw), "\"'"), true
			}
		}
		if err := scanner.Err(); err != nil {
			file.Close()
			return "", false, fmt.Errorf("read %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return "", false, fmt.Errorf("close %s: %w", path, err)
		}
	}
	return value, found, nil
}

func openRCProvisioningDataDir(paths provisioningConfigPaths) (string, error) {
	value, found, err := readShellConfigValue(paths.openRCConfig, "WITMOOT_DATA_DIR")
	if err != nil {
		return "", err
	}
	if !found || value == "" {
		value = paths.serviceDefault
	}
	return validateServiceDataDir(value, "OpenRC")
}

func systemdProvisioningDataDir(paths provisioningConfigPaths) (string, error) {
	value, found, err := readSystemdDataDir(paths.systemdUnit, "WITMOOT_DATA_DIR")
	if err != nil {
		return "", err
	}
	if !found || value == "" {
		value = paths.serviceDefault
	}
	return validateServiceDataDir(value, "systemd")
}

func validateServiceDataDir(value, manager string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s Witmoot data directory %q is not absolute; set WITMOOT_DATA_DIR explicitly", manager, value)
	}
	return value, nil
}

func readShellConfigValue(path, key string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read %s: %w; run create-owner as a user that can read the service configuration, or set WITMOOT_DATA_DIR explicitly", path, err)
	}
	defer file.Close()

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

func readSystemdDataDir(unitPath, key string) (string, bool, error) {
	if unitPath == "" {
		return "", false, nil
	}
	unitValues := make(map[string]string)
	var environmentFiles []string
	configPaths, err := systemdUnitConfigFiles(unitPath)
	if err != nil {
		return "", false, err
	}
	for _, configPath := range configPaths {
		file, err := os.Open(configPath)
		if err != nil {
			return "", false, fmt.Errorf("read systemd unit %s: %w", configPath, err)
		}
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
			name, raw, ok := strings.Cut(text, "=")
			if !ok {
				continue
			}
			switch strings.TrimSpace(name) {
			case "Environment":
				if strings.TrimSpace(raw) == "" {
					clear(unitValues)
					continue
				}
				words, err := splitConfigWords(raw)
				if err != nil {
					file.Close()
					return "", false, fmt.Errorf("parse systemd unit %s:%d: %w", configPath, line, err)
				}
				for _, word := range words {
					name, value, ok := strings.Cut(word, "=")
					if ok {
						unitValues[name] = value
					}
				}
			case "EnvironmentFile":
				if strings.TrimSpace(raw) == "" {
					environmentFiles = nil
					continue
				}
				words, err := splitConfigWords(raw)
				if err != nil {
					file.Close()
					return "", false, fmt.Errorf("parse systemd unit %s:%d: %w", configPath, line, err)
				}
				environmentFiles = append(environmentFiles, words...)
			}
		}
		if err := scanner.Err(); err != nil {
			file.Close()
			return "", false, fmt.Errorf("read %s: %w", configPath, err)
		}
		if err := file.Close(); err != nil {
			return "", false, fmt.Errorf("close %s: %w", configPath, err)
		}
	}
	value, found := unitValues[key]
	for _, environmentFile := range environmentFiles {
		optional := strings.HasPrefix(environmentFile, "-")
		path := strings.TrimPrefix(environmentFile, "-")
		if !filepath.IsAbs(path) {
			return "", false, fmt.Errorf("systemd EnvironmentFile %q in %s is not an absolute path", path, unitPath)
		}
		fileValue, fileFound, err := readSystemdEnvironmentFile(path, key)
		if err != nil {
			if optional && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", false, err
		}
		if fileFound {
			value, found = fileValue, true
		}
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

func readSystemdEnvironmentFile(path, key string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, fmt.Errorf("read systemd environment file %s: %w", path, err)
	}
	defer file.Close()
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

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
