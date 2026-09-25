# Taking your data with you

Data portability is a core comfyware requirement. People should be able to keep
their contributions, and communities should be able to move their shared place.
An export should remain useful after the original server is gone.

This is a working design for Witmoot and a proposed companion imageboard.
Witmoot supports operator backups by copying its stopped data directory, and a
member can download their own contributions as a Zip with `manifest.json` and an
offline `archive.html`. A portable community archive and import workflows are not
implemented yet. The imageboard is a proposal.

## The example already in imvault

[imvault's account export](https://github.com/airencracken/imvault/blob/master/docs/accounts.md#exporting-an-account)
downloads a ZIP containing the account's original uploads and `manifest.json`.
The manifest describes files, names, descriptions, dates, content hashes,
visibility, tags, and albums. Original files use `files/<id>/<name>` paths,
preserving names without collisions. The download streams directly to the user.

The export excludes credentials and other accounts' material. Generated previews
and thumbnails are omitted; the account owner receives the originals. This is
an existing feature, separate from imvault's operator backup tooling.

## What portability needs to preserve

- **Contributions.** Post text and the files a person is entitled to export,
  with names, dates, descriptions, and available authorship information.
- **Relationships.** Which boards threads belong to, reply and quote references,
  image attachments, and their ordering. A folder of disconnected
  files would lose much of the meaning of a conversation.
- **Audience.** Preserve visibility and access information so moving a community
  does not silently make private material public.
- **Usability.** Use ordinary files and a documented, versioned manifest. A
  readable offline view should let people browse their archive without running
  the original application. Structured data should support other tools and
  future imports.

An operator backup restores a running installation, including its private
operational state. A portable export serves a different purpose: taking useful
material elsewhere. Both are necessary, and their contents should be explicit.

## Personal and community scope

A member should be able to export their own contributions without needing shell
access or an operator to assemble the download. A community archive needs an
authorized operator and a clearly stated scope. A member export must not quietly
include other people's private posts, credentials, or account information.

Keep enough thread context to identify where exported posts belong. The rules
for including other participants' contributions need to be explicit; permission
to read a conversation and ownership of its contents are distinct questions.

The proposed imageboard must also resolve how anonymous posters can retrieve
their own contributions. Posting identity and export authorization need to be
designed together without exposing identity to other readers.

## Images must remain usable

A portable archive needs image bytes within its authorized scope, with manifest
paths linking them to the corresponding posts. Live image URLs alone stop being
useful when the source server disappears. Missing or unavailable files must be
reported explicitly.

For locally stored imageboard uploads, retain the original files and their
relationship to the posts. For Witmoot's imvault integration, distinguish the
owner's originals from previews shared with a conversation. Attaching a private
image currently grants access to a preview, not to its original or camera
metadata. Export must respect that boundary. Fetching originals requires the
appropriate ownership or explicit permission.

imvault's account export can already return a member's uploaded originals.
Witmoot's member export records the conversations and the image references that
connect those shared previews to their context, without carrying the originals
or camera metadata. A community archive that carries a whole board, and
cross-application import, remain to be designed and implemented.

## Verification belongs in the feature

Export tests should cover original bytes where authorized, readable manifests,
stable references, duplicate filenames, Unicode, empty accounts, audience
boundaries, credential exclusion, and missing media. Interrupted or incomplete
exports must be distinguishable from successful, complete downloads.

An offline archive should render without reaching back to a running instance.
Import tests should verify preserved content, relationships, and audiences using
an export from a separate instance. A successful download alone does not prove
that a community can move.
