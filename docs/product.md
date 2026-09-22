# A place for your people

Witmoot is comfyware: software for a family, an extended family, or a group of
friends who want somewhere of their own. Witmoot and imvault share this direction:
the personal spirit of the IndieWeb, the control of self-hosting, and a focus on
small communities. The people using the software should be the people it serves
and the people who run it. Familiar forum structure gives conversations a home
and makes them easy to find months later.

## What comfyware means

Comfyware's basis for trust is a bounded social context, governed by the people
actually in it. "Software you'd let your kids use" means an instance operated by
people you trust, under rules you understand. It does not mean every instance
must be suitable for your kids, or that trust depends on a universal ban on
anything someone might find objectionable.

- **Local sovereignty.** The host and community establish their own rules and
  culture. The software should make those choices understandable and give the
  people running the instance practical ways to uphold them.
- **Human-scale governance.** An identifiable person or small group is
  responsible for the place. Members should know who runs it and how to raise
  a concern. Clear responsibilities, membership controls, and moderation tools
  support that relationship.
- **No engagement imperative.** The software has no economic interest in making
  people angry, addicted, or perpetually present. Keep distraction low. No
  recommendation algorithms, ranked feeds, infinite scroll, streaks, popularity
  scores, or pressure to return. Conversations stay chronological, pages have an
  end, and any future notifications should be optional and recipient-controlled.
- **Bounded community.** The primary purpose is a place for a tribe: friends,
  family, or another community with a shared context. Audience acquisition,
  growth, and reach are not measures of success. An Open instance can welcome
  newcomers while retaining a clear purpose and accountable hosts.
- **Exit and ownership.** The community should not be held hostage by the
  software or its vendor. Being able to take your data is a core requirement.
  Keep hosting understandable, backups practical, and exports usable outside
  the application. Preserve the material and the relationships that give it
  meaning, so people can keep their contributions or move their community.
  imvault already provides account exports; Witmoot needs to meet that standard.
- **Tools, not morality.** Provide moderation and governance mechanisms without
  dictating what every community must consider acceptable. Hosts and members
  establish their house rules; the software helps them administer their place.

These are product constraints, including when considering future features.
Personal, Private, and Open change who can participate; all three share the
same respect for attention and local control.

The [data portability requirements](data-portability.md) describe what this
means for Witmoot and a proposed imageboard. They are a working design, including
the distinction between existing backup support and exports still to implement.

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

1. Local governance and ownership: visible house rules and responsible hosts,
   account recovery, member removal, invitation revocation, editing one's own
   posts, owner moderation, configurable rooms, and useful exports.
2. Shared memories: richer image descriptions and album browsing, while keeping
   sharing deliberate and access rules understandable.
3. Gentle conveniences: bookmarks, unread markers, optional email digests, and
   easier backups.

Separate groups inside one installation, per-conversation audience changes,
complex permission matrices, and private messaging need explicit product
decisions. This version does not imply those features or promise compatibility
with phpBB or vBulletin data.
