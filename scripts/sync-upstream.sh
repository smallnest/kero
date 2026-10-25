#!/bin/sh
# Sync this fork with upstream (egoist/kero): fetch, fast-forward main,
# merge upstream/main into dev, test, and push both.
#
# It refuses to touch anything when it can't be sure the repository is
# quiet: the tree must be clean, and both branches must equal origin.
#
# Run it by hand with scripts/sync-upstream.sh. Every 30 minutes, install
# it as a launchd job: scripts/sync-upstream.sh --install (--uninstall ends
# it; see scripts/sync-upstream.plist).
set -eu
cd "$(dirname "$0")/.."

# Tell the user in Lark/Feishu when the run fails or syncs something new,
# through lark-cli with the bot, to larkUser (export it; open_id "ou_…").
# A quiet run says nothing. A failed send never fails the run.
notify() {
	[ -n "${larkUser:-}" ] || return 0
	if ! command -v lark-cli >/dev/null 2>&1; then
		log "lark-cli is not in PATH, so no Lark message"
		return 0
	fi
	lark-cli im +messages-send --as bot --user-id "$larkUser" --text "$1" >/dev/null 2>&1 ||
		log "the Lark message did not go out"
}

log() { printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"; }
# A run that stops on purpose says why in Lark, and so in the log.
die() { log "error: $*"; notify "kero sync failed: $*"; exit 1; }

# --install writes and loads the launchd job of sync-upstream.plist;
# --uninstall unloads it. Both use the repository as it is now.
if [ "${1:-}" = --install ] || [ "${1:-}" = --uninstall ]; then
	repo=$(pwd -P)
	label=sh.kero.sync-upstream
	plist="$HOME/Library/LaunchAgents/$label.plist"
	[ "$(uname)" = Darwin ] || die "--install needs macOS's launchd"
	launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
	if [ "${1:-}" = --uninstall ]; then
		rm -f "$plist"
		log "uninstalled $label"
		exit 0
	fi
	mkdir -p "$HOME/Library/LaunchAgents"
	sed -e "s|__REPO__|$repo|g" -e "s|__PATH__|$PATH|g" "$repo/scripts/sync-upstream.plist" >"$plist"
	plutil -lint "$plist" >/dev/null || die "$plist is not valid"
	launchctl bootstrap "gui/$(id -u)" "$plist" || die "launchctl could not load $plist"
	launchctl enable "gui/$(id -u)/$label"
	log "installed $label: every 30 minutes, log in /tmp/kero-sync-upstream.log"
	log "run it now with: launchctl kickstart -k gui/$(id -u)/$label"
	exit 0
fi

[ -f mygo.json ] && [ -f internal/daemon/daemon.go ] || die "not in the kero repository"
git remote get-url upstream >/dev/null 2>&1 || die "the upstream remote is missing"
[ -z "$(git status --porcelain)" ] || die "the tree is not clean"
start=$(git rev-parse --abbrev-ref HEAD)
[ "$start" = dev ] || die "dev is not the checked-out branch"
# A run that stops before its first write moves no branch and touches no
# file, so the merge can be tried over, and the branch put back, on a
# remote one.
trap 'git checkout --quiet "$start" 2>/dev/null || true' EXIT

log "fetching"
git fetch --quiet --prune origin
git fetch --quiet --prune upstream

[ "$(git rev-parse dev)" = "$(git rev-parse origin/dev)" ] || die "dev is not origin/dev"
ff_main=0
if git merge-base --is-ancestor upstream/main main; then
	if [ "$(git rev-list --count main..upstream/main)" -gt 0 ]; then
		[ "$(git rev-parse main)" = "$(git rev-parse origin/main)" ] || die "main is not origin/main"
		ff_main=1
	fi
else
	log "main: has commits of its own, or is behind origin; leaving it"
fi
merge_dev=0
# A later merge of main into dev hides upstream/main behind it, so dev can
# hold all of upstream's commits yet not have upstream/main in it.
git merge-base --is-ancestor upstream/main dev ||
	{ git merge-base --is-ancestor upstream/main main && git merge-base --is-ancestor main dev; } ||
	merge_dev=1

# merge_tree tries the merge on a remote one, before anything changes, so
# that a conflict leaves the tree clean and main untouched.
#
# CHANGELOG.md always conflicts: this fork writes its own notes in it,
# under its own title, where upstream writes a new release at the top,
# and the merge takes "Unreleased (smallnest/kero)" and "2.1.21
# (smallnest/kero)" to be the same entry. That one conflict is not a
# conflict of the code, so the merge takes this fork's CHANGELOG.md
# whole, and says so. Any other file that conflicts stops the run.
merge_try=/tmp/kero-sync-merge.$$
merge_tree() {
	if git merge-tree --write-tree dev upstream/main >"$merge_try" 2>&1; then
		return 0
	fi
	conflicts=$(grep -E '^CONFLICT' "$merge_try" | sed -e 's/.* in /    /' -e 's/.*: /    /')
	others=$(printf '%s\n' "$conflicts" | grep -v '^    CHANGELOG\.md$' || true)
	if [ -n "$conflicts" ] && [ -z "$others" ]; then
		log "CHANGELOG.md conflicts, as it always does; the merge keeps this fork's"
		merged=$(head -1 "$merge_try")
		if [ "$(git rev-parse "$merged^{tree}")" != "$(git rev-parse dev^{tree})" ]; then
			git read-tree -u --reset "$merged" || die "the merge could not be read"
			git checkout --quiet dev -- CHANGELOG.md || die "CHANGELOG.md could not be kept"
			merge_dev=2 # merged, but not committed
		fi
		return 0
	fi
	if [ -n "$conflicts" ]; then
		log "error: merging upstream/main into dev conflicts; merge by hand:"
		printf '%s\n' "$conflicts" | head -20
		die "nothing changed"
	fi
	die "merging upstream/main into dev conflicts; merge by hand"
}
if [ "$merge_dev" = 1 ]; then
	merge_tree
	rm -f "$merge_try"
fi

# From here a check that stops the run leaves a branch moved, for the
# next run to find.
if [ "$ff_main" = 1 ]; then
	git checkout --quiet main || die "checkout main failed"
	git merge --ff-only --quiet upstream/main || die "main can't fast-forward"
	# main may change its own files (a released mygo.json), so try the
	# merge again, over it.
	log "main: fast-forwarded to $(git log -1 --oneline upstream/main)"
else
	log "main: up to date"
fi

git checkout --quiet dev || die "checkout dev failed"
if [ "$merge_dev" = 1 ]; then
	git merge --no-edit --quiet upstream/main >/dev/null 2>&1 && merge_dev=2 ||
		{ git merge --abort 2>/dev/null || true; merge_tree; }
	rm -f "$merge_try"
fi
if [ "$merge_dev" = 2 ]; then
	# The tree holds the merge already, with this fork's CHANGELOG.md.
	git status --porcelain | grep -q . ||
		die "the merge changes nothing: up to date, and the merge's second parent hides it"
	git commit --no-edit --quiet -m "Merge upstream/main into dev, with this fork's CHANGELOG" ||
		die "the merge could not be committed; dev has the merge, unpushed; fix by hand"
fi
if [ "$merge_dev" != 0 ]; then
	merge_dev=1
	log "dev: merged upstream/main into $(git rev-parse --short dev)"
	log "dev: checking"
	go vet ./... || die "go vet failed; dev has the merge, unpushed; fix by hand"
	CGO_ENABLED=0 go test ./internal/... || die "go test failed; dev has the merge, unpushed; fix by hand"
	log "dev: vet and tests pass"
else
	log "dev: up to date"
fi

if [ "$ff_main" = 1 ] || [ "$merge_dev" = 1 ]; then
	git push --quiet origin main || die "pushing main failed"
	git push --quiet origin dev || die "pushing dev failed"
	log "pushed main and dev"
	notify "kero sync: main and dev are up to date with upstream
main: $(git log -1 --oneline main)
dev: $(git log -1 --oneline dev)"
fi
log "done"
