# Bring a link to the conversation

Songstead and Imvault can send a link to Witmoot as a draft through
`GET /share?source=URL&title=TITLE&body=TEXT`. Sign in, then pick a board to
start a new conversation, or pick a recent conversation to add a message.
Review who can read it and post as usual. Opening `/share` never posts
anything, and the normal board access and archive rules apply.

## Songstead

A Songstead draft carries public music information, the listening link and a
link back to the recommendation. It leaves out notes, listening state and
playback position. Pasting a Songstead recommendation link into a message also
adds a small reference. Opening it needs a Songstead sign-in with access to
that recommendation. Witmoot never contacts Songstead.

## Imvault albums

Album handoffs carry the link only. Private titles, counts, descriptions,
covers and photos never reach the message.

When a message contains an album link from the server in
`WITMOOT_IMVAULT_URL`, Witmoot adds a reference and a
**View current public album preview** link. Reading the conversation does not
contact Imvault. Choosing the link asks Imvault for the album's current title
and image count through the message author's Imvault connection.

Witmoot shows a preview only while the album is public, the message is not
removed, and the reader can see the conversation. Members-only and private
albums show nothing. If Imvault is down, the author has disconnected, or the
album has changed, the conversation and link stay readable. Witmoot copies no
covers or photos and does not cache previews; open the preview again to see
changes.

There is nothing to configure beyond the [Imvault connection](imvault.md).
