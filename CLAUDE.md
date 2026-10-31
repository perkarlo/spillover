# CLAUDE.md

Project guide for Claude Code. User-facing material lives in README.md; this file is the record
of rules, contracts and the reasons behind them.

## Project

limitbreak lets one Claude Code install use several Claude logins. A local proxy sends each
messages request on the active account's own token; when Anthropic says that account's 5-hour or
weekly window is spent, it replays the same request on the next account before Claude Code sees
the error. One Go binary, standard library only, is both the proxy (`limitbreak`, `limitbreak
serve`) and the CLI. Forked from radu2lupu/limitbreak (MIT).

```
.
├── main.go          # proxy: routing, account discovery, tokens, switching, /_status and /_use
├── cli.go           # commands add, list, use, remove and the usage text
├── usage.go         # `limitbreak usage`: each account's windows from /api/oauth/usage
├── *_test.go        # tests against stand-in upstreams in temp home dirs
├── install.sh       # builds or installs the binary, writes the launchd / systemd user service
├── shell.sh         # the `claude` function: through the proxy when it runs, plain when not
├── README.md        # user documentation
├── CLAUDE.md        # this file
├── LICENSE          # MIT, upstream copyright
└── .github/
    ├── checks.sh    # consistency checks; run it locally too
    └── workflows/   # ci.yml (vet, gofmt, test, checks), release.yml (binaries on a v* tag)
```

**After every change, run:** `go vet ./... && test -z "$(gofmt -l .)" && go test ./... && ./.github/checks.sh`

Then `./install.sh` puts the build in service. The restart forgets `limitbreak use`: pick the
account again if it was not the default.

## Invariants

Deliberate decisions. Before changing one, read why it is here.

| # | Rule | Why |
| --- | --- | --- |
| 1 | A token never reaches output, a log line, an error message or `/_status` | Tokens are read in place from Claude Code's own store; whoever sees one holds the account. `account.Dir` and `Path` are `json:"-"`, `readUsage` masks a token the server echoes |
| 2 | Only `/v1/messages*` and the `/usage` read (`GET /api/oauth/usage`, any query) go on an account's token; every other path passes through with Claude Code's own credentials | Connectors, MCP, artifacts and the profile belong to the login Claude Code holds; moving them breaks them or mixes two accounts in one session. The usage read follows the account that is spending (DEV-922) |
| 3 | `CLAUDE_CODE_OAUTH_TOKEN` is never set for Claude Code | With it Claude Code drops its claude.ai connectors and local MCP servers |
| 4 | Only Claude Code refreshes a login: one `claude -p` under that config dir. limitbreak never writes a credential | Refresh tokens rotate; a second refresher logs Claude Code out |
| 5 | Never worse than no proxy: when no account can take a request, it goes on as Claude Code made it | The proxy sits in front of every session; its failure must cost nothing. `shell.sh` runs plain `claude` when the proxy is down |
| 6 | Only a window rejection switches: `429` with `anthropic-ratelimit-unified-status: rejected` | A plain 429 clears in seconds and goes back to Claude Code untouched |
| 7 | `/_use` takes JSON only and the proxy answers it, preflight included | A web page can POST a form to localhost but not JSON without a preflight, so no page can switch accounts |
| 8 | Listens on 127.0.0.1 only | Whoever reaches the port spends the accounts |
| 9 | Standard library only | One small binary that holds tokens; nothing else to audit or update |
| 10 | The account choice and spent marks live in memory | A restart starts from the default account; an account still spent says so on its next reply |

## Placing a request

| Step | Behaviour |
| --- | --- |
| Accounts | The default login (`~/.claude`) and every `~/.claude-<name>` dir with a credential, in name order; cached 30 s |
| Client | `/_c/<client>/...` names a client with its own active account (the prefix is stripped); any other path is `shared` |
| Token | Every source is read (Keychain items, `.credentials.json`); the unexpired token that lasts longest wins. None: one refresh, then read again |
| Spent | Marked until `anthropic-ratelimit-unified-reset`, an hour when unsaid. A login that gives no token is skipped for 5 minutes |
| Replay | The buffered body goes to the next usable account, each account at most once per request; then pass-through (invariant 5) |
| Usage read | On the active account's live token, no refresh, never counted as served. No token, or a 401 or 403: sent again as Claude Code made it |

## Conventions

