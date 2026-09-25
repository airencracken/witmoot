# Images at the table

Witmoot can use an imvault server for images. The integration is optional:
text conversations work with no image service configured.

## Connect the two

1. Run an imvault instance reachable from the Witmoot server. This integration
   uses imvault's v1 account, files, upload, delete, and preview endpoints.
2. Set `WITMOOT_IMVAULT_URL` to that instance's base URL and restart Witmoot.
   Use HTTPS when the connection crosses a network you do not control.
3. In imvault, create an API key under **Settings → API keys**.
4. In Witmoot, open **Images**, enter that key, and choose **Connect imvault**.

Each member connects their own account. Witmoot uses the key's imvault permissions;
use an ordinary account unless administrative access is intended. The owner
configures one server for the board. Members cannot send keys to arbitrary URLs.
API responses cannot redirect Witmoot to another server.

## Running beside imvault

Use separate data directories, service accounts, and loopback ports. For a
native installation with imvault on `127.0.0.1:8080`, Witmoot can use:

```sh
WITMOOT_ADDR=127.0.0.1:8082
WITMOOT_BASE_URL=https://board.example.org
WITMOOT_DATA_DIR=/var/lib/witmoot
WITMOOT_SECURE_COOKIES=true
WITMOOT_TRUSTED_PROXIES=127.0.0.1/32,::1/128
WITMOOT_IMVAULT_URL=https://img.internetrelay.chat
```

Set these in the environment of the Witmoot service and restart it. Add a
separate site block to the existing Caddyfile, using your board's hostname:

```caddyfile
board.example.org {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8082
}
```

The [deployment guide](deployment.md) covers the packaged services, owner
provisioning, and Docker. For OpenRC, put the settings in `/etc/conf.d/witmoot`;
for systemd, use `/etc/witmoot/witmoot.env`.

Use imvault's public, canonical URL for `WITMOOT_IMVAULT_URL`: pasted links
must match it, and Witmoot must be able to reach it. There is no shared sign-in;
each person connects their own imvault API key. Keep both data directories in
your backups, including Witmoot's `imvault.key`.

`WITMOOT_TRUSTED_PROXIES` defaults to empty. Only list proxies you operate,
never all internet addresses. Witmoot checks the direct peer before considering
`X-Forwarded-For`, then walks the chain from right to left until the first
untrusted address. Missing or malformed trusted hops fall back to the peer's
rate-limit bucket. Invalid configuration stops startup.

Caddy normally replaces untrusted forwarded headers; see its
[forwarded-header defaults](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy#defaults).
The [nginx and Apache examples](reverse-proxies.md) also replace these headers
when accepting visitors' connections directly.
If another proxy sits in front of Caddy, configure the trusted chain in Caddy
and Witmoot. Keep the application port private. These settings affect client
addresses for rate limiting; `WITMOOT_SECURE_COOKIES=true` remains necessary for
HTTPS deployments.

## Three ways to share

Open **Add images** when starting a conversation or replying:

- **Upload images** sends new files to your connected imvault account with
  `visibility=private` and `metadata=hidden`, regardless of imvault's defaults.
- **Load image library** fetches your existing images when you ask for them,
  with search and pagination. Selections survive moving between picker pages.
  Without JavaScript, loading or refreshing the library keeps your text draft
  and selections without posting; choose upload files afterward. The first page
  of search results can be selected, or use **Open your full library** to browse
  further and copy a link into your draft.
- **Paste imvault image links** accepts image pages and raw, preview, or thumbnail
  links from the configured server. Public thumbnails need no connection.
  Other images must be accessible through your connected account's files API.
  Thumbnail and preview links with imvault's `?v=…` version parameter work too.

A message may contain four images, each up to 8 MiB. Supported uploads are JPEG,
PNG, WebP, and GIF. Messages still require text. If validation or an image upload fails, text and
valid image selections remain, but browser upload fields must be selected again.

Still images use imvault's generated preview. Animated images use a still
thumbnail. Witmoot does not fetch originals or expose camera metadata. This is
image sharing, not a general attachment store or an embedded imvault browser.

## Avatars from your library

Under **Account**, a member can import an image from their connected imvault
library as their avatar. Witmoot copies the chosen preview, crops it square, and
stores a small PNG in its own database, so the avatar keeps working even if
imvault is unreachable later or the connection is removed. This is the one place
Witmoot keeps image bytes of its own; conversation images remain references.

## Who can see a shared image

Attaching an image explicitly shares its preview with everyone allowed to read
that conversation, even when the image is private in imvault. In Open mode,
new conversations and their attached previews are public. Existing topic
audiences are preserved through mode changes; private images attached to a
members-only topic stay behind sign-in. Open registration still lets anyone
become a member, as explained in the board settings.

Witmoot serves images through local `/images/...` URLs. Each request checks the
current board mode and conversation audience before accessing imvault. Private
and Personal modes close access to formerly public images along with the board.
The proxy does not forward browser cookies, expose API keys, or store shared
conversation image bytes in SQLite. Responses use `Cache-Control: no-store`.

Existing public imvault links remain public at their original URLs. Sharing one
in a private conversation does not change its visibility in imvault. Anyone
who has already viewed an image can keep a copy.

## Connections, backups, and failures

API keys are encrypted with AES-GCM, bound to the Witmoot member and configured
server. The random encryption key is stored as `imvault.key` in Witmoot's data
directory with owner-only file permissions. Back up the entire data directory,
including this key. A database backup alone cannot restore the connections.
Back up imvault separately: Witmoot stores references, and depends on imvault
for the images themselves.

**Disconnect imvault** deletes the member's stored API key without deleting their
files or conversations. Images that relied on that connection stop loading;
public pasted images continue to work. Reconnecting restores accessible images.
Revoking the key in imvault also stops credentialed access. A different account
cannot use an old reference unless imvault grants that account access to the file.
Changing `WITMOOT_IMVAULT_URL` disables references to the old server.

If imvault is unavailable, conversations remain readable and images may be
unavailable. Reading a conversation or opening its composer does not request
the image library. Posting with an unavailable attachment fails with an explanation;
text-only posting remains possible. Library requests and media responses are
bounded in size and time.

Post text and attachment references commit together in SQLite. If a post fails,
Witmoot tries to delete only files newly uploaded for that failed submission;
it never deletes selected library files or pasted images. The two servers do
not share a transaction. An interrupted upload response, process crash, or failed
cleanup can leave an unattached private file in imvault. Cleanup failures with
known IDs are logged for the operator to reconcile.
