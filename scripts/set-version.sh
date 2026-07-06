#!/usr/bin/env bash
# Sync the release version into every file that carries one. Run by
# release-it's after:bump hook; the staged files land in the release commit.
set -euo pipefail
cd "$(dirname "$0")/.."
v="${1:?usage: set-version.sh <version>}"

# BSD/GNU-portable in-place sed.
sedi() { sed -i.bak -E "$1" "$2" && rm "$2.bak"; }

sedi "s/^const version = \".*\"/const version = \"$v\"/" engine/cmd/api/main.go
sedi "s/^version: .*/version: $v/" deploy/chart/oarlock/Chart.yaml
sedi "s/^appVersion: .*/appVersion: \"$v\"/" deploy/chart/oarlock/Chart.yaml

git add engine/cmd/api/main.go deploy/chart/oarlock/Chart.yaml package.json
echo "version set to $v"