| Topic | Rule |
| --- | --- |
| Language | Code, comments, CLI output, README, this file and commits in English |
| Commits | `component: what changed (DEV-N)`, concrete, no adjectives: `proxy: skip a login that gives no token for 5 minutes (DEV-921)`. Components: `proxy`, `cli`, `usage`, `install`, `docs`, `ci` |
| Comments | One to three lines saying what; the why goes to Defense lines. `checks.sh` rejects 4+ comment lines in a row |
| Commands | A command is a `case` in `runCommand`, a line in the `usage` text and a mention in README; `checks.sh` compares the three |
| Exit codes | `0` done, `1` error (`error: <message>` on stderr), `2` usage mistake |
| Tests | `httptest` stand-ins for Anthropic and temp home dirs with `credentialsFile`; a test sets `claude = ""` when a token could expire, so no real `claude` runs |
| Text | No em dash anywhere (`checks.sh`) |
| Upstream | radu2lupu/limitbreak, MIT: keep LICENSE and its copyright line. No PR upstream without the owner's approval |

## Defense lines

The long reason behind short lines in the code. Read before simplifying one away.

| Where | Line | Why it is there |
| --- | --- | --- |
| `hopHeaders` | `Authorization` and `X-Api-Key` dropped on a request sent on an account | The account's token must be the only credential upstream sees |
| `ServeHTTP`, `passThrough` | `Accept-Encoding: identity` | `relay` copies bytes as they arrive and does not decode; an encoded body would reach Claude Code without its `Content-Encoding` |
| `ServeHTTP` | the body is read whole before the first try | The same bytes are sent again on the next account |
| `ServeHTTP` | `for range len(...) + 1` | Each account is tried once per request, so a run of spent replies ends in pass-through |
| `ServeHTTP` | no token: `spend(..., unavailableBackoff)` | A refresh can take up to 45 s; without the backoff every request would wait on it again |
| `usageRead` | `readToken`, not `token` | Claude Code gives the read 5 s; a refresh can take 45 s |
| `usageRead` | a 401 or 403 is sent again with Claude Code's own credentials | Claude Code sends the read with `refreshOAuth`: a 401 would make it refresh its own login, which did nothing wrong |
| `newProxy` | `CheckRedirect` returns `ErrUseLastResponse` | A redirect goes back to Claude Code as it is; the proxy does not follow it with an account's token |
| `relay` | flush after every read | A stream of events is not held back |
| `accounts.list` | cached for `discoveryInterval` | Each login costs `security` calls; a new login still shows up within 30 s |
| `credentialSources` | the user's Keychain item first, every other source kept | An older sign-in can leave a stale item or file under the same name; `readToken` keeps the token that lasts longest |
| `claudeEnv` | `ANTHROPIC_BASE_URL` and `CLAUDE_CODE_OAUTH_TOKEN` removed | The refresh must reach Anthropic as that login: through the proxy it would loop, with an injected token it would refresh nothing |
| `claudeEnv` | `CLAUDE_SECURESTORAGE_CONFIG_DIR` set next to `CLAUDE_CONFIG_DIR` | The refreshed login must land in the Keychain item `keychainService(dir)` names |
| `accounts.use` | the chosen account loses its spent mark | A choice is honoured; if the window is still spent, the next reply marks it again |
| `addAccount` | the dir is removed only when this call created it | A failed sign-in never deletes an existing login |
| `removeAccount` | `default` refused | It is Claude Code's own login; `claude auth logout` signs it out |
| `newWindow` | reset rounded to the minute | The endpoint stamps a reset up to a second off (05:59:59.967 or 06:00:00.513 for 06:00) |
| `usagePath` | `/api/oauth/usage` | The endpoint Claude Code's `/usage` reads; not a documented API, it may change |
| `install.sh` | old labels booted out and removed | Earlier builds ran as `com.hicomputers.claude-account-proxy` and `dev.claude-account-proxy`; two services would fight for the port |
| `install.sh` | `sleep 1` after `launchctl bootout` | Bootstrapping while the old job is still going away fails |
| `install.sh` | the old `claude-account-proxy-shell.sh` becomes a one-line shim | A profile line from an earlier build keeps working |
| `shell.sh` | `curl -m 1` on `/_status` | A stopped proxy costs at most a second before plain `claude` runs |
| `ci.yml` | Go 1.24 | Go 1.22 test binaries abort on current macOS runners; `go.mod` stays at 1.22 |

## Doc maintenance

| Change | Update | CI catches it |
| --- | --- | --- |
| Command added, renamed or removed | `runCommand`, the `usage` text, README | yes, checks.sh |
| Port | `defaultPort`, `shell.sh`, README, this file | yes, checks.sh |
| Service label | `install.sh`, README Uninstall, this file | yes, checks.sh |
| What goes on an account's token | invariants 2 and 6, README "What it does and does not touch" | no |
| `/_status` or `/_use` shape | README | no |
| New reason behind a code line | one-line comment and a Defense lines row | yes, 4+ comment lines rejected |
| File added or removed | the tree at the top | no |
| Run line | the "After every change" line and `ci.yml`, same commands | yes, checks.sh |

The default port is 48617 and the launchd label `dev.limitbreak`; `checks.sh` holds both to the
code. CI runs no real login: a switch on live accounts is still checked by hand after `./install.sh`.
