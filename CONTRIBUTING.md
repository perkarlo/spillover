# Contributing

Thanks for helping. spillover is small on purpose: a handful of Go files, the standard library only, one binary that holds tokens. Changes that keep it that way are the easiest to merge.

## Before you start

- For anything beyond a small fix, open an issue first so the shape is agreed before the work.
- Read the invariants in [CLAUDE.md](CLAUDE.md): the project's rules and the reason behind each. A change that breaks one needs a strong case.
- Security problems go through private reporting, see [SECURITY.md](SECURITY.md).

## Develop

```sh
go build .
go vet ./... && test -z "$(gofmt -l .)" && go test ./... && ./.github/checks.sh
./install.sh            # puts your build in service (launchd on macOS, a systemd user unit on Linux)
```

Tests run against `httptest` stand-ins for Anthropic in temporary home directories: no real login, no network. A switch on live accounts is still checked by hand after `./install.sh`.

## Pull requests

- One change per pull request, with a test when behaviour changes.
- Commit messages: `component: what changed`, concrete and without adjectives. Components: `proxy`, `cli`, `usage`, `install`, `docs`, `ci`.
- A user-facing change updates the README and the `Unreleased` section of [CHANGELOG.md](CHANGELOG.md).
- No em dash anywhere; `checks.sh` rejects it.
- Never paste a token, a credential file or a log line that contains one.
