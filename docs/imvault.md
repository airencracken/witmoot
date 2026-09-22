# Images at the table

Witmoot can use an imvault server for images. The integration is optional:
text conversations work with no image service configured.

## Connect the two

1. Run an imvault instance reachable from the Witmoot server. This integration
   uses imvault's v1 account, files, upload, delete, and preview endpoints.
2. Set `WITMOOT_IMVAULT_URL` to that instance's base URL and restart Witmoot.
   Use HTTPS when the connection crosses a network you do not control.
3. In imvault, create an API key under **Settings → API keys**.
4. In Witmoot, open **Images**, enter that key, and choose **Connect imvault**.

Each member connects their own account. Witmoot uses the key's imvault permissions;
use an ordinary account unless administrative access is intended. The owner
configures one server for the board. Members cannot send keys to arbitrary URLs.
API responses cannot redirect Witmoot to another server.

## Three ways to share

Open **Add images** when starting a conversation or replying:

- **Upload images** sends new files to your connected imvault account with
  `visibility=private` and `metadata=hidden`, regardless of imvault's defaults.
- **Choose from your library** shows your existing images, with search and
  pagination. Selections survive moving between picker pages. Without
  JavaScript, the first page can still be selected; use **Open your full library**
  to search further and copy a link into your draft.
- **Paste imvault image links** accepts image pages and raw, preview, or thumbnail
  links from the configured server. Public thumbnails need no connection.
  Other images must be accessible through your connected account's files API.

A message may contain four images, each up to 8 MiB. Supported uploads are JPEG,
PNG, WebP, and GIF. Messages still require text. If validation or an image upload fails, text and
valid image selections remain, but browser upload fields must be selected again.

Still images use imvault's generated preview. Animated images use a still
thumbnail. Witmoot does not fetch originals or expose camera metadata. This is
image sharing, not a general attachment store or an embedded imvault browser.

## Who can see a shared image

Attaching an image explicitly shares its preview with everyone allowed to read
that conversation, even when the image is private in imvault. In Open mode,
new conversations and their attached previews are public. Existing topic
audiences are preserved through mode changes; private images attached to a
members-only topic stay behind sign-in. Open registration still lets anyone
become a member, as explained in the board settings.

Witmoot serves images through local `/images/...` URLs. Each request checks the
current board mode and conversation audience before accessing imvault. Private
and Personal modes close access to formerly public images along with the board.
The proxy does not forward browser cookies, expose API keys, or store image
bytes in SQLite. Responses use `Cache-Control: no-store`.

Existing public imvault links remain public at their original URLs. Sharing one
in a private conversation does not change its visibility in imvault. Anyone
who has already viewed an image can keep a copy.

## Connections, backups, and failures

API keys are encrypted with AES-GCM, bound to the Witmoot member and configured
server. The random encryption key is stored as `imvault.key` in Witmoot's data
directory with owner-only file permissions. Back up the entire data directory,
including this key. A database backup alone cannot restore the connections.
Back up imvault separately: Witmoot stores references, and depends on imvault
for the images themselves.

**Disconnect imvault** deletes the member's stored API key without deleting their
files or conversations. Images that relied on that connection stop loading;
public pasted images continue to work. Reconnecting restores accessible images.
Revoking the key in imvault also stops credentialed access. A different account
cannot use an old reference unless imvault grants that account access to the file.
Changing `WITMOOT_IMVAULT_URL` disables references to the old server.

If imvault is unavailable, conversations remain readable and images may be
unavailable. Posting with an unavailable attachment fails with an explanation;
text-only posting remains possible. Library requests and media responses are
bounded in size and time.

Post text and attachment references commit together in SQLite. If a post fails,
Witmoot tries to delete only files newly uploaded for that failed submission;
it never deletes selected library files or pasted images. The two servers do
not share a transaction. An interrupted upload response, process crash, or failed
cleanup can leave an unattached private file in imvault. Cleanup failures with
known IDs are logged for the operator to reconcile.
