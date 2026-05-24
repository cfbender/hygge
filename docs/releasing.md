# Releasing Hygge

This document describes the end-to-end release process for maintainers.

---

## Required GitHub repository settings

Configure these once under **Settings → General → Pull Requests**:

| Setting | Value |
|---|---|
| Allow merge commits | **Disabled** |
| Allow squash merging | **Enabled** |
| Default squash commit message | **Pull request title** |
| Allow rebase merging | **Disabled** |

Configure under **Settings → Branches → Branch protection (main)**:

- **Require status checks to pass before merging**
  - Add `conventional commit title` (from `.github/workflows/pr-title.yml`)
- **Require linear history** *(recommended)*

With these settings every merge produces a single commit whose message is the PR
title, which must pass the conventional-commit check.

---

## PR title convention

PR titles must follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/):

```
<type>(<optional scope>): <description>
```

Valid types: `feat` `fix` `docs` `style` `refactor` `perf` `test` `chore` `build` `ci` `revert`

Examples:

```
feat: add session export command
fix(mcp): handle timeout on slow servers
chore: update dependencies
feat!: drop Go 1.24 support
```

The `!` suffix signals a breaking change. The GitHub Actions check
(`.github/workflows/pr-title.yml`) enforces this automatically on every PR.

---

## Generating / updating the changelog

### Prerequisites

Install [git-cliff](https://git-cliff.org/docs/installation):

```sh
cargo install git-cliff
# or: brew install git-cliff
```

### Generate for the upcoming release

```sh
git-cliff v0.11.0..HEAD --tag v0.12.0 --output CHANGELOG.md
```

Replace `v0.11.0` with the previous tag and `v0.12.0` with the new version.
Regenerate `CHANGELOG.md` before cutting the tag so the final section includes
all commits intended for the release and uses git-cliff's release date. The
tag-push release workflow also runs git-cliff and uses the generated current
release section as the GitHub Release body.

### Prepend a new section to an existing CHANGELOG.md

```sh
git-cliff vX.Y.Z..HEAD --tag vA.B.C --prepend CHANGELOG.md
```

### GitHub Release body

On tag pushes, `.github/workflows/release.yml` generates the GitHub Release body
with the current tagged section only:

```sh
git-cliff --latest --strip header --github-repo owner/repo
```

The workflow writes that output to `RELEASE_NOTES.md` and passes it to
`softprops/action-gh-release` as `body_path`. `--latest` resolves to the pushed
tag during the release workflow.

### Without git-cliff installed

Paste the one-liner below, then manually sort the output into the groups
defined in `.cliff.toml` (`Features`, `Bug Fixes`, `Refactoring`, etc.):

```sh
git log vPREV..HEAD --oneline
```

---

## Cutting a release

1. Ensure `main` is clean and all desired PRs are merged.
2. Update `CHANGELOG.md` (see above) and commit the result:

   ```sh
   # Edit CHANGELOG.md, then:
   git add CHANGELOG.md
   git commit -m "docs: update changelog for vX.Y.Z"
   ```

3. Run the bump script, which increments the version constant, commits
   `chore: release vX.Y.Z`, creates an annotated tag, and pushes:

   ```sh
   mise run bump -- minor   # or major / patch
   ```

4. The push triggers `.github/workflows/release.yml`, which builds cross-platform
   binaries, generates the GitHub release body with git-cliff, and creates the
   GitHub release with the packaged artifacts.

> **Dry run**: `HYGGE_BUMP_DRY_RUN=1 mise run bump -- minor` prints the planned
> version without touching the repo.
