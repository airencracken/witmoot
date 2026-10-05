// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"github.com/airencracken/comfylib/privdrop"
	"github.com/airencracken/comfylib/svcconfig"
)

// provisioningCommands are the local commands that touch the database and so
// should run as the service user when invoked with root.
var provisioningCommands = map[string]bool{
	"create-owner": true,
	"set-password": true,
	"reset-link":   true,
	"list-users":   true,
	"admin":        true,
	"migrate":      true,
}

// serviceUser is the account the packages create, and the OpenRC account when
// /etc/conf.d/witmoot does not set WITMOOT_USER.
const serviceUser = "witmoot"

// servicePaths finds the installed service's configuration, if any.
func servicePaths() svcconfig.Paths {
	return svcconfig.Detect("witmoot", "/var/lib/witmoot")
}

// provisioningRequest makes `sudo witmoot create-owner` use the same
// filesystem identity as the installed service. The child receives the
// already-resolved data directory, and the settings below, so it does not
// need to read root-only configuration.
func provisioningRequest(args []string, paths svcconfig.Paths) privdrop.Request {
	return privdrop.Request{
		Args:        args,
		Commands:    provisioningCommands,
		Paths:       paths,
		DefaultUser: serviceUser,
		Settings:    provisioningChildSettings(paths),
	}
}

// provisioningChildSettings resolves, for the commands that make reset links
// or send mail, the site and mail settings the service account usually cannot
// read from the root-only configuration.
func provisioningChildSettings(paths svcconfig.Paths) func(command, dataDir string) (map[string]string, error) {
	return func(command, _ string) (map[string]string, error) {
		if command != "reset-link" && command != "admin" {
			return nil, nil
		}
		return paths.Settings(instanceSettings...)
	}
}
