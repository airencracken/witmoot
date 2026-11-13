# Binary releases

Download Witmoot from [GitHub Releases](https://github.com/airencracken/witmoot/releases).
The latest release is [0.24.1](release-notes/0.24.1.md), with a tidier account page, site colors, and metrics that name nobody.
Read the [release notes](release-notes/) for every version you skip before
upgrading.

Each version has Linux **amd64** (x86-64) and **arm64** (AArch64) builds:

- `witmoot_VERSION_linux_ARCH.tar.gz`: a static binary, documentation,
  license notices, and service examples.
- `witmoot_VERSION_ARCH.deb`: a Debian/Ubuntu package with a systemd unit.
- `witmoot_VERSION_source.tar.gz`: the matching source.
- `witmoot_VERSION_checksums.txt`: SHA-256 checksums for all five files.

You need Go only to build from source. HTTPS connections to Imvault need your
system's CA certificates. To create the first account, see
[setting up Witmoot](setup.md).

## Debian and Ubuntu

Pick the version and architecture. For example:

```sh
release_version=0.24.1
release_arch=amd64
release_url="https://github.com/airencracken/witmoot/releases/download/v$release_version"
curl -fLO "$release_url/witmoot_${release_version}_${release_arch}.deb"
curl -fLO "$release_url/witmoot_${release_version}_checksums.txt"
sha256sum --ignore-missing -c "witmoot_${release_version}_checksums.txt"
sudo apt install "./witmoot_${release_version}_${release_arch}.deb"
```

Use `arm64` on an AArch64 machine. Install only if the checksum passes. The
package creates a `witmoot` account and `/var/lib/witmoot` with mode 0700,
installs `/usr/bin/witmoot`, and keeps settings in `/etc/witmoot/witmoot.env`,
readable only by root.

**The first install leaves the service stopped and disabled.** Edit the
configuration, then create the owner in a terminal and start the service. The
hidden prompt asks for the password twice:

```bash
sudo /usr/bin/witmoot create-owner \
	--username alex --password-prompt
systemctl enable --now witmoot
```

The service listens on `127.0.0.1:8082`. Set up an HTTPS reverse proxy and a
public HTTPS `WITMOOT_BASE_URL` before you open the site to others; see
[deployment](deployment.md). Logs go to `journalctl -u witmoot`.

An upgrade restarts a running service and leaves a stopped one stopped.
Startup can migrate the database, so back up first. Going back to an older
binary may mean restoring that backup. Debian treats the environment file as a
conffile and keeps your edits.

Removing the package stops and disables the service; purging also removes its
configuration. **Neither removes your data or the service account.** Delete
those yourself only when you mean to.

## Portable binary archive

Download the archive and checksum file for your architecture, check them as
above, and extract:

```sh
tar -xzf witmoot_0.24.1_linux_amd64.tar.gz
cd witmoot_0.24.1_linux_amd64
sudo install -m 0755 witmoot /usr/local/bin/witmoot
sudo install -d /usr/local/share/doc/witmoot
sudo install -m 0644 LICENSE README.md THIRD_PARTY_NOTICES.txt /usr/local/share/doc/witmoot/
```

To upgrade, install the binary where your service expects it and restart.
For a new install, create the service account and data directory as in
[deployment](deployment.md), then copy the systemd files:

```sh
sudo install -m 0644 contrib/systemd/witmoot.service /etc/systemd/system/witmoot.service
sudo install -d /etc/witmoot
sudo install -m 0600 contrib/systemd/witmoot.env /etc/witmoot/witmoot.env
sudo systemctl daemon-reload
```

Edit the environment file, create the owner, and enable the service. Skip
these copies on upgrade so your configuration stays. For OpenRC, the archive
has `contrib/openrc` and `contrib/logrotate`; set `WITMOOT_BIN` in
`/etc/conf.d/witmoot` to the installed binary.

## Logs

Debian packages and the systemd unit log to journald. Read them with
`journalctl -u witmoot`. On OpenRC, Gentoo's ebuild installs a logrotate rule
for `/var/log/witmoot.log`; see [logs and backups](deployment.md#logs-and-backups).

## Containers

See [Run Witmoot with Docker](containers.md).

## Building and publishing

GoReleaser **2.18.2** builds the release. The Makefile uses an installed
`goreleaser`, or runs the pinned version with `go run`, which needs network
access:

```sh
make release-check
make release-snapshot
sh scripts/release/check-artifacts.sh
```

Snapshots stay in `dist/` and are never published. The artifact check needs
`dpkg-deb` and `readelf` (Debian packages `dpkg` and `binutils`). It compares
the source archive with every tracked file, so commit your changes first.

Once the change is on `master`, tag it and push the tag:

```sh
git tag -a vX.Y.Z -m "witmoot X.Y.Z"
git push origin vX.Y.Z
```

The tagged commit must be on `master`. The workflow publishes to GitHub
Releases only after every check passes. A tag with a prerelease suffix makes a
prerelease. A rerun can finish a draft but never replaces a published release.

To rebuild an existing tag, run the Release workflow on `master` with
`release_tag` set to that tag.
