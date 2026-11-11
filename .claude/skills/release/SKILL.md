---
name: release
description: Cut a release of SEO Site Checker from main: verify green, propose the next semver, tag it and publish a GitHub Release with notes.
disable-model-invocation: true
---

A release is an annotated `vX.Y.Z` tag on `main` plus a GitHub Release whose notes say what changed since the previous tag. Nothing is deployed: the app runs locally under mise. Arguments, if given, are an explicit version (`0.3.0`); it skips step 3's proposal but still goes through step 4's confirmation.

## 1. Verify the release commit is green

- `main` is checked out, the working tree is clean, and `git fetch` shows it equal to `origin/main`. If not, stop and report what differs; never release from a branch or unpushed commits.
- The CI run for `origin/main`'s HEAD succeeded: `gh run list --branch main --commit <sha> --json conclusion,status,name`.
- `mise run lint` and `mise run test` pass locally.

Done when all three hold for the same sha. Any failure stops the release; show the failing output.

## 2. Collect the changes since the last release

- Previous tag: `git describe --tags --abbrev=0 --match 'v*'`. No tag means this is the first release, and the range is the whole history.
- Every PR merged into `main` in that range (`gh pr list --state merged --base main --search "merged:>=<tag date>" --json number,title,body,closingIssuesReferences`), plus any commit in `git log <tag>..HEAD` that arrived without a PR.
- New goose migration files in the range (`git diff --name-only --diff-filter=A <tag>..HEAD` under the migrations directory).

Done when every commit in the range is accounted for by a PR or listed on its own.

## 3. Propose the version

Semver, pre-1.0 while the MVP is being built:

- **Minor** (`0.x+1.0`): any new user-visible behaviour, any change to existing behaviour, or any new migration.
- **Patch** (`0.x.y+1`): only fixes, docs, tests or internal refactors with no migration.
- First release: `v0.1.0`. Going to `1.0.0` is the user's call, never proposed.

## 4. Draft the notes and get a yes

Write the notes in the language of `GLOSSARY.md` (Check, Page pool, Snapshot, Diff, Report…), one line per user-visible change, each linking its PR:

```markdown
## Added
- <change> (#<pr>)
## Changed
## Fixed
## Upgrading
- Run `mise run migrate` (this release adds <n> migrations).
```

Omit empty sections; include **Upgrading** only when there are new migrations. Internal-only PRs (tests, CI, refactors) collapse into one closing line: "Plus <n> internal changes."

Show the user the proposed version, the reason for minor vs patch, and the notes. Wait for an explicit yes; apply any edits they ask for and show the result again.

## 5. Tag and publish

```sh
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
gh release create vX.Y.Z --verify-tag --title "vX.Y.Z" --notes-file <notes file in the scratchpad>
```

Done when `gh release view vX.Y.Z` shows the release on the tagged sha. Report its URL.
