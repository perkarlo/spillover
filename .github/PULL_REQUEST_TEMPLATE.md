## What and why

<!-- One change. Link the issue if there is one: Fixes #N -->

## Checked

- [ ] `go vet ./... && test -z "$(gofmt -l .)" && go test ./... && ./.github/checks.sh` passes
- [ ] A test covers the new behaviour
- [ ] README and the `Unreleased` section of CHANGELOG.md updated for a user-facing change
- [ ] No token, credential or unredacted log in the diff or the description
