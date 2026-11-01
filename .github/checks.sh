#!/bin/bash
# Consistency checks between code and docs. CI runs it; run it locally before every commit.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

GREEN='\033[0;32m'; RED='\033[0;31m'; DIM='\033[2m'; NC='\033[0m'
if [ ! -t 1 ] || [ -n "${NO_COLOR:-}" ]; then GREEN=''; RED=''; DIM=''; NC=''; fi
FAIL=0
ok()   { printf "  ${GREEN}ok${NC}   %s\n" "$1"; }
fail() { printf "  ${RED}FAIL${NC} %s\n       ${DIM}%s${NC}\n" "$1" "$2"; FAIL=1; }

# Tracked plus untracked-but-not-ignored files, so a new file is checked before its first commit.
files() {
  git ls-files -z --cached --others --exclude-standard -- "$@" \
    | while IFS= read -r -d '' f; do [ -e "$f" ] && printf '%s\0' "$f"; done
}

# ─── 1. No em dash anywhere ──────────────────────────────────────────────────
emdash=$(printf '\342\200\224')
hits=$(files | xargs -0 grep -IlF -- "$emdash" 2>/dev/null)
if [ -z "$hits" ]; then
  ok "no em dash"
else
  fail "em dash in: $(echo "$hits" | tr '\n' ' ')" "Use a colon, a comma or a plain hyphen."
fi

# ─── 2. Comment policy: no block of 4+ comment lines in code ─────────────────
comment_blocks() {
  [ "$#" -gt 1 ] || return 0
  awk -v pat="$1" '
    function flush() { if (n >= 4) print file ":" s "-" last; n = 0 }
    FNR == 1 { flush(); file = FILENAME }
    $0 ~ pat && !/^[[:space:]]*\/\/go:/ && !/^#!/ && !/# ───/ && !/^# shellcheck / { n++; if (n == 1) s = FNR; last = FNR; next }
    { flush() }
    END { flush() }
  ' "${@:2}"
}
go_files=(); while IFS= read -r -d '' f; do go_files+=("$f"); done < <(files '*.go')
other_files=(); while IFS= read -r -d '' f; do other_files+=("$f"); done < <(files '*.sh' '*.yml' '*.yaml')
blocks="$(comment_blocks '^[[:space:]]*//' ${go_files[@]+"${go_files[@]}"})"
blocks="$blocks$(comment_blocks '^[[:space:]]*#' ${other_files[@]+"${other_files[@]}"})"
if [ -z "$blocks" ]; then
  ok "comment blocks are short (the why lives in CLAUDE.md, Defense lines)"
else
  fail "long comment block: $(echo "$blocks" | tr '\n' ' ')" \
       "Keep one line in the code and move the reason to the Defense lines table."
fi

# ─── 3. CLAUDE.md stays under 200 lines ──────────────────────────────────────
lines=$(wc -l < CLAUDE.md | tr -d ' ')
if [ "$lines" -le 200 ]; then
  ok "CLAUDE.md is ${lines} lines"
else
  fail "CLAUDE.md is ${lines} lines" "The limit is 200; move user-facing detail to README."
fi

# ─── 4. Shell scripts parse and pass shellcheck ──────────────────────────────
bad=""
sh_files=(); while IFS= read -r -d '' f; do sh_files+=("$f"); done < <(files '*.sh')
for f in ${sh_files[@]+"${sh_files[@]}"}; do bash -n "$f" 2>/dev/null || bad="$bad $f"; done
if [ -n "$bad" ]; then
  fail "bash -n failed:$bad" "Fix the syntax error."
elif ! command -v shellcheck >/dev/null; then
  ok "shell scripts parse (shellcheck not installed, skipped)"
elif out=$(shellcheck -S warning ${sh_files[@]+"${sh_files[@]}"} 2>&1); then
  ok "shell scripts parse and pass shellcheck"
else
  fail "shellcheck found problems" "$out"
fi

# ─── 5. The port is the same in code, shell function and docs ────────────────
port=$(sed -n 's/^[[:space:]]*defaultPort[[:space:]]*= \([0-9][0-9]*\)$/\1/p' main.go)
shell_port=$(sed -n 's/.*SPILLOVER_PORT:-\([0-9][0-9]*\)}.*/\1/p' shell.sh)
if [ -z "$port" ]; then
  fail "defaultPort not found in main.go" "Did the constant block change?"
