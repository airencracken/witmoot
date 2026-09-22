# A place for your people

Witmoot is comfyware: software for a family, an extended family, or a group of
friends who want somewhere of their own. It shares imvault's small, self-hosted
clubhouse philosophy. Familiar forum structure gives conversations a home and
makes them easy to find months later.

## The first shape

One installation serves one private group. An owner sets up the board and
invites people. Everyone inside can read and join every conversation. Rooms
organize the conversation; topics and chronological replies keep it together.

The default experience should be warm, readable, and unhurried. Useful empty
states are better than fabricated activity. Counts describe the gathering;
they should not become a competition. No ranking, popularity scores, streaks,
or pressure to keep checking in.

## Technology choices

- Go's HTTP server and HTML templates own routing, validation, and rendering.
- SQLite owns persistent state, with foreign keys, transactions, WAL journaling,
  and a versioned embedded migration.
- A single database connection keeps writes simple for small groups. This is
  deliberately a modest deployment model.
- HTMX enhances ordinary navigation and forms. Basic use works without it.
- Alpine.js can be added for a concrete local interaction that warrants it.
- Embedded templates and assets produce a standalone binary with no CDN or
  frontend build dependency at runtime.

## Where to go next

1. The basics of keeping a home: account recovery, member removal, invitation
   revocation, editing one's own posts, owner moderation, and configurable rooms.
2. Shared memories: attachments and optional imvault links, with access rules
   that do not accidentally expose private material.
3. Gentle conveniences: bookmarks, unread markers, optional email digests, and
   good exports and backups.

Public boards, separate groups inside one installation, complex permission
matrices, and private messaging need explicit product decisions. The first
version does not imply those features or promise compatibility with phpBB or
vBulletin data.
