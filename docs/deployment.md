# A home for your board

[GitHub Releases](https://github.com/airencracken/witmoot/releases) has Linux
amd64 and arm64 archives and Debian packages; see
[binary releases](releases.md) to install without Go. This guide covers source
installs, owner setup, mail, proxies, logs and backups.

Witmoot runs as one unprivileged process listening on `127.0.0.1:8082`, which
leaves Imvault's usual 8080 free. Native services keep the database and
`imvault.key` in `/var/lib/witmoot`. The standalone binary uses `./data`.

## Source install on Gentoo / OpenRC

You need Go 1.26.11 or later, Make, `app-admin/logrotate` and
`app-misc/ca-certificates`. The first build downloads the pinned Go modules.
As root, from the source checkout, create the service account once, then
install:

```sh
groupadd --system witmoot
useradd --system --gid witmoot --home-dir /var/lib/witmoot \
	--shell "$(command -v nologin)" witmoot
install -d -m 0700 -o witmoot -g witmoot /var/lib/witmoot
make install install-openrc
```

`make install` puts the binary in `/usr/local/bin/witmoot`. `install-openrc`
adds the init script, `/etc/conf.d/witmoot` and the logrotate rule. Use
`PREFIX=/usr` to install under `/usr`. Reinstalling updates the binary and init
script and keeps your configuration. It does not enable or restart the
service. Packagers can set `DESTDIR`, `PREFIX`, `SYSCONFDIR`, `UNITDIR` and
`LOGROTATEDIR`.

Edit `/etc/conf.d/witmoot` for your HTTPS hostname:

```sh
WITMOOT_BASE_URL="https://board.example.org"
WITMOOT_SECURE_COOKIES="true"
WITMOOT_TRUSTED_PROXIES="127.0.0.1/32,::1/128"
# Optional; use your Imvault server's public, canonical URL.
WITMOOT_IMVAULT_URL="https://photos.example.org"
# Optional mail relay for reset links; leave unset to disable sending.
# WITMOOT_SMTP_HOST="smtp.example.org"
```

Create the owner as described below, then start the board:

```sh
rc-update add witmoot default
rc-service witmoot start
curl --fail http://127.0.0.1:8082/healthz
```

Run `rc-service witmoot restart` after changing the binary or configuration.
On SIGTERM, Witmoot gives active requests ten seconds to finish.

## Provision the owner

Run this in a terminal. The hidden prompt asks for the password twice:

```bash
sudo /usr/local/bin/witmoot create-owner \
	--username alex --password-prompt
```

Use `/usr/bin/witmoot` for the Gentoo package or `PREFIX=/usr`. The command
reads `WITMOOT_DATA_DIR` from the active OpenRC or systemd configuration. Run
as root, it does the database work as the service account so file ownership
stays right. If OpenRC and systemd point at different data directories, set
`WITMOOT_DATA_DIR` yourself.

Passwords need at least 12 characters and at most 72 bytes. New boards start in Private mode.
For scripts, pass one password line on stdin with `--password-stdin`. For
browser setup, see [setting up Witmoot](setup.md).

## Passwords and account recovery

Owners create a single-use reset link for any account under **Members**. It
shows once in a read-only field with a **Copy link** button and expires after
24 hours. The member opens it and chooses a new password. **Cancel reset link**
revokes it. Members change their own password under **Sign-in** on the
account page, which ends their other sessions.

From the server:

```bash
sudo /usr/local/bin/witmoot set-password --username freya --password-prompt
sudo /usr/local/bin/witmoot reset-link --username freya --expires 48h
sudo /usr/local/bin/witmoot list-users
sudo /usr/local/bin/witmoot admin
```

- `set-password` replaces the password and signs the account out everywhere.
- `reset-link` prints a link and its code.
- `list-users` prints each account's ID, username, role and email address.
- `admin` opens an interactive manager and needs a terminal.

These commands read `WITMOOT_DATA_DIR`, `WITMOOT_BASE_URL`, `WITMOOT_NAME` and
the `WITMOOT_SMTP_*` settings from the service configuration unless the
environment sets them. Set `WITMOOT_BASE_URL` so printed links are complete;
`admin` only emails complete links.

## Mail

Mail is optional. With an SMTP relay, an owner can email a reset link to a
member who has saved an address:

```sh
WITMOOT_SMTP_HOST="smtp.example.org"
WITMOOT_SMTP_PORT="587"
WITMOOT_SMTP_USERNAME="witmoot"
WITMOOT_SMTP_PASSWORD="…"
WITMOOT_SMTP_FROM="Witmoot <no-reply@example.org>"
WITMOOT_SMTP_TLS="starttls"
```

Without `WITMOOT_SMTP_FROM`, mail comes from `no-reply@` the
`WITMOOT_BASE_URL` domain, with `WITMOOT_NAME` as the display name. With
neither, startup stops with an error.

`starttls` is the default and refuses to send if the relay does not offer
TLS. Use `implicit` for port 465 and `none` only for a trusted local relay.
Any other value stops startup. Without `WITMOOT_SMTP_HOST`, owners copy reset
links by hand.

The site name becomes the email subject, so the site name and board names
must each fit on one line. A member's address must be one bare address with
no display name or comma.

## Site identity and invitations

Under **Settings**, owners set the site name, welcome heading and text, and
footer source link, and upload a favicon or mascot. Images can be PNG, JPEG
or GIF up to 2 MiB; Witmoot converts them to PNG. A custom favicon replaces
the little Moot Knight. `WITMOOT_SOURCE_URL` sets the footer link until an
owner saves another one.

On the **Invites** page, owners can let individual members invite people.
Those members see and revoke only their own invitations. A new invitation
link and code show once in read-only fields with Copy buttons. Owners see who
invited each new account. New accounts start as members without invite
permission.

The **Server** section at the bottom of **Settings** shows the running
version, a link to [instance metrics](#instance-metrics), and automatic backup
status.

## Instance metrics

Owners open `/admin/metrics` from **Settings**. It shows counts of members,
boards, conversations and posts, new posts in the past seven UTC days,
and the database size. **Site health** shows requests, errors, response times
and data served per route, and **Security** counts failed sign-ins,
rate-limited attempts and refused invitation or reset links. Automatic backups
and configured apps appear below. Witmoot does not record who signs in or what
anyone reads.

## Gentoo ebuild

`contrib/gentoo` has a live `witmoot-9999.ebuild` and service-account
packages. It builds `master` and vendors Go modules during unpack, so the
compile step needs no network. Add it to a local overlay as root from the
source checkout:

```sh
install -d /var/db/repos/witmoot-local/{profiles,metadata,www-apps/witmoot}
printf 'witmoot-local\n' > /var/db/repos/witmoot-local/profiles/repo_name
printf 'masters = gentoo\nthin-manifests = true\n' > /var/db/repos/witmoot-local/metadata/layout.conf
cp contrib/gentoo/witmoot-9999.ebuild contrib/gentoo/metadata.xml \
	/var/db/repos/witmoot-local/www-apps/witmoot/
cp -R contrib/gentoo/acct-user contrib/gentoo/acct-group /var/db/repos/witmoot-local/
install -d /etc/portage/repos.conf
cat > /etc/portage/repos.conf/witmoot-local.conf <<'EOF'
[witmoot-local]
location = /var/db/repos/witmoot-local
masters = gentoo
auto-sync = no
EOF
emerge --ask --autounmask www-apps/witmoot
```

The example needs Bash for brace expansion. Review the keyword and license
changes Portage proposes. Witmoot is AGPL-3.0-or-later (`AGPL-3+`). The `test`
USE flag runs the Go and packaging tests.

Portage creates the account and data directory and installs
`/usr/bin/witmoot`, both service definitions and the logrotate rule. Then
configure, create the owner, and enable your service as above.

## Caddy and Imvault

**Caddy is the recommended reverse proxy.** For nginx and Apache 2.4, see
[HTTPS reverse proxies](reverse-proxies.md).

Point your board's hostname at the host and add a Caddy site:

```caddyfile
board.example.org {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8082
}
```

A copy lives in `contrib/caddy/Caddyfile`. Use the same hostname in Caddy and
`WITMOOT_BASE_URL`, validate, and reload Caddy. Keep port 8082 on loopback.
See [running beside Imvault](imvault.md#running-beside-imvault) for proxy
trust and image connections. Witmoot does not need ffmpeg.

## Logs and backups

OpenRC writes stdout and stderr to `/var/log/witmoot.log`. The shipped rule
rotates daily, or above 10 MiB when checked, and keeps 14 compressed archives.
Make sure logrotate's cron job or timer runs; the rule alone schedules
nothing. It uses `copytruncate`, so a few lines can be lost at rotation. If
you change `WITMOOT_LOG_FILE`, update `/etc/logrotate.d/witmoot` too. Check
the rule with `logrotate --debug /etc/logrotate.d/witmoot`.

The service also makes [automatic backups](automatic-backups.md). For a full
backup, stop Witmoot and copy the **entire** data directory, including
`imvault.key`. Restore with the service stopped and keep ownership and private
permissions. Back up Imvault separately.

To upgrade, take that backup, install the new binary and restart. Startup runs
schema migrations. First it saves a private database snapshot in a
`witmoot-before-vN-*` directory beside the database, and refuses to upgrade if
that fails. The snapshot leaves out `imvault.key`, so keep the full backup too.

Account commands refuse an outdated schema. To migrate before starting the
server, stop it and run `witmoot migrate`. Stop older versions before running
new commands. Going back to an older binary may mean restoring the pre-upgrade
backup.

## systemd

Run `make install install-systemd`, create the service account and data
directory as above, and edit `/etc/witmoot/witmoot.env`. Create the owner,
then:

```sh
systemctl daemon-reload
systemctl enable --now witmoot
journalctl -u witmoot
```

Logs go to journald, so the logrotate rule is only for OpenRC. The unit makes
the filesystem read-only. A custom data directory needs a unit override with
`ReadWritePaths=/your/path`; create the directory with mode 0700 and the
service account as owner before you create the owner.

## Docker / Compose

See [Run Witmoot with Docker](containers.md) for images, version pins,
backups, upgrades and restores. In short:

```bash
docker compose pull
docker compose run --rm --no-deps witmoot \
	create-owner --username alex --password-prompt
docker compose up -d
docker compose logs -f witmoot
```

Open <http://localhost:8082> to try it. For HTTPS, put `WITMOOT_BASE_URL`,
`WITMOOT_SECURE_COOKIES="true"` and optionally `WITMOOT_IMVAULT_URL` in a
local `.env` file. `WITMOOT_NAME`, `WITMOOT_SOURCE_URL` and `WITMOOT_PORT` go
there too. Run `docker compose up -d` to apply changes. Imvault is not part of
the Compose file; point at your existing server.

Witmoot listens on 8080 inside the container. A proxy reaching it through
Docker shows up as a Docker network address, not host loopback. Set
`WITMOOT_TRUSTED_PROXIES` to the proxy's address or a network that holds only
trusted proxies. If Caddy runs in a container too, put both on one dedicated
network and proxy to `witmoot:8080`.

## Configuration format

Quote every value, numbers and booleans included. Leave out `export`:

```sh
WITMOOT_BASE_URL="https://witmoot.example.org"
WITMOOT_BACKUP_KEEP="7"
```

OpenRC reads `/etc/conf.d/witmoot` and systemd reads
`/etc/witmoot/witmoot.env`. Edit the one for the service manager you run, then
restart. Bare and single-quoted values still work, and upgrades keep your
settings. The
[shared configuration convention](https://github.com/airencracken/comfylib/blob/master/docs/configuration.md)
covers quoting and escaping.

For optional Bubblewrap confinement, see [Linux confinement](sandbox.md).
