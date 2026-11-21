# Changelog

All notable changes to spillover. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `spillover list`, the `LOGIN` column of `spillover usage --all` and `/_status` (`loginExpiresAt`) show when each login must be signed in again. The status line adds `· login <account> 2d3h` in the last three days.

### Changed

- A login past its expiry is skipped without a refresh attempt, and the log names the command that signs it in again.
- The project lives at github.com/perkarlo/spillover. The service label is `io.github.perkarlo.spillover`; `./install.sh` moves an existing `co.birtik.spillover` service over.

### Security

- The proxy answers only requests addressed to the loopback (`Host` 127.0.0.1, localhost or [::1]) and refuses any request a browser sent (`Origin` or `Sec-Fetch-Site`). Before, a web page could use DNS rebinding to send messages requests on your accounts and read the replies.
- Paths with `.` or `..` segments are refused, so a request cannot reach past `/v1/messages` with an account's token.

## [0.8.0] - 2026-11-06

### Added

- `spillover use` picks the active account; `/_status` counts the replies each account served.
- `spillover usage` shows each account's 5-hour and weekly windows.
- `spillover statusline` puts the session's account and windows in Claude Code's status line.

### Changed

- Renamed from limitbreak; `./install.sh` moves an installed limitbreak over.
- The terms caveat in the README cites the Usage Policy effective November 12, 2026 and the Consumer Terms.

### Removed

- The `video` command and the README animation.

## [0.1.0] - 2026-10-25

First release as limitbreak by Radu Lupu: the account-switching proxy, `add`, `list` and `remove`, prebuilt binaries.

[Unreleased]: https://github.com/perkarlo/spillover/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/perkarlo/spillover/compare/v0.1.0...v0.8.0
[0.1.0]: https://github.com/perkarlo/spillover/releases/tag/v0.1.0
