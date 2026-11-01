# shellcheck shell=bash
# Source this from ~/.zshrc or ~/.bashrc: `claude` goes through the proxy when it runs, plainly when not.
claude() {
  local port="${SPILLOVER_PORT:-48617}"
  if curl -s -m 1 -o /dev/null "http://127.0.0.1:${port}/_status"; then
    ANTHROPIC_BASE_URL="http://127.0.0.1:${port}" command claude "$@"
  else
    command claude "$@"
  fi
}
