# Linux confinement

Bubblewrap support is optional. Normal startup is unchanged. Once you request
confinement, missing tools, blocked namespaces, or invalid mounts cause an error;
the application never retries outside the sandbox.

Install your distribution's current, patched `bubblewrap` package. Unprivileged
user namespaces must be permitted for the service account. The launcher refuses
to run as root and rejects setuid Bubblewrap installations. Kernel or AppArmor policy may prevent startup; `sandbox --check`
reports the error without starting the server. Do not weaken host policy merely
to hide an error.

## OpenRC

Add this to `/etc/conf.d/witmoot`, then restart the service:

```sh
WITMOOT_SANDBOX=true
```

The service continues to use its existing user, data directory and log file.
Logs remain on stdout/stderr, with OpenRC handling redirection outside the
sandbox. The launcher forwards SIGTERM and allows twenty seconds for shutdown.

You can verify the policy first, as the service account with its application
settings loaded:

```sh
WITMOOT_DATA_DIR=/var/lib/witmoot witmoot sandbox --check
```

Run that command using the configured service user, not root. The data directory
must already exist. Provision accounts using the existing account commands.

## systemd

The shipped unit already confines filesystem access. To also use Bubblewrap,
create a drop-in with `systemctl edit witmoot`:

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

Use `/usr/local/bin/witmoot` for a default source installation. Keep the unit's
other hardening settings. Its default `RestrictNamespaces=yes` blocks the
namespaces Bubblewrap needs; the override permits only the listed types.
`ProtectKernelTunables=yes` masks parts of procfs, which prevents an unprivileged
Bubblewrap process from mounting its own procfs. The override removes that
conflict while keeping host sysfs read-only. Bubblewrap supplies its own private
procfs with protected kernel-control paths for the confined server and tools.
`RestrictSUIDSGID=no` permits the path-resolution syscalls used by newer
Bubblewrap versions; `NoNewPrivileges=yes` continues to block setuid privilege
gains. `KillMode=mixed` sends SIGTERM to the launcher first, so it can let the
server shut down before systemd kills any remaining processes.
Run `systemctl daemon-reload`, then restart the service. To disable whole-service
Bubblewrap, remove these overrides and restore the packaged unit's restrictions.

## What the server can access

The sandbox has read-only `/usr`, binary and library directories, the dynamic
linker cache and library alternatives, system CA certificates, DNS/hosts
configuration and local timezone information. It retains the host network for
HTTP, mail and other configured services. It has its own process namespace,
minimal devices, and temporary directory. On merged-`/usr` systems, `/bin`,
`/lib` and similar paths are recreated as the same symlinks into `/usr` rather
than mounted separately. Only its configured data directory is writable on the
host. Other home directories,
`/etc/shadow`, host temporary files and host Unix sockets are absent.

Application settings are passed in the environment, including credentials the
server needs. Unrelated environment variables and dynamic-loader settings are
removed. Credentials are never put in Bubblewrap's command-line arguments.
`/usr` remains visible: do not place private application credentials there.

Witmoot keeps its database and Imvault key in the data directory, so no other
writable path is needed. To trust a private certificate authority, for example
for an internal SMTP relay or Imvault host, set `SSL_CERT_FILE` to a PEM bundle
in the service environment (`/etc/conf.d/witmoot` or `/etc/witmoot/witmoot.env`).
The bundle should include any public CAs you still need; the system certificate
directory remains available. The sandbox binds that file read-only and points
the server at it. `SSL_CERT_DIR` is not passed into the sandbox.

A data directory that is too broad or overlaps system directories, such as `/`,
`/var`, `/home`, `/tmp` or `/usr`, is refused, including through symlinks.
`--bwrap /path/to/bwrap` selects a custom executable; `WITMOOT_BWRAP` supplies
its default. The `--write-dir` and `--read-file` options of earlier releases are
gone: Witmoot never needed the extra directory, and a certificate file is now
configured with `SSL_CERT_FILE`.

The server cannot create user namespaces of its own: Bubblewrap starts it with
`--disable-userns`. Witmoot never builds nested sandboxes, so this removes a
large part of the kernel a compromised server could otherwise reach. It needs
Bubblewrap 0.8 or later, which every supported distribution ships.

A compromised server can still access its own data and use the network. This
limits access to the rest of the host; it does not protect the database from the
application itself. Bubblewrap is not a CPU, memory or disk quota. Existing
request limits and timeouts still apply; use your service manager for resource
limits.

## Verification

The sandbox policy itself comes from the shared
[comfylib](https://github.com/airencracken/comfylib) `sandbox` package, whose
own tests cover hidden host files, environment filtering, writable data,
retained server networking, a custom CA bundle and graceful shutdown in real
Bubblewrap namespaces. `make test-sandbox` runs Witmoot's server under real
Bubblewrap from check to graceful stop. Setup failures fail the tests rather
than skipping them. `make test-sandbox-mutations` checks deliberate regressions
in how Witmoot builds its policy.
