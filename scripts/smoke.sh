#!/usr/bin/env bash
# Usage: scripts/smoke.sh <binary> <expected version>
# Starts the server over stdio and checks the version it reports and that it
# answers tools/list. Sends no request to PRIM.
set -euo pipefail

if (($# != 2)); then
	echo "usage: $0 <binary> <expected version>" >&2
	exit 2
fi
bin=$1
want=$2

status=0
out=$(
	{
		printf '%s\n' \
			'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
			'{"jsonrpc":"2.0","method":"notifications/initialized"}' \
			'{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
		# Closing stdin right away drops the replies.
		sleep 2
	} | PRIM_API_KEY=dummy "$bin" 2>&1
) || status=$?

fail() {
	echo "smoke: $bin: $*" >&2
	printf '%s\n' "$out" >&2
	exit 1
}

((status == 0)) || fail "exit status $status"
got=$(sed -n 's/.*"serverInfo":{[^}]*"version":"\([^"]*\)".*/\1/p' <<<"$out")
[[ $got == "$want" ]] || fail "version ${got:-missing}, want $want"
grep -q '"id":2,"result":{.*"tools":\[' <<<"$out" || fail "no tools/list result"
echo "smoke: $bin reports $got and lists its tools"
