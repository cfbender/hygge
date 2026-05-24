#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat >&2 <<'EOF'
Usage: mise run changelog -- major|minor|patch

Generates CHANGELOG.md with git-cliff for the next major/minor/patch version.
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

git-cliff v0.3.0..HEAD --tag "$tag" --output CHANGELOG.md
