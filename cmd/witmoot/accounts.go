package main

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/airencracken/comfylib/smtp"
	"github.com/airencracken/comfylib/svcconfig"
	"github.com/airencracken/comfylib/token"
	"golang.org/x/term"

	"witmoot/internal/admin"
	"witmoot/internal/forum"
)

// commandSummary describes each command for its --help output.
var commandSummary = map[string]string{
	"version":      "Print this binary's build version without opening a database.",
	"migrate":      "Back up and migrate an existing database. Stop the server first. Account commands never migrate.",
	"sandbox":      "Start the server confined by Bubblewrap. Run as the service user.\nThe data directory must already exist; see docs/sandbox.md.",
	"serve":        "Start the HTTP server using WITMOOT_* variables; see witmoot --help for defaults.",
	"create-owner": "Create a new owner locally. Existing accounts are never promoted or changed.\nWITMOOT_DATA_DIR is read from the active service configuration unless set in the environment. Root invocations use the configured service user. Use a hidden terminal prompt or read from stdin.",
	"set-password": "Replace an account's password and end its signed-in sessions.\nThis is the forced reset for someone who cannot use a one-time link. Root invocations use the configured service user.",
	"reset-link":   "Make a single-use link an account can open to choose its own password.\nThe link expires (" + forum.HumanDuration(forum.ResetLinkTTL) + " by default) and is shown once. WITMOOT_BASE_URL is read from the\nactive service configuration unless set in the environment. Root invocations use the configured service user.",
	"list-users":   "List every account in the local database with its role and email address.",
	"admin":        "Open an interactive manager for accounts: browse members, create owners,\nissue or cancel reset links, and set passwords. It needs an interactive terminal;\nuse set-password, reset-link, or list-users for scripts. Site and mail settings\nare read from the active service configuration unless set in the environment.",
}

// instanceSettings are the service settings reset links and mail depend on.
// Account commands resolve them like WITMOOT_DATA_DIR, so a link printed or
// mailed from the command line matches one made in the web interface.
var instanceSettings = []string{
	"WITMOOT_BASE_URL", "WITMOOT_NAME", "WITMOOT_SMTP_HOST", "WITMOOT_SMTP_PORT",
	"WITMOOT_SMTP_USERNAME", "WITMOOT_SMTP_PASSWORD", "WITMOOT_SMTP_FROM", "WITMOOT_SMTP_TLS",
}

// openProvisioningStore opens the database in the resolved data directory. It
// refuses to create one, so a mistyped command cannot leave an empty board
// behind; start or provision the board first.
func openProvisioningStore(paths svcconfig.Paths) (*forum.Store, error) {
	dataDir, err := paths.DataDir("WITMOOT_DATA_DIR")
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "witmoot.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("no Witmoot database at %s; start or provision the board first", dbPath)
	}
	return forum.OpenCurrentStore(dbPath)
}

// accountByUsername looks up one account, with a friendly error when it is
// missing rather than a raw SQL result.
func accountByUsername(ctx context.Context, store *forum.Store, name string) (forum.User, error) {
	user, _, err := store.Credentials(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return forum.User{}, fmt.Errorf("no account named %q", name)
	}
	return user, err
}

func setPassword(args []string, stdin io.Reader, stdout io.Writer) error {
	return setPasswordWithConfigPaths(args, stdin, stdout, servicePaths())
}

func setPasswordWithConfigPaths(args []string, stdin io.Reader, stdout io.Writer, paths svcconfig.Paths) error {
	flags := commandFlags("set-password", stdout)
	username := flags.String("username", "", "account username (required)")
	passwordPrompt := flags.Bool("password-prompt", false, "prompt twice without echoing (requires a terminal)")
	passwordStdin := flags.Bool("password-stdin", false, "read the new password from standard input")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*username) == "" || *passwordPrompt == *passwordStdin {
		return errors.New("usage: witmoot set-password --username NAME (--password-prompt | --password-stdin)")
	}
	password, err := readProvisionedPassword(*passwordPrompt, stdin)
	if err != nil {
		return err
	}
	if message := forum.ValidatePassword(password); message != "" {
		return errors.New(message)
	}
	store, err := openProvisioningStore(paths)
	if err != nil {
		return err
	}
	defer closeStore(store)
	user, err := accountByUsername(context.Background(), store, strings.TrimSpace(*username))
	if err != nil {
		return err
	}
	hash, err := forum.HashPassword(password)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := store.SetPassword(ctx, user.ID, hash); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Password updated for %q. Any signed-in sessions were ended.\n", user.Username)
	return err
}

func resetLink(args []string, stdout io.Writer) error {
	return resetLinkWithConfigPaths(args, stdout, servicePaths())
}

