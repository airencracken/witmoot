# HTTPS reverse proxies

**Caddy is the recommended proxy**; it gets and renews certificates for you.
nginx and Apache 2.4 also work. Serve Witmoot at the root of its own hostname,
such as `https://board.example.org`. A path prefix such as `/witmoot/` is not
supported.

## Configure Witmoot

With the service and proxy on one host, set:

```sh
WITMOOT_ADDR="127.0.0.1:8082"
WITMOOT_BASE_URL="https://board.example.org"
WITMOOT_SECURE_COOKIES="true"
WITMOOT_TRUSTED_PROXIES="127.0.0.1/32,::1/128"
```

Put these in `/etc/conf.d/witmoot` for OpenRC or `/etc/witmoot/witmoot.env`
for systemd, then restart Witmoot. Keep port 8082 private. The base URL must
match the hostname visitors use.

The examples assume the proxy takes visitors' connections directly. If a CDN
or another proxy comes first, trust its addresses in the web server before
accepting its client-IP headers. For containers, see
[Docker / Compose](deployment.md#docker--compose).

## Generate a site configuration

The binary carries all three templates:

```sh
witmoot --help
witmoot proxy-config caddy --domain board.example.org > witmoot.Caddyfile
witmoot proxy-config nginx --domain board.example.org > witmoot.nginx.conf
witmoot proxy-config apache --domain board.example.org > witmoot.apache.conf
```

`--upstream HOST:PORT` changes the destination, which defaults to
`127.0.0.1:8082`. For nginx and Apache, pass `--tls-cert /path/fullchain.pem`
and `--tls-key /path/privkey.pem` together; otherwise the paths follow
Certbot's `/etc/letsencrypt/live/HOST/` layout.

The command only prints a configuration. Copy the Witmoot settings from its
comments into your service configuration, include the site in your proxy, and
validate it as shown below. The generator assumes one host; start from the
static examples for anything else.

## Caddy: recommended

Use [`contrib/caddy/Caddyfile`](../contrib/caddy/Caddyfile) as shown in
[Caddy and Imvault](deployment.md#caddy-and-imvault). Point DNS at the host
and open ports 80 and 443. Validate before reloading:

```sh
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile
```

## nginx

Include [`contrib/nginx/witmoot.conf`](../contrib/nginx/witmoot.conf) from
nginx's `http` block. Replace `board.example.org` and the certificate paths.
nginx does not renew certificates, so arrange renewal and a reload yourself.

The example redirects HTTP to HTTPS, replaces forwarded client addresses, and
turns off buffering and caching. It allows **64 MiB per request**, enough for
a batch of images. Keep its forwarding-header lines as they are.

```sh
nginx -t
# Only reload after the check succeeds.
nginx -s reload
```

See nginx's
[proxy module reference](https://nginx.org/en/docs/http/ngx_http_proxy_module.html).

## Apache 2.4

Include [`contrib/apache/witmoot.conf`](../contrib/apache/witmoot.conf) with
`mod_ssl`, `mod_proxy`, `mod_proxy_http`, `mod_headers` and `mod_alias`
loaded. On Debian/Ubuntu, run `a2enmod ssl proxy proxy_http headers alias`.
Apache must listen on ports 80 and 443. Replace the hostname and certificate
paths, and arrange renewal and reloads.

The site replaces forwarded headers, proxies every route, and allows
**64 MiB per request**. Do not add static aliases or shared caches for
`/images/`; they would skip Witmoot's access checks. Keep
`ProxyAddHeaders Off` and the `RequestHeader` lines together.

```sh
apachectl configtest
# Only reload after the check succeeds.
apachectl graceful
```

Some distributions call it `apache2ctl`. See the
[Apache proxy reference](https://httpd.apache.org/docs/2.4/mod/mod_proxy.html#proxyaddheaders).

## Verify your deployment

Open `/healthz` through the public hostname, sign in, follow an invitation,
and post an image. Check a private board from a second account. Give Imvault
its own hostname and proxy site; see [running beside Imvault](imvault.md).

`make test-proxies` runs the nginx and Apache examples locally and needs both
servers installed. Set `NGINX_BINARY`, `APACHE_BINARY` and `APACHE_MODULE_DIR`
for custom installs; the Apache module path defaults to
`/usr/lib/apache2/modules`.
