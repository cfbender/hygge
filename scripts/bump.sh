#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat >&2 <<'EOF'
Usage: mise run bump -- major|minor|patch

Increments cmd/hygge/cli/cli.go Version, commits the change, creates an
annotated tag, then pushes the current branch and tag to origin.

Set HYGGE_BUMP_DRY_RUN=1 to print the planned version without editing,
committing, tagging, or pushing.
EOF
}

if [[ $# -ne 1 ]]; then
	usage
	exit 2
fi

part=$1
case "$part" in
major | minor | patch) ;;
*)
	usage
	exit 2
	;;
esac

version_file="cmd/hygge/cli/cli.go"
current=$(perl -ne 'print "$1\n" if /^const Version = "([0-9]+\.[0-9]+\.[0-9]+)"$/' "$version_file")

if [[ -z "$current" ]]; then
	printf 'Could not find semver Version constant in %s\n' "$version_file" >&2
	exit 1
fi

IFS=. read -r major minor patch <<<"$current"
case "$part" in
major)
	major=$((major + 1))
	minor=0
	patch=0
	;;
minor)
	minor=$((minor + 1))
	patch=0
	;;
patch)
	patch=$((patch + 1))
	;;
esac

next="$major.$minor.$patch"
tag="v$next"

if [[ "${HYGGE_BUMP_DRY_RUN:-}" == "1" ]]; then
	printf '%s -> %s (%s)\n' "$current" "$next" "$tag"
	exit 0
fi

dirty_paths=$( { git diff --name-only; git diff --cached --name-only; } | sort -u | grep -v '^CHANGELOG\.md$' || true )
if [[ -n "$dirty_paths" ]]; then
	printf 'Working tree has uncommitted changes outside CHANGELOG.md. Commit or stash them before bumping:\n%s\n' "$dirty_paths" >&2
	exit 1
fi

branch=$(git branch --show-current)
if [[ -z "$branch" ]]; then
	printf 'Cannot bump from a detached HEAD.\n' >&2
	exit 1
fi

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	printf 'Tag %s already exists locally.\n' "$tag" >&2
	exit 1
fi

if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
	printf 'Tag %s already exists on origin.\n' "$tag" >&2
	exit 1
fi

perl -0pi -e 's/const Version = "\Q'"$current"'\E"/const Version = "'"$next"'"/' "$version_file"
gofmt -w "$version_file"

git add "$version_file"
git add CHANGELOG.md
git commit -m "chore: release $tag"
git tag -a "$tag" -m "Release $tag"
git push origin "$branch"
git push origin "$tag"

printf 'Released %s on %s.\n' "$tag" "$branch"
