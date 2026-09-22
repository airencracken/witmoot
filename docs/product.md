# A place for your people

Witmoot is comfyware: software for a family, an extended family, or a group of
friends who want somewhere of their own. It shares imvault's small, self-hosted
clubhouse philosophy. Familiar forum structure gives conversations a home and
makes them easy to find months later.

## The first shape

One installation serves one board. Owners choose Personal, Private, or Open
in Settings. Personal is an owner-only notebook. Private is an invited group.
Open offers public browsing and registration, with accounts required to post.
Rooms organize conversations; topics and chronological replies keep them together.

Modes set the audience for new conversations: owners, members, or the public.
Each conversation keeps that audience through later mode changes, including all
its replies. Opening registration does let new members see older members-only
topics. The interface states this explicitly. Private and Personal modes also
close public access to the whole board; Personal blocks member access as well.

This carries over imvault's selectable-profile approach without treating the
three uses as separate products. Personal is stricter here: invitations are
disabled, and every owner has access. There is no anonymous posting.

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

Separate groups inside one installation, per-conversation audience changes,
complex permission matrices, and private messaging need explicit product
decisions. This version does not imply those features or promise compatibility
with phpBB or vBulletin data.
