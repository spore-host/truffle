#!/usr/bin/env bash
#
# Release guard: prove the binary about to be published reports the tag it was
# built from.
#
# Why this exists. The version truffle reports used to be a hardcoded constant
# in the source ("0.1.0"). Nothing verified it, and nothing needed to: the
# release pipeline injects the real version with an -X ldflag, so the constant
# only ever surfaced in non-release builds. It stayed at "0.1.0" across dozens of
# releases, invisible precisely because releases were fine — until a source
# build reported itself as v0.1.0 and every source build also silently claimed
# the update checker owed it an upgrade to whatever was current (spawn hit the
# same class of bug at spore-host/spawn#483; see pkg/buildinfo for the shared
# rationale).
#
# The constant is gone (see pkg/buildinfo). This script closes the other half:
# it fails the tag if the ldflag wiring ever breaks — a renamed variable, a
# renamed module path, a mistyped ldflag, a `builds:` entry that lost its
# ldflags. Every one of those is silent today: `go build` succeeds, GoReleaser
# succeeds, the release publishes, and the binary reports "dev".
#
# Usage: check-release-version.sh <tag>     e.g. check-release-version.sh v0.53.0
set -euo pipefail

tag="${1:-}"
if [ -z "$tag" ]; then
  echo "usage: $0 <tag>   (e.g. v0.53.0)" >&2
  exit 2
fi

# The tag is vX.Y.Z; the version the binary reports is X.Y.Z. GoReleaser's
# {{.Version}} is the tag without the leading "v", and pkg/buildinfo trims a "v"
# from anything injected, so both sides agree on the un-prefixed form.
want="${tag#v}"

# Read the ldflag out of .goreleaser.yaml rather than restating it, so this
# check exercises the real wiring instead of a copy that could drift alongside
# it. A `builds:` entry that loses its -X line makes the grep come back empty
# and fails below.
truffle_ldflag=$(grep -oE '\-X [^ ]*truffle/cmd\.Version=\{\{\.Version\}\}' .goreleaser.yaml || true)

fail=0
note() { echo "::error::$*" >&2; fail=1; }

if [ -z "$truffle_ldflag" ]; then
  note ".goreleaser.yaml has no '-X .../truffle/cmd.Version={{.Version}}' ldflag — released truffle binaries would report 'dev'"
fi
[ "$fail" -eq 0 ] || exit 1

# Build with exactly the ldflag the release will use, substituting the real tag
# for {{.Version}}, and ask the binary what it thinks it is. This is the part a
# static check can't do: it catches a ldflag whose variable name no longer
# resolves, which the Go linker accepts silently (it does not error on an -X for
# a symbol that doesn't exist).
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Building truffle with the release ldflag for ${tag}..."
go build -ldflags "${truffle_ldflag//\{\{.Version\}\}/$want}" -o "$tmp/truffle" .

# `truffle version` prints "Version:    X.Y.Z"; take that field.
got_truffle=$("$tmp/truffle" version | awk '/^Version:/ {print $2}')

check() { # $1=binary name  $2=reported
  if [ "$2" != "$want" ]; then
    note "$1 reports version '$2' but the tag is '$tag' (expected '$want') — the -X ldflag is not reaching the version the binary prints"
  else
    echo "✅ $1 reports ${2} for tag ${tag}"
  fi
}
check truffle "$got_truffle"

[ "$fail" -eq 0 ] || exit 1

# A release must not carry an unreleased-version placeholder in the changelog
# either: the tag is the moment [Unreleased] becomes [X.Y.Z] (see CLAUDE.md), and
# publishing with the section unpromoted means the release notes for X.Y.Z say
# "Unreleased" forever.
if [ -f CHANGELOG.md ] && ! grep -qF "## [${want}]" CHANGELOG.md; then
  note "CHANGELOG.md has no '## [${want}]' section — promote [Unreleased] to [${want}] before tagging"
fi

[ "$fail" -eq 0 ] || exit 1
echo "✅ Release version check passed for ${tag}"
