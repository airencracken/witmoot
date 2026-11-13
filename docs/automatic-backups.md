# Automatic backups

Since 0.17.0, the running service makes a verified, compressed backup every 24
hours. You do not need a cron job or timer. The first attempt starts one minute
after startup. Later attempts count from the last successful backup, even
across restarts. A failed attempt retries after 15 minutes.

Backups go to `backups/` inside the data directory. Each snapshot directory
holds `manifest.json` and `snapshot.tar.zst` when `zstd` is on the service's
PATH, or `snapshot.tar.gz` otherwise. Gzip is built in. If zstd is installed
but fails, the backup fails; Witmoot does not fall back to gzip.

## Limits and scheduling

Set these in `/etc/conf.d/witmoot` for OpenRC or `/etc/witmoot/witmoot.env`
for systemd, then restart the service:

```sh
WITMOOT_BACKUP_INTERVAL="24h"
WITMOOT_BACKUP_KEEP="7"
WITMOOT_BACKUP_MAX_BYTES="10737418240"
WITMOOT_BACKUP_COPY_MAX_BYTES="5368709120"
```

- Interval: 1 hour to 365 days. `0` turns automatic backups off.
- Copies kept: 1 to 30, default 7.
- Total compressed archives: default 10 GiB.
- One uncompressed snapshot: default 5 GiB. Raise this cap if your data
  directory is larger.

Rotation removes the oldest copies past either limit and always keeps the
newest one. A snapshot too large for the archive cap is rejected, and existing
backups stay. A failed backup leaves older copies alone. Rotation never
touches manual backups or pre-migration snapshots.

During a backup, leave free disk space for one uncompressed snapshot plus its
compressed archive.

Owners see the schedule, caps, folder, last success, last error and next
attempt in the **Server** section at the bottom of **Settings**. With backups
turned off it says "Automatic backups are off." Errors also go to the service
log. Backups are never served over the web. They do not include service
configuration or outside credentials; keep those separately.

## Recovery

Copy completed snapshot directories to another machine and keep them private.
They contain password hashes and private conversations. A local copy will not
survive a lost disk.

The snapshot holds a consistent copy of the SQLite database and `imvault.key`
when present, so members' Imvault connections survive a restore. A missing or
wrong `imvault.key` makes the backup fail.

To test a backup, compare the archive's `sha256sum` with the hash in
`manifest.json`, then extract it into a new private directory:

```sh
umask 077
mkdir /private/restore-drill
tar -xf /private/backups/SNAPSHOT/snapshot.tar.zst -C /private/restore-drill
```

Use `snapshot.tar.gz` for gzip copies. Extracting zstd copies needs zstd.
Extract only backups you made, and never over a running data directory.
Point the matching binary at the extracted directory with `WITMOOT_DATA_DIR`
and an unused loopback port.

To recover for real, stop the service first. Keep the service account's
ownership and private permissions when you replace its data directory. Images
live in Imvault; back that up separately.
