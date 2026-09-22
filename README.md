# Witmoot

A little corner of the internet for your people.

Witmoot is a private bulletin board for families and groups of friends. It takes
its familiar rooms, topics, and conversations from phpBB and vBulletin, and its
small, self-hosted clubhouse spirit from imvault. **Comfyware:** somewhere to
keep the plans, little updates, good finds, and conversations worth coming back to.

One Go binary. One data directory. Server-rendered HTML, SQLite, and HTMX.
No frontend build step or external service needed to run it.

![Witmoot's private board with a sample conversation](docs/images/board.png)

*An isolated browser-test instance, with sample conversation data.*

## Come on in

Requires Go 1.26 or later. Dependencies are pinned in `go.mod`; HTMX 2.0.10 and
its license are included locally. The first build downloads Go dependencies.

```bash
make build
read -r -s -p 'Choose an owner password: ' witmoot_password
printf '\n'
printf '%s\n' "$witmoot_password" | ./bin/witmoot create-owner --username alex --password-stdin
unset witmoot_password
./bin/witmoot
```

Open <http://127.0.0.1:8080>, sign in, and use **Invites** to make a link for
someone you know. Each link works once and expires after seven days. Passwords
need at least 12 characters and may contain up to 72 bytes.

An owner is created locally. There is no public first-user takeover or open
registration, even on an empty installation. Invited accounts are always members.

## What is here

- Five starter rooms for everyday conversation, plans, projects, recommendations,
  and the group itself.
- Private topic lists, chronological messages, and pagination.
- New conversations and replies, with plain text and preserved line breaks.
- Search across conversation titles and message text.
- Owner invitations, member accounts, sign-in, and sign-out.
- Responsive pages and HTMX navigation. The same forms work without JavaScript.
- Transactional schema initialization, topic creation, replies, and invitation
  redemption. Restarting keeps your conversations and sessions.

One installation is one group. All members can read all rooms and conversations.
There are no hidden subgroups or private messages in this first version.

## Make it yours

| Setting | Default | Purpose |
| --- | --- | --- |
| `WITMOOT_NAME` | `Witmoot` | Name in the header and page titles |
| `WITMOOT_DATA_DIR` | `./data` | SQLite database and journal files |
| `WITMOOT_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `WITMOOT_SECURE_COOKIES` | `false` | Set `true` behind HTTPS |

```bash
WITMOOT_NAME='Our little corner' WITMOOT_ADDR=127.0.0.1:9000 make run
```

The starter rooms are seeded once by the initial migration. Their names are
currently fixed; room management is future work. The default data directory is
created with owner-only access. An existing directory keeps its permissions.

## Keeping the house in order

Use an HTTPS reverse proxy for a hosted installation, keep the app port private,
and set `WITMOOT_SECURE_COOKIES=true`. Secure mode uses `__Host-` cookies.

Passwords use bcrypt. Session and invitation tokens are random and only their
SHA-256 hashes are stored. Sessions expire after seven days. Forms require CSRF
tokens and use Go's cross-origin protection. User text is escaped; private pages
send `Cache-Control: no-store` and disable HTMX history storage. Assets are served
locally under a restrictive content security policy.

Sign-in and invitation redemption share a limit of 20 attempts per 15 minutes
per direct client IP. The limiter is in memory and does not trust forwarded
headers. Behind a proxy, users share that proxy's budget; use an edge limiter
and account for this when configuring a deployment.

For a simple backup, **stop the server and copy the entire data directory**.
Restore it with the server stopped, then start Witmoot against that directory.
Do not copy only the live `.db` file while SQLite is using WAL journaling.

This is a first working foundation. Account recovery, member removal, post
editing/deletion, moderation, invitation revocation, attachments, email, and
custom room management are not implemented yet. Do not use it as the sole copy
of irreplaceable family material. SQLite data is not encrypted at rest.

## Working on it

```bash
make check   # formatting, vet, and tests with the race detector
make build   # standalone binary; no cgo required
```

Tests cover private routes, invitation races and rollback, session expiry,
CSRF, role checks, escaping, search literals, pagination, failed writes, and
persistence across restarts.

Browser checks use Node.js and Playwright only as development tools:

```bash
npm --prefix scripts/browser ci
cd scripts/browser
npx playwright install chromium
cd ../..
make test-browser
```

These checks start an isolated temporary instance and exercise the full flow with
HTMX enabled and JavaScript disabled, plus mobile layout and browser security
policy checks. They fail if browser tooling is missing. Optional screenshots:
`WITMOOT_SCREENSHOT_DIR=/tmp/witmoot-screenshots make test-browser`.

The product direction lives in [docs/product.md](docs/product.md).