func resetLinkWithConfigPaths(args []string, stdout io.Writer, paths svcconfig.Paths) error {
	flags := commandFlags("reset-link", stdout)
	username := flags.String("username", "", "account username (required)")
	expires := flags.Duration("expires", forum.ResetLinkTTL, "how long the link stays valid, for example 48h")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*username) == "" {
		return errors.New("usage: witmoot reset-link --username NAME [--expires 24h]")
	}
	if *expires < time.Minute || *expires > 365*24*time.Hour {
		return errors.New("--expires must be between 1 minute and 365 days")
	}
	settings, err := paths.Settings("WITMOOT_BASE_URL")
	if err != nil {
		return err
	}
	base, err := forum.CanonicalBaseURL(settings["WITMOOT_BASE_URL"])
	if err != nil {
		return err
	}
	store, err := openProvisioningStore(paths)
	if err != nil {
		return err
	}
	defer closeStore(store)
	user, err := accountByUsername(context.Background(), store, strings.TrimSpace(*username))
	if err != nil {
		return err
	}
	code, hash := token.New()
	if err := store.CreateAuthToken(context.Background(), user.ID, forum.TokenPasswordReset, hash, time.Now().Add(*expires)); err != nil {
		return err
	}
	path := base + "/reset/" + code
	if _, err := fmt.Fprintf(stdout, "Reset link for %q (works once, expires in %s):\n  %s\n\nReset code, if the link is hard to share:\n  %s\n", user.Username, *expires, path, code); err != nil {
		return err
	}
	if base == "" {
		_, err = fmt.Fprintln(stdout, "\nSet WITMOOT_BASE_URL to print a complete link.")
	}
	return err
}

func listUsers(args []string, stdout io.Writer) error {
	return listUsersWithConfigPaths(args, stdout, servicePaths())
}

func listUsersWithConfigPaths(args []string, stdout io.Writer, paths svcconfig.Paths) error {
	flags := commandFlags("list-users", stdout)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: witmoot list-users")
	}
	store, err := openProvisioningStore(paths)
	if err != nil {
		return err
	}
	defer closeStore(store)
	members, err := store.Members(context.Background())
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "ID\tUSERNAME\tROLE\tEMAIL\tSTATUS"); err != nil {
		return err
	}
	for _, member := range members {
		email := member.Email
		if email == "" {
			email = "—"
		}
		status := "active"
		if member.Suspended {
			status = "suspended"
		}
		if _, err := fmt.Fprintf(table, "%d\t%s\t%s\t%s\t%s\n", member.ID, member.Username, member.Role, email, status); err != nil {
			return err
		}
	}
	return table.Flush()
}

// runAdmin opens the interactive account manager. It shares the same store
// operations as the account commands, so the two cannot disagree about what a
// reset link or a forced password change does.
func runAdmin(args []string, stdout io.Writer) error {
	flags := commandFlags("admin", stdout)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: witmoot admin")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("witmoot admin needs an interactive terminal; use set-password, reset-link, or list-users for scripts")
	}
	paths := servicePaths()
	options, err := adminOptions(paths)
	if err != nil {
		return err
	}
	store, err := openProvisioningStore(paths)
	if err != nil {
		return err
	}
	defer closeStore(store)
	brand, err := store.LoadBranding(context.Background(), forum.SiteBranding{Name: options.SiteName})
	if err != nil {
		return err
	}
	options.SiteName = brand.Name
	return admin.Run(store, options)
}

// adminOptions resolves the site and mail settings the admin view uses the
// same way the service does.
func adminOptions(paths svcconfig.Paths) (admin.Options, error) {
	settings, err := paths.Settings(instanceSettings...)
	if err != nil {
		return admin.Options{}, err
	}
	lookup := func(key string) string { return settings[key] }
	base, err := forum.CanonicalBaseURL(lookup("WITMOOT_BASE_URL"))
	if err != nil {
		return admin.Options{}, err
	}
	siteName := cmp.Or(lookup("WITMOOT_NAME"), "Witmoot")
	if err := forum.CheckSiteName(siteName); err != nil {
		return admin.Options{}, err
	}
	var sender smtp.Sender = smtp.Disabled{}
	mailSettings, err := mailConfig(lookup)
	if err != nil {
		return admin.Options{}, err
	}
	if mailSettings.Host != "" {
		if sender, err = smtp.New(mailSettings); err != nil {
			return admin.Options{}, err
		}
	}
	return admin.Options{BaseURL: base, SiteName: siteName, Mailer: sender}, nil
}

// closeStore closes the database after a command; a failure is reported but
// cannot change the command's outcome any more.
func closeStore(store *forum.Store) {
	if err := store.Close(); err != nil {
		slog.Error("close database", "error", err)
	}
}

// readProvisionedPassword reads a password the way create-owner and
// set-password both expect: either a hidden terminal prompt or one stdin line.
func readProvisionedPassword(prompt bool, stdin io.Reader) (string, error) {
	if prompt {
		return readPromptPassword()
	}
	password, err := io.ReadAll(io.LimitReader(stdin, 1025))
	if err != nil {
		return "", err
	}
	if len(password) > 1024 {
		return "", errors.New("password input is too long")
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r"), nil
}
