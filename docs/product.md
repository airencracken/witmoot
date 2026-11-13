# A place for your people

Witmoot is Comfyware: software for a family, an extended family, or a group of
friends who want somewhere of their own. Like Imvault, it draws on the
personal spirit of the IndieWeb, the control of self-hosting, and a focus on
small communities. The people who use it are the people it serves and the
people who run it. A familiar forum layout gives conversations a home you can
find again months later.

## What Comfyware means

Trust comes from a small social circle run by the people in it. "Software
you'd let your kids use" means an instance run by people you trust, under
rules you understand. It does not mean every instance suits children, or that
the software bans whatever someone might object to.

- **Local sovereignty.** The host and community set their own rules and
  culture. The software makes those choices clear and gives hosts practical
  ways to keep them.
- **Human-scale governance.** A named person or small group looks after the
  place. Members know who runs it and how to raise a concern.
- **No engagement imperative.** Witmoot gains nothing from making people
  angry, hooked or always online. No recommendation algorithms, ranked feeds,
  infinite scroll, streaks, popularity scores or nudges to come back.
  Conversations stay in order, pages end, and any future notifications will be
  optional and in the recipient's control.
- **Bounded community.** Witmoot is a place for a tribe: friends, family, or
  another group with shared context. Growth and reach are not the goal. An
  Open board can welcome newcomers and still have a clear purpose and
  accountable hosts.
- **Exit and ownership.** Your community is never held hostage by the software.
  Hosting stays understandable, backups practical, and exports readable outside
  the app, so people can keep their words or move their community. Members can
  download their own archive, and owners can download the whole community with
  its private audiences and access rules. Importing is still to come.
- **Tools, not morality.** Witmoot gives hosts moderation tools without
  deciding what every community must accept. Hosts and members write their
  own house rules.

These guide every new feature. Personal, Private and Open change who can take
part; all three respect people's attention and local control. See
[taking your data with you](data-portability.md) for what exports cover today.

## Inspirations

- [Imvault](https://github.com/airencracken/imvault): the companion Comfyware
  app and a home for the group's images.
- [copyparty](https://github.com/9001/copyparty): sharing from hardware people
  already have. The lesson for Witmoot is to stay easy to run and easy to use.
- phpBB and vBulletin: familiar rooms, lasting conversations, and a shared
  place people return to in their own time.

## The first shape

One installation serves one board. Owners choose a mode in **Settings**:

- **Personal** is an owner-only notebook. Invitations are off and every owner
  has access.
- **Private** is an invited group.
- **Open** offers public reading and registration. Posting needs an account.

Boards hold conversations, and replies follow in order. The mode sets the
audience for new conversations: owners, members, or everyone. A conversation
keeps its audience through later mode changes. Opening registration lets new
members read older members-only conversations, and Settings says so. Private
and Personal close public access to the whole board; Personal closes it to
members too. There is no anonymous posting.

With an Imvault connection, members upload, choose or paste images. Sharing
an image is an explicit part of posting, and it follows the conversation's
audience. New uploads stay private in Imvault. See [Imvault](imvault.md).

The Moot Knight sits at a small round table with a book and quill: a welcoming
host for a place built around gathering and keeping conversations.

Witmoot should feel warm, readable and unhurried. An honest empty page beats
made-up activity. Counts describe the gathering and never become a contest.
Owners see only totals on the metrics page; Witmoot does not track who signs
in or what anyone reads.

## Technology choices

- Go's HTTP server and HTML templates handle routing, validation and
  rendering.
- SQLite holds all state, with foreign keys, transactions, WAL journaling and
  embedded migrations. One database connection keeps writes simple for small
  groups.
- HTMX enhances links and forms. Everything works without it.
- Templates and assets are embedded, so the binary needs no CDN or frontend
  build.

## Where to go next

Witmoot already has boards, groups and per-member permissions, member and
community archives, account deletion, house rules and owner names, member
suspension, post removal, and a private owner activity log. Password
changes, reset links and `set-password` cover account recovery.

1. Ownership and recovery: self-service email recovery and imports.
2. Shared memories: richer image descriptions and album browsing, with
   sharing kept explicit and access rules clear.
3. Gentle conveniences: bookmarks, unread markers and optional email digests.

Per-conversation audience changes and private messages need product decisions
first. Witmoot does not promise to import phpBB or vBulletin data.
