# Run Witmoot with Docker

Images are published at `ghcr.io/airencracken/witmoot:v0.24.0` for Linux
amd64 and arm64. Docker picks the right architecture. Each image holds the
same static binary as the native release, CA certificates, timezone data, and
zstd for automatic backups.

## Start an instance

Download `docker-compose.yml` from the matching release's source archive or
repository tag, then run from its directory:

```sh
docker compose pull
docker compose up -d
docker compose exec witmoot create-owner --username owner --password-prompt
```

Open `http://localhost:8082`. Witmoot listens on port 8080 inside the
container, and Compose publishes it only on host loopback `127.0.0.1:8082`.
Set `WITMOOT_PORT` in `.env` to use another host port.

To reach it from another computer, put a reverse proxy in front and set
`WITMOOT_BASE_URL` to your public HTTPS origin; that also turns on secure
cookies. List only the proxy's real address in `WITMOOT_TRUSTED_PROXIES`.
Loopback inside a container means the container itself. See
[Docker / Compose](deployment.md#docker--compose) for proxy networking.

The container runs as UID/GID `10001:10001` with a read-only root
filesystem, no Linux capabilities, and a small writable `/tmp`. The named data
volume has mode `0700` and survives container replacement. For a bind mount,
create the directory first with owner `10001:10001` and mode `0700`.
`docker compose down -v` deletes your data.

The healthcheck probes `/healthz` on the port in `WITMOOT_ADDR`. Override it if
you bind to a specific non-loopback address.

Pin a version or digest in `.env`:

```dotenv
WITMOOT_IMAGE="ghcr.io/airencracken/witmoot:v0.24.0"
```

For a digest, use the full multi-platform index digest
(`ghcr.io/airencracken/witmoot@sha256:...`) from
`docker buildx imagetools inspect ghcr.io/airencracken/witmoot:v0.24.0`.
There are no `latest`, major or minor tags, so a pull never moves you to a
newer database schema.

## Back up, upgrade, and restore

Automatic backups land in `/data/backups`; see
[automatic backups](automatic-backups.md). Copy them off the Docker host.

For a full-volume backup, stop the server first:

```sh
umask 077
docker compose stop witmoot
docker compose run --rm --no-deps -T --entrypoint tar witmoot -C /data -cf - . > witmoot-before-upgrade.tar
```

Check that the command succeeded and keep the file private. To upgrade, read
the new release notes, set `WITMOOT_IMAGE` to the new version, then run
`docker compose pull` and `docker compose up -d`. Check `docker compose ps`
and `docker compose logs witmoot` before you use it again.

Migrations run forward only. To go back, you need the old image and the
backup taken before the upgrade. Restore into a **new** named volume so the
current one stays intact:

```yaml
# docker-compose.restore.yml
volumes:
  witmoot-data:
    name: witmoot-restored
```

Set `WITMOOT_IMAGE` in `.env` to the version that made the backup. From the
same project directory, run:

```sh
docker compose -f docker-compose.yml -f docker-compose.restore.yml run --rm --no-deps -T --entrypoint tar witmoot -C /data -xf - < witmoot-before-upgrade.tar
docker compose -f docker-compose.yml -f docker-compose.restore.yml up -d
```

The new volume must be empty. Keep using the override for later Compose
commands. Check health, sign-in, and your data before you delete the old
volume. Restore only backups you made.

## Build locally

From a source checkout:

```sh
VERSION=0.24.0 docker compose -f docker-compose.yml -f docker-compose.build.yml up --build -d
```

This builds `witmoot:local` and does not pull the published image.

## Release maintenance

Published images come from `Dockerfile.release`, which packages the verified
native release binaries. The release workflow publishes images only after
both architectures pass their checks.

On the first release, GHCR creates the package as private. Open
**Packages → witmoot → Package settings → Change visibility** and make it
public, then re-run the failed publish job. Its saved artifacts last seven
days. A published version tag is never overwritten; ship runtime security
fixes as a new patch version.

Local checks:

```sh
make test-containers
docker build --build-arg VERSION=0.24.0 -t witmoot:local .
make test-container-lifecycle VERSION=0.24.0
```
