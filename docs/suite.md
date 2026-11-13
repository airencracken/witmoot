# Optional Comfyware connections

Each Comfyware app keeps its own accounts, permissions and data. Every
connection here is optional. With none configured, members sign in with a
local password and the Apps menu is hidden.

Set these in `/etc/conf.d/witmoot` (OpenRC), `/etc/witmoot/witmoot.env`
(systemd), or the Compose `.env` file, then restart. All default to empty.

## Apps menu

Set `WITMOOT_APPS` to a JSON array of app IDs and their HTTP(S) addresses.
Witmoot leaves itself out of the menu. For example:

```sh
WITMOOT_APPS='[{"id":"imvault","url":"https://photos.example.org"},{"id":"songstead","url":"https://music.example.org"},{"id":"witmoot","url":"https://discuss.example.org"},{"id":"folkweave","url":"https://people.example.org"},{"id":"tidings","url":"https://read.example.org"}]'
```

Unknown IDs, duplicates, and addresses with credentials, queries or fragments
stop startup. Witmoot never contacts these addresses, and the menu grants no
membership in the other apps.

## Community sign-in

Connect an OpenID Connect provider:

```sh
WITMOOT_SHARED_ISSUER=https://identity.example.org
WITMOOT_SHARED_CLIENT_ID=witmoot
WITMOOT_SHARED_CLIENT_SECRET=configure-in-your-protected-service-environment
```

Set `WITMOOT_BASE_URL` to Witmoot's public address and register exactly
`https://your-app.example.org/auth/shared/callback` with the provider, using a
separate client for each app. The issuer must use HTTPS; loopback HTTP works
for testing. Protect the client secret like any other password.

Members sign in with their password, open **Account → Community sign-in**, and
link their community identity. After that, the login page offers it. An
unlinked identity cannot create an account or claim one by email address.
Invitations, roles and suspensions still apply. Unlinking leaves the password
working. Signing out ends only the Witmoot session. Keep a working local owner
password in case the provider is down.

Links between accounts and identities live in the database and in full
backups, not in personal exports. An unfinished sign-in expires after five
minutes or when the server restarts.

## Drafts from other apps

Songstead and Imvault can prepare a draft for Witmoot. The source app shows
what it will send. In Witmoot, pick a board or a recent conversation and check
who can read it before posting. See
[bring a link to the conversation](discussion-references.md).

To send the link back, list the source app in `WITMOOT_APPS` and set
`WITMOOT_BASE_URL`. After posting, choose to return and keep the conversation
link; the source app asks you to confirm before saving it. The offer lasts
twenty minutes and ends if the server restarts. If it runs out, copy the
conversation link by hand.

## Maintenance

`python3 scripts/comfy.py check` runs the shared configuration, release and
container checks from the Comfylib version pinned in `go.mod`.
