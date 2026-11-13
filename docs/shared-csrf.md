# Shared CSRF derivation

Witmoot derives form tokens with Comfylib's `token.SessionCSRF`, so existing
sessions keep their tokens.

To develop against a sibling Comfylib worktree, use an untracked `go.work`:

```sh
go work init . ../comfylib
go work edit -replace=github.com/airencracken/comfylib=../comfylib
```

Release checks run with `GOWORK=off` against the published module. Keep
`go.work` and local replacements out of commits.
