# Security policy

spillover sits between Claude Code and Anthropic and reads the OAuth tokens of every Claude login on the machine. A flaw here can hand someone your accounts, so reports are welcome.

## Reporting a vulnerability

Use GitHub's private reporting: **Security > Report a vulnerability** on this repository. Please do not open a public issue for a vulnerability.

Include the release or commit you ran, your OS, and the steps that show the problem. Never send a real token, a credential file or a log line that contains one; a redacted value is enough.

This is a small project maintained in spare time. Reports are answered as fast as that allows, and a confirmed issue gets a fix and an advisory before anything else.

## In scope

- A token reaching output, a log, an error message or `GET /_status`.
- The proxy reachable from another host, or account switching (`POST /_use`) triggered by a web page.
- A request sent on an account it should not go on, or anything other than `/v1/messages*` sent on an account's token.
- spillover writing, rotating or refreshing a credential itself instead of leaving it to Claude Code.

## Out of scope

- Whether using several subscriptions this way is allowed: see "Terms of service" in the README.
- Bugs in Claude Code itself or in Anthropic's services.

## Supported versions

Only the latest release gets fixes.