elif [ "$port" != "$shell_port" ]; then
  fail "shell.sh defaults to port ${shell_port:-?}, main.go to ${port}" "Keep SPILLOVER_PORT's default in shell.sh equal to defaultPort."
elif grep -q "$port" README.md && grep -q "$port" CLAUDE.md; then
  ok "port ${port} agrees in main.go, shell.sh, README and CLAUDE.md"
else
  fail "port ${port} missing from README or CLAUDE.md" "A port change updates both docs."
fi

# ─── 6. The launchd label is the same in install.sh and docs ─────────────────
label=$(sed -n 's|.*<key>Label</key><string>\([^<]*\)</string>.*|\1|p' install.sh)
if [ -z "$label" ]; then
  fail "launchd label not found in install.sh" "Did the plist change?"
elif [ "$(grep -c "$label" install.sh)" -ge 3 ] && grep -q "$label" README.md && grep -q "$label" CLAUDE.md; then
  ok "launchd label ${label} agrees in install.sh, README and CLAUDE.md"
else
  fail "launchd label ${label} is not used everywhere" \
       "install.sh (plist name, Label, bootout), README Uninstall and CLAUDE.md name the same label."
fi

# ─── 7. Commands: runCommand, the usage text and README agree ────────────────
cases=$(awk '/^func runCommand/,/^}/' cli.go | sed -n 's/^[[:space:]]*case "\([a-z]*\)":.*/\1/p' | sort -u)
in_usage=$(awk '/^const usage = /,/`$/' cli.go | sed -n 's/^\(const usage = `\)\{0,1\}spillover \([a-z][a-z]*\).*/\2/p' | sort -u)
in_readme=$(grep -oE 'spillover [a-z]+' README.md | awk '{print $2}' | sort -u)
missing_usage=$(comm -23 <(echo "$cases" | grep -vx serve) <(echo "$in_usage") | tr '\n' ' ')
extra_usage=$(comm -13 <(echo "$cases") <(echo "$in_usage") | tr '\n' ' ')
missing_readme=$(comm -23 <(echo "$cases") <(echo "$in_readme") | tr '\n' ' ')
extra_readme=$(comm -13 <(echo "$cases") <(echo "$in_readme") | tr '\n' ' ')
if [ -z "$cases" ]; then
  fail "no command found in runCommand" "Did cli.go change shape?"
elif [ -n "$missing_usage$extra_usage$missing_readme$extra_readme" ]; then
  fail "commands disagree" \
       "not in usage: ${missing_usage:--} | usage only: ${extra_usage:--} | not in README: ${missing_readme:--} | README only: ${extra_readme:--}"
else
  ok "commands agree in runCommand, usage text and README: $(echo "$cases" | tr '\n' ' ')"
fi

# ─── 8. The run line in CLAUDE.md is what CI runs ────────────────────────────
run_line=$(sed -n 's/^\*\*After every change, run:\*\* `\(.*\)`$/\1/p' CLAUDE.md)
ci_steps=$(sed -n 's/^[[:space:]]*- run: //p' .github/workflows/ci.yml)
not_in_ci=""
rest="$run_line"
while [ -n "$rest" ]; do
  part="${rest%% && *}"
  printf '%s\n' "$ci_steps" | grep -qxF -- "$part" || not_in_ci="$not_in_ci [$part]"
  [ "$part" = "$rest" ] && rest="" || rest="${rest#* && }"
done
if [ -z "$run_line" ]; then
  fail "no \"After every change, run:\" line in CLAUDE.md" "The Project section names the commands CI runs."
elif [ -n "$not_in_ci" ]; then
  fail "CLAUDE.md run line has steps CI does not run:$not_in_ci" "Keep the run line and ci.yml the same commands."
else
  ok "CLAUDE.md run line matches ci.yml"
fi

echo ""
[ "$FAIL" -eq 0 ] && printf "${GREEN}Consistency checks passed.${NC}\n" \
                  || printf "${RED}Consistency checks failed.${NC}\n"
exit "$FAIL"
