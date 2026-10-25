#!/bin/sh
# Tell the user in Lark/Feishu about each PR opened on upstream
# (egoist/kero) since the last run, with its summary, to decide whether to
# cherry-pick it into dev. It says nothing when there are none.
#
# Which PRs were told is kept in ~/.local/state/kero/upstream-prs (override
# with KERO_PR_STATE). The first run of a new store tells only of PRs
# opened in the last day, rather than of every open PR.
#
# Needs gh (logged in) and lark-cli (bot). Every 30 minutes, install it as a
# launchd job: scripts/notify-upstream-prs.sh --install (--uninstall ends
# it; see scripts/notify-upstream-prs.plist).
set -eu

REPO=egoist/kero
label=sh.kero.notify-upstream-prs

# --install writes and loads the launchd job of notify-upstream-prs.plist;
# --uninstall unloads it. Both use the repository as it is now, and
# larkUser's open_id from the environment or the sync's job.
if [ "${1:-}" = --install ] || [ "${1:-}" = --uninstall ]; then
	script=$(cd "$(dirname "$0")" && pwd -P)/notify-upstream-prs.sh
	repo=$(dirname "$(dirname "$script")")
	plist="$HOME/Library/LaunchAgents/$label.plist"
	[ "$(uname)" = Darwin ] || { echo "$0: --install needs macOS's launchd" >&2; exit 1; }
	launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
	if [ "${1:-}" = --uninstall ]; then
		rm -f "$plist"
		echo "uninstalled $label"
		exit 0
	fi
	if [ -z "${larkUser:-}" ]; then
		# The sync's job has it; take it from there.
		larkUser=$(plutil -extract EnvironmentVariables.larkUser raw \
			"$HOME/Library/LaunchAgents/sh.kero.sync-upstream.plist" 2>/dev/null) || larkUser=
	fi
	[ -n "$larkUser" ] || { echo "$0: set larkUser=ou_… to the Lark user to tell" >&2; exit 1; }
	mkdir -p "$HOME/Library/LaunchAgents"
	sed -e "s|__SCRIPT__|$script|g" -e "s|__REPO__|$repo|g" \
		-e "s|__PATH__|$PATH|g" -e "s|__LARK_USER__|$larkUser|g" \
		-e "s|__HOME__|$HOME|g" \
		"$repo/scripts/notify-upstream-prs.plist" >"$plist"
	plutil -lint "$plist" >/dev/null || { echo "$0: $plist is not valid" >&2; exit 1; }
	launchctl bootstrap "gui/$(id -u)" "$plist" || { echo "$0: launchctl could not load $plist" >&2; exit 1; }
	launchctl enable "gui/$(id -u)/$label"
	echo "installed $label: every 30 minutes, log in /tmp/kero-upstream-prs.log"
	echo "run it now with: launchctl kickstart -k gui/$(id -u)/$label"
	exit 0
fi

log() { printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"; }

state=${KERO_PR_STATE:-$HOME/.local/state/kero/upstream-prs}
now=$(date +%s)
mkdir -p "$(dirname "$state")"
touch "$state"
# newSince is when the run before this one started (it leaves "since" in
# the store); the first run of a store starts a day back, not at every
# open PR.
prefix='since '
cut=$(grep -E '^since [0-9]+$' "$state" | tail -n 1 | cut -d' ' -f2)
if [ "${cut:-}" = "" ]; then cut=$((now - 86400)); else prefix=; fi
{ echo "$prefix$now"; awk '/^since / { next } { print }' "$state"; } >"$state.tmp"
mv "$state.tmp" "$state"

prs=$(gh pr list --repo "$REPO" --state open --limit 100 --json \
	number,title,author,createdAt,isDraft,additions,deletions,changedFiles,url,body |
	jq -c --argjson cut "$cut" --rawfile seen "$state" '
		[$seen | split("\n")[] | select(test("^[1-9][0-9]*$"))] as $seen
		| [.[] | select((.createdAt | fromdateiso8601) >= $cut)
		       | . as $pr | select(($seen | index($pr.number | tostring)) == null)]
		| .[]')

[ -n "$prs" ] || { log "no new PRs"; exit 0; }

# summary is the PR body without its HTML comments or footers, held to
# max characters, so the message stays a summary.
summary() {
	printf '%s\n' "$1" | python3 -c '
import re, sys
b = sys.stdin.read()
b = re.sub(r"<!--.*?-->", "", b, flags=re.S)
b = re.sub(r"<div.*", "", b, flags=re.S)
b = re.sub(r"\n{3,}", "\n\n", b).strip()
print(b[:1200] + ("…" if len(b) > 1200 else ""))
'
}

sent=
printf '%s\n' "$prs" | while IFS= read -r pr; do
	number=$(printf '%s' "$pr" | jq -r .number)
	title=$(printf '%s' "$pr" | jq -r .title)
	author=$(printf '%s' "$pr" | jq -r .author.login)
	draft=$(printf '%s' "$pr" | jq -r 'if .isDraft then " (draft)" else "" end')
	size=$(printf '%s' "$pr" | jq -r '"\(.additions)/-\(.deletions) across \(.changedFiles) file(s)"')
	url=$(printf '%s' "$pr" | jq -r .url)
	body=$(summary "$(printf '%s' "$pr" | jq -r .body)")
	text="kero upstream PR #$number$draft: $title
by $author, $size
$url
$body
--
cherry-pick into dev?   https://github.com/smallnest/kero/compare/main...$REPO:$(printf '%s' "$pr" | jq -r .headRefName)"

	if command -v lark-cli >/dev/null 2>&1 &&
		lark-cli im +messages-send --as bot --user-id "${larkUser:-}" --text "$text" \
			--idempotency-key "kero-pr-$number" >/dev/null 2>&1; then
		log "told of PR #$number: $title"
		echo "$number" >>"$state"
	else
		log "could not tell of PR #$number; it is tried again next time"
	fi
done

log "done"
