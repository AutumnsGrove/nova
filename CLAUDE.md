# CLAUDE.md

@AGENTS.md

Nova is a **Go** project. Ignore Python, UV, `pytest`, or Black guidance from a `CLAUDE.md` in a parent
directory: build and test with `make ci` and the commands in `AGENTS.md`.

## Bumping the version

When the user asks to bump, release, or tag a version, follow **"Releasing and bumping versions"** in
`AGENTS.md` exactly. In short: confirm the number; branch `release/vX.Y.Z` from `main`; move `[Unreleased]` in
`CHANGELOG.md` under the new version with upgrade notes for anything that changes behaviour for a caller;
`make ci`; open the PR; after it merges, tag the merge commit `vX.Y.Z` on `main` and push the tag (that starts
the release workflow); verify the release and that `go list -m` resolves it. Never move or delete a published tag.
Nova has no version constant to edit, so do not go looking for one.
