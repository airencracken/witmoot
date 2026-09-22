# Witmoot

A little corner of the internet for your people.

Witmoot is a bulletin board for personal use, families, and groups of friends. It takes
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

Open <http://127.0.0.1:8080>, sign in, and choose **Settings** to select Personal,
Private, or Open. New installations start in Private mode. Use **Invites** to
make a link for someone you know. Each link works once and expires after seven days. Passwords
need at least 12 characters and may contain up to 72 bytes.

An owner is created locally. Public registration never creates an owner, even on
an empty installation. Invited and openly registered accounts are always members.

## Your place, your house rules

Owners can change the mode at **Settings** (`/settings`). It takes effect for new
requests immediately and is stored in SQLite, so it survives restarts.

| Mode | Who can browse and post | Joining | New conversations |
| --- | --- | --- | --- |
| **Personal** | Owners only | Disabled, including invitations | Owners only |
| **Private** | Signed-in members and owners | Invitation required | Members only |
| **Open** | Visitors can read public conversations; signed-in members can post | Anyone can register | Public |

Each conversation keeps its audience when the mode changes, and replies inherit
that audience. Conversations created before this feature remain members-only.
Owners can read every audience; members can read public and members-only topics.
Visitors can read public topics only, and only while the board is Open. Counts,
latest-topic summaries, and search respect those same audiences.

**Open signup lets anyone become a member, including access to existing
members-only conversations.** Members-only means behind sign-in; it does not
reserve conversations for the original invited group. Owner-only topics remain
restricted to owners. Personal mode blocks existing member sessions without
deleting their accounts or content. Unexpired invitations work again after
leaving Personal mode.

The composer shows the audience before posting. If a mode change affects that
audience while a draft is open, the server preserves the draft and asks the
author to review the new audience before resubmitting. There is no anonymous
posting or per-conversation audience editor in this version.

This follows imvault's idea of one application with selectable profiles and
preserved content audiences. Personal mode here is strictly owner-only, and
Open requires an account to post; it does not reproduce imvault's anonymous uploads.

## What is here

- Five starter rooms for everyday conversation, plans, projects, recommendations,
  and the group itself.
- Audience-aware topic lists, chronological messages, and pagination.
- New conversations and replies, with plain text and preserved line breaks.
- Search across conversation titles and message text.
- Owner invitations, member accounts, sign-in, and sign-out.
- Responsive pages and HTMX navigation. The same forms work without JavaScript.
- Transactional schema initialization, topic creation, replies, and invitation
  redemption. Restarting keeps your conversations and sessions.

One installation is one board. There are no separate groups or private messages
inside it. Owner-only conversations are shared among all owner accounts.

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

Sign-in, open registration, and invitation redemption share a limit of 20 attempts per 15 minutes
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

Tests cover all three modes, audience filtering, migration from the original
private schema, persisted settings, stale forms and registration policy,
invitation races and rollback, session expiry, CSRF, role checks, escaping,
search literals, pagination, and failed writes.

Browser checks use Node.js and Playwright only as development tools:

```bash
npm --prefix scripts/browser ci
cd scripts/browser
npx playwright install chromium
cd ../..
make test-browser
```

These checks start an isolated temporary instance and exercise the full flow with
HTMX enabled and JavaScript disabled, including changing modes in Settings,
public browsing, open registration, Personal access restrictions, mobile layout,
and browser security policy checks. They fail if browser tooling is missing. Optional screenshots:
`WITMOOT_SCREENSHOT_DIR=/tmp/witmoot-screenshots make test-browser`.

The product direction lives in [docs/product.md](docs/product.md).
