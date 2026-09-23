# HTTPS reverse proxies

**Caddy is the recommended setup**, with automatic certificate issuance and
renewal. nginx and Apache 2.4 are supported alternatives. Serve Witmoot at
the root of its own hostname, such as `https://board.example.org`; these
examples do not support a URL prefix such as `/witmoot/`.

## Configure Witmoot

For a native service and proxy on the same host, set:

```sh
WITMOOT_ADDR="127.0.0.1:8082"
WITMOOT_BASE_URL="https://board.example.org"
WITMOOT_SECURE_COOKIES="true"
WITMOOT_TRUSTED_PROXIES="127.0.0.1/32,::1/128"
```

Put these in `/etc/conf.d/witmoot` for OpenRC or `/etc/witmoot/witmoot.env`
for systemd, then restart Witmoot. Keep port 8082 private. The public base
URL must match the hostname visitors use for invitations and form submissions.
The proxy examples replace incoming forwarding headers so rate limits use
the visitor's address.

These examples assume the proxy directly accepts visitors' connections.
If a CDN or another proxy comes first, configure its trusted addresses in
the web server before accepting its client-IP headers. For containers, use
the service name on a private network and trust only the actual proxy peers;
see [Docker deployment](deployment.md#docker--compose).

## Generate a site configuration

The binary includes all three templates, so a package install has the helpers
available without finding example files:

```sh
witmoot --help
witmoot proxy-config caddy --domain board.example.org > witmoot.Caddyfile
witmoot proxy-config nginx --domain board.example.org > witmoot.nginx.conf
witmoot proxy-config apache --domain board.example.org > witmoot.apache.conf
```

Use `--upstream HOST:PORT` to change the loopback destination. It defaults to
the native service port. nginx and Apache accept `--tls-cert /path/fullchain.pem`
and `--tls-key /path/privkey.pem` together; otherwise their paths follow Certbot's
`/etc/letsencrypt/live/HOST/` layout. Caddy manages certificates automatically.

These commands print config to stdout. They do not change the service, obtain
certificates, or reload the proxy. Copy the application settings from the
generated comments into your service configuration, then include the site
in the proxy's configuration and validate it with the commands below.
The helpers support a proxy and application on the same host; use the static
examples as a starting point for other network arrangements.

## Caddy: recommended

Use [`contrib/caddy/Caddyfile`](../contrib/caddy/Caddyfile) and the
[Caddy deployment guide](deployment.md#caddy-and-imvault). Point DNS at the
host and allow public ports 80 and 443; Caddy handles certificates and HTTPS.

Validate the complete configuration before reloading:

```sh
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile
```

## nginx

Include [`contrib/nginx/witmoot.conf`](../contrib/nginx/witmoot.conf) from
nginx's `http` block. Replace `board.example.org` and the certificate/key paths
with your own. The paths follow Certbot's layout; nginx needs certificates
you already manage. Arrange renewal and a configuration check/reload afterward.

The example redirects HTTP to HTTPS, preserves application headers and the
host, replaces forwarded client addresses, and disables request/response
buffering and proxy caching. Its **64 MiB whole-request limit** accommodates
Witmoot's image batches and multipart overhead. Witmoot and imvault still
enforce their own image and request limits.

Run these administrative commands after including the site:

```sh
nginx -t
# Only reload after the check succeeds.
nginx -s reload
```

Keep the supplied forwarding-header assignments. See nginx's
[proxy header and buffering reference](https://nginx.org/en/docs/http/ngx_http_proxy_module.html).

## Apache 2.4

Include [`contrib/apache/witmoot.conf`](../contrib/apache/witmoot.conf) with
`mod_ssl`, `mod_proxy`, `mod_proxy_http`, `mod_headers`, and `mod_alias` loaded.
On Debian/Ubuntu, use `a2enmod ssl proxy proxy_http headers alias`; on Gentoo,
enable those modules in your Apache package/server configuration. The main
configuration must listen on ports 80 and 443. Replace the hostname and
certificate paths, and arrange certificate renewal and reloads.

The virtual host disables forward proxying, preserves the host, replaces
forwarded headers, and proxies all routes. Its **64 MiB whole-request limit**
includes multipart overhead. Keep private boards and `/images/` behind Witmoot;
do not add static-file aliases or shared caches that bypass its access checks.

Validate and gracefully reload as an administrator:

```sh
apachectl configtest
# Only reload after the check succeeds.
apachectl graceful
```

Some distributions call the command `apache2ctl`. Preserve `ProxyAddHeaders Off`
and the explicit `RequestHeader` assignments together. See the
[Apache proxy reference](https://httpd.apache.org/docs/2.4/mod/mod_proxy.html#proxyaddheaders).

## Verify your deployment

Check `/healthz` through the public HTTPS hostname, then sign in, follow an
invitation, and post with an image. Check private-board access from a second
account. Give imvault its own hostname and proxy site; each application's
proxy and trust settings are independent. See [running beside imvault](imvault.md).

`make test-proxies` runs the nginx and Apache examples against a temporary
HTTP backend over local TLS. It checks spoofed headers, cookies, authentication
and HTMX headers, escaped URLs, uploads over 1 MiB, ranges, access-denied
responses, and streaming. Both servers are required; a missing server fails
the check. Set `NGINX_BINARY`, `APACHE_BINARY`, and `APACHE_MODULE_DIR` for custom
installations; the default Apache module path is `/usr/lib/apache2/modules`.
CI runs these checks before building release packages.
