package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

// provisioningCommands are the local commands that touch the database and so
// should run as the service user when invoked with root.
var provisioningCommands = map[string]bool{
	"create-owner": true,
	"set-password": true,
	"reset-link":   true,
	"list-users":   true,
}

// reexecProvisioningAsService makes `sudo witmoot create-owner` use the same
// filesystem identity as the installed service. The child receives the
// already-resolved data directory so it does not need to read root-only config.
func reexecProvisioningAsService(args []string) (bool, int, error) {
	if os.Geteuid() != 0 || len(args) == 0 || !provisioningCommands[args[0]] || hasHelpFlag(args[1:]) {
		return false, 0, nil
	}
	commandName := args[0]
	paths := defaultProvisioningConfigPaths()
	username, groupName, managed, err := resolveProvisioningServiceAccount(paths)
	if err != nil {
		return true, 1, err
	}
	if !managed {
		return false, 0, nil
	}
	if username == "root" {
		return true, 1, fmt.Errorf("the configured Witmoot service user is root; %s refuses to write its database as root", commandName)
	}
	dataDir, err := resolveProvisioningDataDir(paths)
	if err != nil {
		return true, 1, err
	}

	account, err := user.Lookup(username)
	if err != nil {
		return true, 1, fmt.Errorf("look up Witmoot service user %q: %w", username, err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 {
		return true, 1, fmt.Errorf("Witmoot service user %q must have a non-root numeric UID", username)
	}
	gid, err := serviceGroupID(groupName, account.Gid)
	if err != nil {
		return true, 1, err
	}
	if gid == 0 {
		return true, 1, fmt.Errorf("Witmoot service group %q must have a non-root numeric GID", groupName)
	}
	groupIDs, err := account.GroupIds()
	if err != nil {
		return true, 1, fmt.Errorf("look up groups for Witmoot service user %q: %w", username, err)
	}
	groups := make([]uint32, 0, len(groupIDs))
	for _, id := range groupIDs {
		parsed, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			return true, 1, fmt.Errorf("invalid supplementary group ID %q for Witmoot service user %q", id, username)
		}
		if parsed == 0 {
			return true, 1, fmt.Errorf("Witmoot service user %q belongs to the root group", username)
		}
		groups = append(groups, uint32(parsed))
	}

	executable, err := os.Executable()
	if err != nil {
		return true, 1, fmt.Errorf("find Witmoot executable: %w", err)
	}
	command := exec.Command(executable, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.Env = withDataDirEnvironment(os.Environ(), "WITMOOT_DATA_DIR", dataDir)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
		Uid:    uint32(uid),
		Gid:    gid,
		Groups: groups,
	}}
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return true, exitError.ExitCode(), nil
		}
		return true, 1, fmt.Errorf("run %s as service user %q: %w", commandName, username, err)
	}
	return true, 0, nil
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func serviceGroupID(groupName, fallbackGID string) (uint32, error) {
	if groupName == "" {
		groupName = fallbackGID
	}
	group, err := user.LookupGroup(groupName)
	if err == nil {
		groupID, parseErr := strconv.ParseUint(group.Gid, 10, 32)
		if parseErr != nil {
			return 0, fmt.Errorf("invalid group ID %q for service group %q", group.Gid, groupName)
		}
		return uint32(groupID), nil
	}
	if groupID, parseErr := strconv.ParseUint(groupName, 10, 32); parseErr == nil {
		return uint32(groupID), nil
	}
	return 0, fmt.Errorf("look up service group %q: %w", groupName, err)
}

func withDataDirEnvironment(environment []string, name, value string) []string {
	prefix := name + "="
	filtered := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			filtered = append(filtered, item)
		}
	}
	return append(filtered, prefix+value)
}
