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

`bun install` rewrites `bun.lock`. Restore it unless the dependencies
really changed.
