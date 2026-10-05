# Third-party components

Witmoot's own code is licensed under AGPL-3.0-or-later. The components below
retain their upstream licenses.

- HTMX 2.0.10: https://github.com/bigskysoftware/htmx/tree/v2.0.10
  Vendored as `internal/forum/static/htmx.min.js`; its 0BSD license is in
  `internal/forum/static/htmx.LICENSE`.
- Go dependencies are versioned in `go.mod` and verified with `go.sum`.
- comfylib (AGPL-3.0-or-later, by the same author) supplies the Bubblewrap
  sandbox, service configuration, mail, key file, token, client address and
  proxy configuration packages shared with Imvault. It uses only the Go
  standard library.
- The interactive `witmoot admin` view uses Charm's Bubble Tea, Bubbles, and
  Lip Gloss (all MIT). They are Go dependencies pinned in `go.mod`.
- Playwright is a development-only browser testing dependency under
  `scripts/browser/`.

The binary embeds Go's `time/tzdata` fallback, which contains IANA timezone data
from the public-domain timezone database. The host's timezone data takes
precedence when available.
