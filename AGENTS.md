# Kero

- Cutting a release: follow [RELEASING.md](RELEASING.md), from `dev`.

## The fork's branches

This repository is `smallnest/kero`, a fork of `egoist/kero` that keeps its
own features. Upstream does not merge the fork's pull requests.

- `main` is `upstream/main`, always. Never commit to it here.
- `dev` is where the fork's features live: `main` plus the fork's commits.
  Build, test and tag releases from `dev`.
- `backup/pre-rebase-main` was the fork's `main` before `dev` took its
  place. It is a backup only.

### Update from upstream

```sh
git checkout main
git fetch upstream
git merge --ff-only upstream/main
git push origin main

git checkout dev
git rebase main
go build ./... && go vet ./... && go test . ./internal/...
git push origin dev --force-with-lease
```

Resolve conflicts on `dev` only, never on `main`. A `--force-with-lease`
push of `dev` is expected after a rebase.

### Add a feature

```sh
git checkout dev
git checkout -b feat/<name>
# ... commit ...
# merge back into dev when done
```

### Take one upstream commit

Rebase `dev` onto `main` after updating `main`. To take a single upstream
commit without moving `main`, cherry-pick it onto `dev`:

```sh
git fetch upstream
git checkout dev
git cherry-pick -x <sha>
```

Keep `-x`, so the commit names where it came from.

## The fork's features keep working

Each fork feature came as a pull request upstream never merged, and it
lives in `dev`, not only in its branch. Merging upstream, or any branch,
must keep every one: resolve a conflict by keeping both sides, never by
taking upstream's file whole, and check them after the merge rather than
only that it builds.

| Feature | PR | Code to find | Tests |
|---|---|---|---|
| Files view in the side panel | #241 | `sideFiles`, `filesView`, `filesRoot` | `TestSideToggle`, `TestFilePane`, `TestFileTreeRel` |
| Sidebar sort and group, project badges, folded projects | #214 | `sortButton`, `sessionSidebar`, `projectBadgeColor`, `projectInitial`, `dateHeader` | `TestProjectOrder`, `TestProjectDrag`, `TestSessionAlign`, `TestCollapseSelectedProject`, `TestSidebarRowKeepsTime` |
| Background opacity and blur | #211 | `washOpacity`, `vibrancyFor`, `WindowBlur` | `TestVibrancyFor`, `TestBlurlessBackgroundIsTranslucent`, `TestTranslucentGlass` |
| The menu of installed monospaced fonts | #209 | `fontOptions` | `TestFontOptionsInstalled` |
| Kero the frog | #206 | `pet.go`, `petState` | `go test . -run TestPet` |
| Quitting ends this machine's sessions | #219 | `quitRequested`, `quitAndEnd`, `QuitEndsSessions` | `TestQuitEndsSessions` |

```sh
for s in sideFiles filesView sortButton sessionSidebar projectBadgeColor fontOptions petState quitRequested vibrancyFor; do
	grep -rq "$s" --include='*.go' . || echo "missing: $s"
done
go test . -run 'TestSideToggle|TestFilePane|TestProjectOrder|TestProjectDrag|TestSessionAlign|TestCollapseSelectedProject|TestSidebarRowKeepsTime|TestQuitEndsSessions|TestFontOptionsInstalled|TestVibrancyFor|TestBlurlessBackgroundIsTranslucent|TestTranslucentGlass'
```

The branches the pull requests came from (`feat/files-view`,
`sidebar-sort`, `window-opacity`, `font-picker`, `kero-pet`,
`quit-ends-sessions`) are behind `dev` and may be rebased: a commit that
looks missing there says nothing about `dev`. Check the code and the
tests above.

Two merges showed why. One took upstream's `view.go` whole: 2.1.21 lost
the sidebar's sort button, its colored badges and its indentation, while
the data side stayed, and nothing failed to build or test. The other left
`files.toggle` with a "this fork has no Files view" case: the menu entry
named Toggle Side Panel opened Changes. Neither shows in the app's
strings either, since a locale file keeps the words of a view that no
code opens, and a feature's test can go missing with its code.

## Which repository releases come from

Kero here releases as `smallnest/kero`: `mygo.json` `updates.github`,
`internal/update.Repo`, and the install scripts in `scripts/` name it.
Do not change them back to `egoist/kero`. The old releases app updates
from `egoist/kero`; new builds update from `smallnest/kero`.

## Signing and the update key

There is no Apple Developer ID. macOS builds are ad-hoc signed (an
`xattr -d com.apple.quarantine` for a user who downloads one), and no
release takes a certificate or notarization.

`~/.kero-release-keys/mygo-update.key` signs updates; its public half is
`mygo.json`'s `updates.publicKey`. Never commit the secret, and never
replace the pair: every installed app trusts only that public key.

## Checks before a push

```sh
go build ./... && go vet ./... && go test . ./internal/...
go tool mygo i18n check
bun install && bun run --filter website build   # when the website changes
```

Before pushing a merge, run the check above for the fork's features too.

`bun install` rewrites `bun.lock`. Restore it unless the dependencies
really changed.
