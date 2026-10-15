# Unreleased

## Recent posts

Recent shows individual posts and replies, newest first, with their author,
board, conversation, message text, shared images and discussion references.
Links open the exact message on its conversation page. Pages contain twenty
posts with previous/next links; opening Recent does not record read status or
change messages. Edits retain the original posting position.

The feed applies the existing board, group, individual-access and conversation
audience rules before pagination. Removed messages and archived boards stay out.
Board listings and conversation search remain available in their usual places.

Migration 020 adds a partial index on message creation time and ID. It does not
rewrite messages or change their IDs. Startup saves a pre-migration snapshot as
usual. Keep a verified backup before upgrading; going back to an older binary
requires restoring the matching pre-upgrade database.
