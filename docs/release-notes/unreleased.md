# Witmoot, unreleased

These changes are on the main branch and not yet in a release.

## Metrics without names

Witmoot no longer records who signs in or what anyone reads. Migration 021
deletes the per-member sign-in rows and the per-message and per-image view rows
that 0.22.0 and 0.23.0 stored. The owner metrics page keeps its aggregate counts:
members, boards, conversations, messages, new messages in the past seven UTC
days, and database size, plus backup status and configured apps. The sign-in
and most-viewed lists are gone.

Take a backup before upgrading. The migration cannot be undone, and an older
binary refuses the migrated database; restore the backup to go back.

## Default listen address

The binary now listens on `127.0.0.1:8082` when `WITMOOT_ADDR` is not set. This
affects anyone running `witmoot` without an explicit address, including
`make run`; set `WITMOOT_ADDR=127.0.0.1:8080` to keep the old port. Packaged
OpenRC and systemd services already set 8082, and containers set `:8080` inside
the container, so neither changes.

## Around the board

- **Your account** is grouped into Profile, Times and dates, Sign-in, and Your
  data. **Show animated avatars** is part of the avatar form, and size limits
  appear in the error when a picture breaks them.
- **Settings** starts with the site's name and mode. Version, metrics, and
  automatic backups moved to a **Server** section at the bottom.
- Board settings show group and individual grants only after you choose
  **Selected members only**.
- The home page drops its site access block; the sidebar explains who can join.
- Invitation and reset links appear once, with a **Copy** button.
- Sharing a link from another Comfyware app offers a list of recent
  conversations to join, in place of a conversation number.
- Pages say "conversation" and "message" throughout, and "Recent posts" is now
  **Recent messages**.
- Downloaded archives use Witmoot's colours and headings.
- Invitation, account, and metrics pages from Comfylib match Witmoot's colours
  and Georgia headings.
