# Taking your data with you

Members can download their contributions from Account. Owners can download the
whole community from Settings. Both ZIPs contain `manifest.json` and a readable
`archive.html` that works offline. Import workflows are not implemented yet.

## Member archives

An account archive contains that member's messages, authorship and timestamps,
board and conversation references, and the member's avatar when present. Other
people's replies are excluded. Removed messages keep their position and
`removed: true`, with only the removal placeholder and no former image references.

Members retain their own words from conversations they can no longer read.
Those entries omit current board names, topic titles and image references, so
an export cannot recover material whose audience has changed.

Available Imvault shared previews are copied into `images/`, with manifest paths
and links to their Imvault pages. Previews that cannot be retrieved keep a link
and an explicit unavailable note. Private image attachments grant access to
shared previews, not originals or camera metadata. Download originals through
[Imvault's account export](https://github.com/airencracken/imvault/blob/master/docs/accounts.md#exporting-an-account).

## Community archives

Only owners can use `/community/export`. The `witmoot-community-export` version
1 manifest includes every board and conversation, including private and empty
boards, all messages with author IDs and names, and available shared previews.
It also records site mode, public contact and house rules, board descriptions,
ordering, restricted and archived states, groups, group membership, and both
group and individual board grants. Explicit access-denial overrides are kept.
Moving material later must preserve these audiences rather than default to
public access.

Passwords, sessions, account email addresses, reset links and image credentials
are excluded. Treat the archive as private: it includes the community's private
conversations. It is a portable record, not a complete operational backup or an
import file with a currently supported importer.

## Failures and storage

The applications prepare exports in private temporary files before successful
download headers. A preparation failure returns an error, and a failed download
connection is aborted. Only one export is prepared or downloaded at a time.
Hosts need enough temporary disk space (`TMPDIR`) for the ZIP. Temporary files
are removed after success, failure or cancellation.

Unavailable Imvault previews are reported within a Witmoot archive instead of
preventing the download of people's words. The manifest's `path` means bytes
are included; `note` explains why they are not. Existing exports and backups are
not rewritten when somebody later removes a message or deletes an account.

## Leaving

Account offers **Delete your account**. It requires the current password and
typed username. Deletion clears sign-ins, profile information, image connections,
invitations and access grants. The member's messages become anonymous placeholders
and their conversation titles become "Conversation". Other people's replies
and message positions remain. Images on Imvault are managed separately.

The last owner cannot leave until another owner takes over. Existing backups,
other people's quotations and references, and downloaded copies remain; deletion
cannot recall them. Owners deleting an entire board see the scope and a link to
export the community first. Archiving a board preserves its conversations.

## Operator backups

Stop Witmoot before copying the entire data directory, including `imvault.key`.
The automatic pre-migration database snapshot is only a rollback aid, not this
full backup. Restore with the server stopped and preserve ownership and private
permissions. Back up Imvault separately using its verified backup command.

An operational backup includes credentials and private state needed to restore
the running installation. Portable archives deliberately omit those secrets.
Keep both private and periodically test restoration.
