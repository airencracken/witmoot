# Bring a link to the conversation

`GET /share?source=URL&title=TITLE&body=TEXT` prepares a draft from a deliberately
shared source. The shared Comfylib reference package builds and validates this
browser handoff. Sign in, choose a board for a new discussion or an existing
topic ID for a reply, review its audience, and use the ordinary post action.
GET never writes a topic or reply. Topic/board access, archived/read-only rules,
CSRF protection and audience checks remain the existing ones.

Songstead drafts include public music information, the listening URL and a link
back to the recommendation. They do not include sender/group notes, listening
state or manual positions. Pasting a Songstead recommendation URL into an
existing message also yields a small reference; opening the source requires its
own sign-in and recommendation permission. Witmoot does not fetch Songstead or
need a configured Songstead service, and source links work without previews.

imvault album handoffs are link-only. Private titles, counts, descriptions,
covers and album contents are never copied into messages by the handoff.
For a pasted album URL matching `WITMOOT_IMVAULT_URL`, Witmoot adds a source
reference and an explicit **View current public album preview** link. Opening a
topic does not contact imvault. A requested preview uses the post author's
existing encrypted imvault connection and the authenticated
`GET /api/v1/albums/{slug}/preview` API. No additional credentials are stored.

Every preview request first checks local post/topic/board access and verifies
that the exact album URL still appears in the unremoved post. The API requires
album ownership and returns metadata only while the album is public. Witmoot
also rejects nonpublic responses and discards metadata on malformed, revoked or
unavailable responses. It never shows private or members-only album metadata,
even to a signed-in Witmoot reader. Only public images are counted; there is no
cover proxy or album-content copy. Responses are no-store, with no preview cache.
An unavailable, disconnected or older imvault keeps the topic and source link
readable. Revisit the preview to see updated public album titles/image counts.

No new Witmoot configuration or database migration is required. Existing image
connections, optional-service configuration, backups and disconnect semantics
remain as documented in [imvault.md](imvault.md). Publish the Comfylib v0.1.1
`reference` package before releasing this app; resolve real module checksums and
run the clean release checks with GOWORK disabled.
