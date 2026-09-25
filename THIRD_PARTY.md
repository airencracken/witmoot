# Third-party components

Witmoot's own code is licensed under AGPL-3.0-or-later. The components below
retain their upstream licenses.

- HTMX 2.0.10: https://github.com/bigskysoftware/htmx/tree/v2.0.10
  Vendored as `internal/forum/static/htmx.min.js`; its 0BSD license is in
  `internal/forum/static/htmx.LICENSE`.
- Go dependencies are versioned in `go.mod` and verified with `go.sum`.
- The interactive `witmoot admin` view uses Charm's Bubble Tea, Bubbles, and
  Lip Gloss (all MIT). They are Go dependencies pinned in `go.mod`.
- Playwright is a development-only browser testing dependency under
  `scripts/browser/`.
