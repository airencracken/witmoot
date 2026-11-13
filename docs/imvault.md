# Images at the table

Witmoot can share images from an Imvault server. This is optional; text
conversations work without it.

## Connect the two

1. Run an Imvault instance the Witmoot server can reach.
2. Set `WITMOOT_IMVAULT_URL` to its base URL and restart Witmoot. Use HTTPS
   across any network you do not control.
3. In Imvault, create an API key under **Settings → API keys**.
4. In Witmoot, open **Account → Images**, paste the key, and choose
   **Connect Imvault**.

Each member connects their own account, and Witmoot acts with that key's
permissions. Use an ordinary Imvault account unless you mean to grant admin
access. Members cannot point their key at any server other than the one the
owner configured.

## Running beside Imvault

Give each app its own data directory, service account and loopback port. With
Imvault on `127.0.0.1:8080`:

```sh
WITMOOT_ADDR="127.0.0.1:8082"
WITMOOT_BASE_URL="https://board.example.org"
WITMOOT_DATA_DIR="/var/lib/witmoot"
WITMOOT_SECURE_COOKIES="true"
WITMOOT_TRUSTED_PROXIES="127.0.0.1/32,::1/128"
WITMOOT_IMVAULT_URL="https://photos.example.org"
```

Put these in `/etc/conf.d/witmoot` for OpenRC or `/etc/witmoot/witmoot.env`
for systemd, restart Witmoot, and add a separate site to your Caddyfile:

```caddyfile
board.example.org {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8082
}
```

Use Imvault's public, canonical URL for `WITMOOT_IMVAULT_URL`. Pasted links
must match it, and Witmoot must be able to reach it. There is no shared
sign-in. Back up both data directories, including Witmoot's `imvault.key`.

`WITMOOT_TRUSTED_PROXIES` is empty by default. List only proxies you run.
Witmoot reads `X-Forwarded-For` only from a trusted peer, walking right to left
to the first untrusted address, and uses that address for rate limits. Invalid
values stop startup. Caddy and the
[nginx and Apache examples](reverse-proxies.md) replace forwarded headers from
visitors. If another proxy sits in front of Caddy, trust it in both Caddy and
Witmoot. HTTPS deployments still need `WITMOOT_SECURE_COOKIES="true"`.

## Three ways to share

Open **Add images** when you start a conversation or reply:

- **Upload images** sends new files to your Imvault account as private, with
  metadata hidden, whatever your Imvault defaults are.
- **Load image library** shows your existing images, with search and pages.
  Your selections stay as you move between pages. Without JavaScript, loading
  the library keeps your draft and selections. You can pick from the first
  page of results, or use **Open your full library** to browse further and
  copy a link into your message.
- **Paste Imvault image links** accepts image pages and raw, preview or
  thumbnail links from the configured server, including ones with `?v=…`.
  Public thumbnails need no connection. Other images must be reachable through
  your own account.

A message can hold four images of up to 8 MiB each: JPEG, PNG, WebP or GIF.
Messages still need text. If posting fails, your text and chosen images stay,
but you have to pick upload files again.

Witmoot shows Imvault's preview for still images and a still thumbnail for
animated ones. It never fetches originals or shows camera metadata.

## Avatars from your library

In the **Profile** section of the account page, members can pick an avatar
from their Imvault library. Witmoot copies the preview, crops it square and
keeps a small PNG, plus a short GIF animation when the image is animated. The
avatar keeps working if Imvault goes away or you disconnect. This is the only
image Witmoot stores itself; message images stay in Imvault.

## Who can see a shared image

Attaching an image shares its preview with everyone who can read that
conversation, even when the image is private in Imvault. In Open mode, new
conversations and their images are public. A conversation keeps its audience
when the board mode changes, so images in a members-only conversation stay
behind sign-in. Switching to Private or Personal mode closes public access to
the whole board, images included.

Images load through local `/images/...` URLs. Each request checks the
conversation's audience before Witmoot asks Imvault. Witmoot does not forward
browser cookies or expose API keys. Witmoot remembers a
successful Imvault check for 15 seconds, and browsers cache images privately
for 15 seconds, so revoking access can take that long to show.

Sharing a public Imvault link in a private conversation does not change it in
Imvault. Anyone who has already seen an image may have kept a copy.

## Connections, backups, and failures

Witmoot encrypts each API key with the random key in `imvault.key` in its data
directory. Back up the whole data directory with that file. A database backup
alone cannot restore connections. Witmoot stores references only, so back up
Imvault separately.

**Disconnect Imvault** deletes your stored key. Your files and conversations
stay. Images that relied on your connection stop loading; public pasted images
keep working. Reconnecting brings them back. Revoking the key in Imvault also
stops access. Changing `WITMOOT_IMVAULT_URL` breaks references to the old
server.

If Imvault is down, conversations stay readable and images may not load.
Posting with an unreachable image fails with an explanation; text-only
messages still work.

If a message fails after an upload, Witmoot deletes the files it just uploaded
for it. It never deletes library images or pasted links. A crash or network
failure can still leave an unattached private file in Imvault. Witmoot logs
failed cleanups with their file IDs.

For album links and drafts from Imvault, see
[bring a link to the conversation](discussion-references.md).
