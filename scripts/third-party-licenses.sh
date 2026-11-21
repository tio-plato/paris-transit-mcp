#!/usr/bin/env bash
# Writes build/THIRD_PARTY_LICENSES from the license and notice files of the
# modules linked into the binary: their licenses require shipping them with it.
set -euo pipefail
shopt -s nullglob

out=build/THIRD_PARTY_LICENSES
mkdir -p build
: >"$out"
# Every GOOS we ship links the same modules, so one list covers them all.
go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/paris-transit-mcp |
	sort -u |
	while read -r module dir; do
		[[ -n $module ]] || continue
		files=("$dir"/LICENSE* "$dir"/NOTICE*)
		if ((${#files[@]} == 0)); then
			echo "third-party-licenses: no license file in $module" >&2
			exit 1
		fi
		{
			printf '== %s\n\n' "$module"
			cat "${files[@]}"
			printf '\n'
		} >>"$out"
	done
