#!/usr/bin/env bash
# Copyright (c) 2026 The terraform-provider-rustfs contributors
# SPDX-License-Identifier: Apache-2.0
#
# Resolve the version to release. Used by the Prepare Release workflow; the
# Release workflow's tag job performs the same patch/minor/major bump in tfkit.
#
# Usage: scripts/next-version.sh <latest-tag> <release-type> [manual-version]
#   latest-tag       newest tag, with or without a leading v (v0.5.1)
#   release-type     patch | minor | major, used when manual-version is empty
#   manual-version   x.y.z, overrides release-type
#
# Prints the bare version (0.6.0), without the leading v.
set -euo pipefail

latest="${1:-v0.0.0}"
release_type="${2:-patch}"
manual="${3:-}"

base="${latest#v}"
if [[ ! "$base" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "invalid latest tag '$latest': expected vX.Y.Z" >&2
	exit 2
fi

IFS='.' read -r major minor patch <<<"$base"

if [[ -n "$manual" ]]; then
	if [[ ! "$manual" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		echo "invalid version '$manual': expected X.Y.Z" >&2
		exit 2
	fi
	echo "$manual"
	exit 0
fi

case "$release_type" in
major) echo "$((major + 1)).0.0" ;;
minor) echo "${major}.$((minor + 1)).0" ;;
patch) echo "${major}.${minor}.$((patch + 1))" ;;
*)
	echo "invalid release type '$release_type': expected patch, minor or major" >&2
	exit 2
	;;
esac
