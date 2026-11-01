#!/usr/bin/env bash
# Installs spillover as this user's login service (launchd on macOS, systemd user unit on Linux).
# Usage: ./install.sh [--dry-run] [--binary <prebuilt spillover>]
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
bin="${HOME}/.local/bin"
dry=0
binary=""
while (($#)); do
  case "$1" in
    --dry-run) dry=1 ;;
    --binary) binary="$2"; shift ;;
    *) echo "unknown argument: $1" >&2; exit 1 ;;
  esac
  shift
done
run() { if ((dry)); then echo "+ $*"; else "$@"; fi; }

run mkdir -p "$bin"
if [[ -n "$binary" ]]; then
  run install -m 755 "$binary" "$bin/spillover"
else
  run bash -c "cd '$here' && go build -o '$bin/spillover' ."
fi
run install -m 644 "$here/shell.sh" "$bin/spillover-shell.sh"

case "$(uname -s)" in
  Darwin)
    plist="${HOME}/Library/LaunchAgents/co.birtik.spillover.plist"
    if ((dry)); then echo "+ write $plist"; else
      mkdir -p "$(dirname "$plist")"
      cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>co.birtik.spillover</string>
  <key>ProgramArguments</key><array><string>${bin}/spillover</string></array>
  <key>EnvironmentVariables</key><dict><key>PATH</key><string>${bin}:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string></dict>
  <key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
  <key>StandardErrorPath</key><string>${HOME}/Library/Logs/spillover.log</string>
</dict></plist>
PLIST
      # Earlier builds ran under other names; stop and remove them so two services never fight for the port.
      for label in com.hicomputers.claude-account-proxy dev.claude-account-proxy dev.limitbreak; do
        old="${HOME}/Library/LaunchAgents/${label}.plist"
        if [[ -f "$old" ]]; then launchctl bootout "gui/$(id -u)/${label}" 2>/dev/null || true; rm -f "$old"; sleep 1; fi
      done
      launchctl bootout "gui/$(id -u)/co.birtik.spillover" 2>/dev/null && sleep 1 || true
      launchctl bootstrap "gui/$(id -u)" "$plist"
    fi ;;
  Linux)
    unit="${HOME}/.config/systemd/user/spillover.service"
    if ((dry)); then echo "+ write $unit"; else
      mkdir -p "$(dirname "$unit")"
      for old in claude-account-proxy limitbreak; do
        systemctl --user disable --now "${old}.service" 2>/dev/null || true
        rm -f "${HOME}/.config/systemd/user/${old}.service"
      done
      cat > "$unit" <<UNIT
[Unit]
Description=spillover

[Service]
ExecStart=${bin}/spillover
Environment=PATH=${bin}:/usr/local/bin:/usr/bin:/bin
Restart=always

[Install]
WantedBy=default.target
UNIT
      systemctl --user daemon-reload
      systemctl --user enable --now spillover.service
    fi ;;
  *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

# Keep an old profile line working: an old shell file now just loads the new one.
for old in claude-account-proxy limitbreak; do
  run rm -f "$bin/$old"
  if [[ -f "$bin/$old-shell.sh" ]] && ! ((dry)); then
    echo 'source "$HOME/.local/bin/spillover-shell.sh"' > "$bin/$old-shell.sh"
  fi
done
echo "Add to your shell profile:  source $bin/spillover-shell.sh"
