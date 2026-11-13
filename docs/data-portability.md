# Taking your data with you

Members download their own posts from **Your data** on the account page.
Owners download the whole community from **Settings**. Both ZIPs contain
`manifest.json` and an `archive.html` you can read offline. Witmoot cannot
import these archives yet.

## Member archives

An account archive holds your posts with their timestamps, the boards and
conversations they belong to, your profile, and your avatar. Other people's
replies are left out. A removed post keeps its place, marked
`removed: true`, with only the placeholder text.

You keep your own words from conversations you can no longer read. Those
entries leave out the board name, conversation title and images.

Shared Imvault previews you attached are copied into `images/`, with links to
their Imvault pages. A preview that cannot be fetched keeps its link and a
note saying it is unavailable. Archives hold previews only. Download originals
through
[Imvault's account export](https://github.com/airencracken/imvault/blob/master/docs/accounts.md#exporting-an-account).

## Community archives

Only owners can use `/community/export`. The archive holds every board and
conversation, private and empty ones included, every post with its author,
and available shared previews. It also records the site mode, public contact
and house rules, board descriptions and order, restricted and archived boards,
groups and their members, and every board grant and denial. Anyone moving the
material elsewhere has what they need to keep private conversations private.

Passwords, sessions, email addresses, reset links and Imvault keys are left
out. Keep the archive private: it contains private conversations. It is a
record you can keep or move. It is not a backup.

## Preparing a download

Witmoot builds each archive in a temporary file before sending it, one at a
time. The host needs enough space in `TMPDIR` for the ZIP. If preparation
fails you see an error; a failed download is cut off and never left looking
complete.

A missing Imvault preview never blocks the download. In the manifest, `path`
means the image is included and `note` explains why it is not. Later removals
and account deletions do not change archives or backups already made.

## Leaving

**Delete your account** under **Your data** asks for your password and your
typed username. It clears your sign-ins, profile, image connection,
invitations and access grants. Your posts become anonymous placeholders and
your conversation titles become "Conversation". Other people's replies stay
where they are. Your images in Imvault are not touched.

The last owner must hand over to another owner before leaving. Deletion cannot
recall backups, quotations or downloaded copies. When an owner deletes a whole
board, Witmoot shows what will go and links to the community export first.
Archiving a board keeps its conversations.

## Operator backups

A community archive leaves out the credentials needed to restore a running
server. For that, back up the whole data directory, including `imvault.key`;
see [logs and backups](deployment.md#logs-and-backups). Keep both kinds
private and test a restore now and then.
