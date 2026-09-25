package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"witmoot/contrib"
	"witmoot/internal/forum"
)

const commandHelp = `witmoot - a little bulletin board for your people

Usage: witmoot [COMMAND] [OPTIONS]

Commands:
  serve          Start the HTTP server (also the default with no arguments).
  create-owner   Provision a new owner using a hidden prompt or stdin.
  set-password   Replace an account's password and end its sessions.
  reset-link     Make a single-use link an account can use to choose a password.
  list-users     List local accounts with their roles and email addresses.
  admin          Open an interactive manager for accounts (needs a terminal).
  proxy-config   Print a Caddy, nginx, or Apache HTTPS site configuration.
  help [COMMAND] Show help; COMMAND --help also works.

Server configuration uses environment variables:
  WITMOOT_ADDR            Listen address (binary default: 127.0.0.1:8080).
  WITMOOT_DATA_DIR        Database directory (default: ./data).
  WITMOOT_NAME            Board name (default: Witmoot).
  WITMOOT_SOURCE_URL      Footer source link (default: upstream repository).
  WITMOOT_BASE_URL        Public origin, for example https://board.example.org.
  WITMOOT_SECURE_COOKIES  Set true for HTTPS (default: false).
  WITMOOT_TRUSTED_PROXIES Comma-separated proxy IPs/CIDRs; empty trusts none.
  WITMOOT_IMVAULT_URL     Optional HTTPS origin of your Imvault image host.
  WITMOOT_SMTP_HOST       Optional SMTP relay; empty leaves mail disabled.
  WITMOOT_SMTP_PORT       Relay port (default: 587).
  WITMOOT_SMTP_USERNAME   Relay username, if it needs one.
  WITMOOT_SMTP_PASSWORD   Relay password, if it needs one.
  WITMOOT_SMTP_FROM       From address, for example Witmoot <no-reply@example.org>.
  WITMOOT_SMTP_TLS        starttls (default), implicit, or none.

Native services and proxy examples use 127.0.0.1:8082 by default.
Service settings: /etc/conf.d/witmoot (OpenRC), /etc/witmoot/witmoot.env (systemd).
Account commands read WITMOOT_DATA_DIR from the active service configuration when
it is not set in the environment. When run as root, they repeat the database
operation as the service user to preserve ownership.

Examples:
  WITMOOT_ADDR=127.0.0.1:8082 WITMOOT_DATA_DIR=/var/lib/witmoot witmoot serve
  witmoot create-owner --username alex --password-prompt
  witmoot create-owner --username alex --password-stdin < password-file
  witmoot set-password --username freya --password-prompt
  witmoot reset-link --username freya --expires 48h
  witmoot proxy-config caddy --domain board.example.org > witmoot.Caddyfile

Logs: stdout/stderr; systemd uses journald, OpenRC uses /var/log/witmoot.log.
OpenRC packages include /etc/logrotate.d/witmoot; a cron job or timer runs rotation.
More settings and deployment examples: docs/deployment.md and docs/reverse-proxies.md.`

func runCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return runServer()
	}
	switch args[0] {
	case "serve":
		return serve(args[1:], stdout)
	case "create-owner":
		return createOwner(args[1:], stdin, stdout)
	case "set-password":
		return setPassword(args[1:], stdin, stdout)
	case "reset-link":
		return resetLink(args[1:], stdout)
	case "list-users":
		return listUsers(args[1:], stdout)
	case "admin":
		return runAdmin(args[1:], stdout)
	case "proxy-config":
		return proxyconfig.Run(args[1:], stdout)
	case "help":
		if len(args) == 2 && (commandSummary[args[1]] != "" || args[1] == "proxy-config") {
			return runCommand([]string{args[1], "--help"}, stdin, stdout)
		}
		if len(args) != 1 {
			return errors.New("usage: witmoot help [COMMAND]; use witmoot --help for commands")
		}
		fallthrough
	case "--help", "-h":
		if len(args) != 1 {
			return errors.New("use witmoot help COMMAND for command-specific help")
		}
		_, err := fmt.Fprintln(stdout, commandHelp)
		return err
	default:
		return fmt.Errorf("unknown command %q; use witmoot --help", args[0])
	}
}

func serve(args []string, out io.Writer) error {
	flags := commandFlags("serve", out)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected server arguments; use witmoot serve --help")
	}
	return runServer()
}

func commandFlags(command string, out io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("witmoot "+command, flag.ContinueOnError)
	flags.SetOutput(out)
	flags.Usage = func() {
		fmt.Fprintf(out, "Usage: witmoot %s [OPTIONS]\n\n", command)
		fmt.Fprintln(out, commandSummary[command])
		fmt.Fprintln(out)
		flags.PrintDefaults()
	}
	return flags
}

func createOwner(args []string, stdin io.Reader, stdout io.Writer) error {
	return createOwnerWithConfigPaths(args, stdin, stdout, defaultProvisioningConfigPaths())
}

func createOwnerWithConfigPaths(args []string, stdin io.Reader, stdout io.Writer, paths provisioningConfigPaths) error {
	flags := commandFlags("create-owner", stdout)
	username := flags.String("username", "", "owner username (required)")
	passwordPrompt := flags.Bool("password-prompt", false, "prompt twice without echoing (requires a terminal)")
	passwordStdin := flags.Bool("password-stdin", false, "read the password from standard input")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *username == "" || *passwordPrompt == *passwordStdin {
		return errors.New("usage: witmoot create-owner --username NAME (--password-prompt | --password-stdin)")
	}
	value, err := readProvisionedPassword(*passwordPrompt, stdin)
	if err != nil {
		return err
	}
	if message := forum.ValidateCredentials(*username, value); message != "" {
		return errors.New(message)
	}
	hash, err := forum.HashPassword(value)
	if err != nil {
		return err
	}
	dataDir, err := resolveProvisioningDataDir(paths)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	store, err := forum.OpenStore(filepath.Join(dataDir, "witmoot.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.CreateOwner(context.Background(), *username, hash); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Owner created. Start Witmoot, sign in, and invite your people.")
	return err
}
