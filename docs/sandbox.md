# Linux confinement

You can run Witmoot inside Bubblewrap. It is off by default. Once you turn it
on, a missing tool, blocked namespace or bad mount stops startup; Witmoot
never falls back to running unconfined.

Install your distribution's current `bubblewrap` package, version 0.8 or later.
The service account must be allowed to create user namespaces. The launcher
refuses to run as root or with a setuid Bubblewrap. If kernel or AppArmor
policy blocks it, `sandbox --check` shows the error without starting the
server. Fix the cause; do not loosen host policy to hide it.

## OpenRC

Add this to `/etc/conf.d/witmoot` and restart the service:

```sh
WITMOOT_SANDBOX="true"
```

The service keeps its user, data directory and log file. The launcher passes
SIGTERM on and allows twenty seconds for shutdown.

To check the policy first, run this as the service account, not root, with
its settings loaded. The data directory must already exist:

```sh
WITMOOT_DATA_DIR=/var/lib/witmoot witmoot sandbox --check
```

## systemd

The shipped unit already limits filesystem access. To add Bubblewrap, create a
drop-in with `systemctl edit witmoot`:

```ini
[Service]
ExecStart=
ExecStart=/usr/bin/witmoot sandbox
RestrictNamespaces=user mnt pid ipc uts net
ProtectKernelTunables=no
ReadOnlyPaths=/sys
RestrictSUIDSGID=no
KillMode=mixed
```

Use `/usr/local/bin/witmoot` for a source install. Keep the unit's other
settings. The overrides let Bubblewrap create its namespaces and mount its own
`/proc`, keep `/sys` read-only, and let the launcher stop the server before
systemd kills what is left. `NoNewPrivileges=yes` still blocks setuid. Run
`systemctl daemon-reload` and restart. To turn Bubblewrap off, remove the
drop-in.

## What the server can access

The server sees read-only system directories, CA certificates, DNS and hosts
files, and timezone data. It keeps the host network for HTTP, mail and
Imvault. It gets its own process namespace, minimal devices and a private
temporary directory. Only its data directory is writable. Home directories,
`/etc/shadow`, host temporary files and host Unix sockets are hidden.

Witmoot passes its own settings, credentials included, through the
environment and drops everything else. Credentials never appear in
Bubblewrap's arguments. `/usr` stays visible, so keep secrets out of it.

To trust a private certificate authority, such as for an internal SMTP relay
or Imvault host, set `SSL_CERT_FILE` to a PEM bundle in the service
environment. Include any public CAs you still need; the system certificate
directory also stays available. `SSL_CERT_DIR` is not passed in.

Witmoot refuses a data directory that is too broad or overlaps system paths,
such as `/`, `/var`, `/home`, `/tmp` or `/usr`, even through symlinks.
`--bwrap /path/to/bwrap` or `WITMOOT_BWRAP` picks a different Bubblewrap.
The `--write-dir` and `--read-file` options from before 0.10.0 are gone; use
`SSL_CERT_FILE` for a certificate.

The server cannot create user namespaces of its own (`--disable-userns`),
which keeps much of the kernel out of reach if it is compromised. A
compromised server can still read its own data and use the network.
Bubblewrap sets no CPU, memory or disk limits; use your service manager for
those.

## Verification

`make test-sandbox` runs the server under real Bubblewrap from check to
shutdown. The policy itself comes from the
[Comfylib](https://github.com/airencracken/comfylib) `sandbox` package.
