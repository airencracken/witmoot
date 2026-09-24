# A home for your board

[GitHub Releases](https://github.com/airencracken/witmoot/releases) provide
Linux amd64/arm64 binary archives and Debian packages. See [binary releases](releases.md)
for installation without Go, checksums, and package upgrade behavior.

Witmoot runs as one unprivileged process. Native service packages listen on
`127.0.0.1:8082`, leaving imvault's usual port free, and keep the database and
optional imvault encryption key in `/var/lib/witmoot`. The standalone binary
still defaults to `127.0.0.1:8080` and `./data`.

## Source install on Gentoo / OpenRC

From the source checkout, create the service account first, then install as
root. You need Go 1.26 or later, Make, and the normal install tools; the first
build downloads the pinned Go dependencies. Install `app-admin/logrotate` and
`app-misc/ca-certificates` through Portage too.

```sh
groupadd --system witmoot
useradd --system --gid witmoot --home-dir /var/lib/witmoot \
	--shell "$(command -v nologin)" witmoot
install -d -m 0700 -o witmoot -g witmoot /var/lib/witmoot
make install install-openrc
```

Create the account only once. `make install` installs `/usr/local/bin/witmoot`;
`install-openrc` installs the init script, `/etc/conf.d/witmoot`, and the
logrotate rule. It sets the service's binary path to match `PREFIX`. To install
under `/usr` instead, use `make install install-openrc PREFIX=/usr`.

Reinstalling updates the binary and service script while preserving existing
configuration and rotation rules, including symlinks. It does not enable or
restart the service. Packaging supports `DESTDIR`, `PREFIX`, `SYSCONFDIR`,
`UNITDIR`, and `LOGROTATEDIR`; `DESTDIR` never becomes part of runtime paths.

Edit `/etc/conf.d/witmoot` for your HTTPS hostname:

```sh
WITMOOT_BASE_URL="https://board.example.org"
WITMOOT_SECURE_COOKIES="true"
WITMOOT_TRUSTED_PROXIES="127.0.0.1/32,::1/128"
# Optional; use your imvault server's public, canonical URL.
WITMOOT_IMVAULT_URL="https://img.internetrelay.chat"
```

Provision the owner as described below, then start the board:

```sh
rc-update add witmoot default
rc-service witmoot start
curl --fail http://127.0.0.1:8082/healthz
```

Use `rc-service witmoot restart` after binary or configuration changes.
Witmoot handles SIGTERM and has ten seconds to finish active requests.

## Provision the owner

The command reads `WITMOOT_DATA_DIR` from the active OpenRC or systemd service
configuration when the environment variable is unset. You can invoke it as root;
it repeats the database operation as the configured service account so new
database files keep the right ownership. The hidden prompt asks for the password
twice and needs a terminal:

```bash
sudo /usr/local/bin/witmoot create-owner \
	--username alex --password-prompt
```

For the Gentoo package or `PREFIX=/usr`, use `/usr/bin/witmoot`. If OpenRC and
systemd are both installed but configure different data directories, run under
the active service manager or set `WITMOOT_DATA_DIR` explicitly. Passwords need
at least 12 characters and at most 72 bytes. New boards start in Private mode;
public registration never provisions an owner. For scripts, pass one password
line on stdin with `--password-stdin`.

## Site identity and invitations

Owners can set the site name, footer source link, welcome heading and text, and
upload a favicon or mascot under **Settings**. Brand images accept PNG, JPEG, or
GIF up to 2 MiB; Witmoot converts them to PNG and serves them from the same
origin. The source link defaults to the Witmoot repository and can be changed
or hidden in Settings. `WITMOOT_SOURCE_URL` sets the default used before an
owner saves an override.

Owners can grant individual members permission to issue invitations on the
**Invites** page. Members with that permission see and revoke only the
invitations they issued. New accounts record the issuer and invitation used;
owners can review that attribution on the same page. New accounts remain
members and do not inherit invitation permission.

## Gentoo ebuild

`contrib/gentoo` includes a live `witmoot-9999.ebuild` and service-account
packages. It builds `master`, vendors the pinned Go modules during unpack,
then compiles without network access. Install it in a local overlay, for
example from the source checkout as root:

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

The example uses Bash brace expansion. Review Portage's keyword/license
changes for the live package and its account packages. Witmoot uses
AGPL-3.0-or-later, recorded as `AGPL-3+` alongside the dependency licenses in
the ebuild. The optional `test` USE flag runs the Go and packaging
tests; browser tests remain a separate development check.

Portage creates the account and private data directory, installs
`/usr/bin/witmoot`, both service definitions, and the rotation rule. Follow the
configuration and owner setup above, then enable your chosen service. Use
Portage's configuration-update tools when it offers changes to `/etc` files.

## Caddy and imvault

**Caddy is the recommended reverse proxy.** nginx and Apache 2.4 are also
supported; see [HTTPS reverse proxies](reverse-proxies.md) for their complete
examples, certificate setup, upload limits, and validation commands.

Point your board's DNS hostname at the host and add a separate Caddy site:

```caddyfile
board.example.org {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8082
}
```

A copy lives in `contrib/caddy/Caddyfile`. Replace the hostname in both Caddy
and `WITMOOT_BASE_URL`, validate your Caddy configuration, and reload Caddy.
Keep the Witmoot port on loopback. See [running beside imvault](imvault.md#running-beside-imvault)
for proxy trust, image access, and the separate account connections. Witmoot
does not need ffmpeg; imvault handles media processing.

## Logs and backups

OpenRC redirects stdout/stderr to `/var/log/witmoot.log`. The shipped rule
rotates daily or when the log exceeds 10 MiB at a scheduled check, retaining
14 archives with delayed compression. Ensure logrotate's cron job or timer
actually runs; installing the rule alone does not schedule rotation.

`copytruncate` keeps the service's open log descriptor usable without a
restart. A small amount of output can be lost between copying and truncating.
If you change `WITMOOT_LOG_FILE`, update `/etc/logrotate.d/witmoot` as well.
Validate the rule with `logrotate --debug /etc/logrotate.d/witmoot`.

Stop Witmoot before copying the **entire** data directory, including
`imvault.key` when present. Restore with the service stopped and preserve
ownership and private permissions. Back up imvault separately. For an upgrade,
take that backup, install the new binary, and restart; schema migrations run
at startup. An older binary may require restoring the pre-upgrade backup.

## systemd

Use `make install install-systemd`, create the service account and private data
directory as above, and configure `/etc/witmoot/witmoot.env`. Provision the
owner using the same data directory, then run:

```sh
systemctl daemon-reload
systemctl enable --now witmoot
journalctl -u witmoot
```

The unit logs to the journal, whose retention is managed by journald. The file
rotation rule is only needed for OpenRC. Native services default to loopback
port 8082 under either init system. A custom data directory also needs a unit
override with `ReadWritePaths=/your/path` because the service filesystem is
otherwise read-only. Create that directory with mode 0700 and the service's
ownership before provisioning the owner.

## Docker / Compose

The image runs as UID/GID 10001 with CA certificates for HTTPS connections.
Compose publishes **only** `127.0.0.1:8082`, persists `/data` in a named volume,
keeps upload scratch space in a bounded `/tmp`, and limits container log files.
There is no imvault container dependency; point at your existing server if
you want images.

```bash
docker compose build
docker compose run --rm --no-deps witmoot \
	create-owner --username alex --password-prompt
docker compose up -d
docker compose logs -f witmoot
```

Open <http://localhost:8082> for a local trial. For HTTPS hosting, put
`WITMOOT_BASE_URL`, `WITMOOT_SECURE_COOKIES=true`, and optionally
`WITMOOT_IMVAULT_URL` in a local `.env` file. `WITMOOT_NAME`,
`WITMOOT_SOURCE_URL`, and `WITMOOT_PORT`
are configurable there too. Run `docker compose up -d` to apply changes.

When proxying into Docker, the peer seen by Witmoot is usually a Docker network
address, not host loopback. Set `WITMOOT_TRUSTED_PROXIES` to the actual proxy
address or a dedicated network containing only trusted proxies. Do not copy
the native loopback trust setting or trust every address. If Caddy itself runs
in a container, put both services on a dedicated network and proxy to
`witmoot:8080` instead of `127.0.0.1:8082`.

Named volumes acquire the image's data-directory ownership on first use. If
replacing it with a bind mount, create that directory as UID/GID 10001, mode
0700, before starting. Keep the volume when recreating the container; `docker
compose down --volumes` deletes the board. Stop the service before backing up
the entire volume.

The container configuration follows the [Compose service reference](https://docs.docker.com/reference/compose-file/services/);
the native init script uses [OpenRC's standard service functions](https://github.com/OpenRC/openrc/blob/master/service-script-guide.md).
