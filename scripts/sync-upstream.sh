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

log() { printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"; }
die() { log "error: $*"; exit 1; }

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

maybe_merge() {
	if ! git merge --no-edit upstream/main >/tmp/kero-sync-merge.$$ 2>&1; then
		log "error: merging upstream/main into dev conflicts:"
		sed 's/^/    /' /tmp/kero-sync-merge.$$ | tail -20
		git merge --abort || true
		rm -f /tmp/kero-sync-merge.$$
		die "dev left as it was; merge by hand"
	fi
	rm -f /tmp/kero-sync-merge.$$
}

[ -f mygo.json ] && [ -f internal/daemon/daemon.go ] || die "not in the kero repository"
git remote get-url upstream >/dev/null 2>&1 || die "the upstream remote is missing"
[ -z "$(git status --porcelain)" ] || die "the tree is not clean"
[ "$(git rev-parse --abbrev-ref HEAD)" = dev ] || die "dev is not the checked-out branch"
# A check that stops the run moves no branch and touches no file, so main
# and dev may both be elsewhere (origin/dev is where dev goes back to).
start=$(git rev-parse --abbrev-ref HEAD)
trap 'git checkout --quiet "$start" 2>/dev/null || true' EXIT

log "fetching"
git fetch --quiet --prune origin
git fetch --quiet --prune upstream

[ "$(git rev-parse dev)" = "$(git rev-parse origin/dev)" ] || die "dev is not origin/dev"
# Nothing below runs on a main or dev another machine moved, so the checks
# come before the first checkout.
ff_main=0
if git merge-base --is-ancestor upstream/main main; then
	if [ "$(git rev-list --count main..upstream/main)" -gt 0 ]; then
		[ "$(git rev-parse main)" = "$(git rev-parse origin/main)" ] || die "main is not origin/main"
		ff_main=1
	fi
else
	log "main: has commits of its own; leaving it"
fi
merge_dev=0
if ! git merge-base --is-ancestor upstream/main dev; then
	merge_dev=1
fi

if [ "$ff_main" = 1 ]; then
	git checkout --quiet main || die "checkout main failed"
	git merge --ff-only --quiet upstream/main || die "main can't fast-forward"
	log "main: fast-forwarded to $(git log -1 --oneline upstream/main)"
else
	log "main: up to date"
fi

git checkout --quiet dev || die "checkout dev failed"
if [ "$merge_dev" = 1 ]; then
	merge_base=$(git rev-parse HEAD)
	maybe_merge
	log "dev: merged $(git rev-list --count "$merge_base"..upstream/main) commit(s) of upstream/main"
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
fi
log "done"
