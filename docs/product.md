# A place for your people

Witmoot is comfyware: software for a family, an extended family, or a group of
friends who want somewhere of their own. Witmoot and imvault share this direction:
the personal spirit of the IndieWeb, the control of self-hosting, and a focus on
small communities. The people using the software should be the people it serves
and the people who run it. Familiar forum structure gives conversations a home
and makes them easy to find months later.

## What comfyware means

- **For your people.** Friends, families, and small communities are the starting
  point. Growth and reach are not measures of success.
- **Comfortable for a family.** Software you would let your kids use is the
  design standard. Clear audiences, understandable controls, and community
  stewardship matter as much as a friendly appearance. Member management and
  moderation belong among the next essentials.
- **Respect for attention.** Keep distraction low and avoid habit-forming
  mechanics. No recommendation algorithms, ranked feeds, infinite scroll,
  streaks, popularity scores, or pressure to return.
- **A pace you choose.** Conversations stay chronological, pages have an end,
  and catching up does not require staying online. Any future notifications
  should be optional and under the recipient's control.
- **Run by and for the people using it.** Keep hosting understandable, make
  backups practical, and build toward useful exports. Community needs guide
  features; engagement targets do not.

These are product constraints, including when considering future features.
Personal, Private, and Open change who can participate; all three share the
same respect for attention and local control.

## Inspirations

- [imvault](https://github.com/airencracken/imvault): a companion comfyware
  project, with selectable ways to share and a home for the group's images.
- [copyparty](https://github.com/9001/copyparty): portable, browser-accessible
  sharing on hardware people already have. For Witmoot, the lesson is to keep
  useful community tools practical to run and approachable to use.
- phpBB and vBulletin: familiar rooms, persistent conversations, and a shared
  place people can return to in their own time.

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

Optional imvault integration lets members upload, choose existing images, and
paste links. A connection belongs to one member. Sharing an image is an explicit
part of posting, and its Witmoot URL follows the conversation's audience. New
uploads stay private in imvault. See [imvault.md](imvault.md) for the full model.

The Moot Knight sits at a small round table with a book and quill: a welcoming
host for a place built around gathering and keeping conversations.

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
2. Shared memories: richer image descriptions and album browsing, while keeping
   sharing deliberate and access rules understandable.
3. Gentle conveniences: bookmarks, unread markers, optional email digests, and
   good exports and backups.

Separate groups inside one installation, per-conversation audience changes,
complex permission matrices, and private messaging need explicit product
decisions. This version does not imply those features or promise compatibility
with phpBB or vBulletin data.
