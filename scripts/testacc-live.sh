#!/usr/bin/env bash
set -euo pipefail

ENDPOINT="${RUSTFS_ENDPOINT:-rustfs:9001}"
FILTER="${GO_TEST_FILTER:-TestAcc}"

# Strip an optional scheme so the host/port can be probed with /dev/tcp.
host_port="${ENDPOINT#http://}"
host_port="${host_port#https://}"
host_port="${host_port%%/*}"
host="${host_port%%:*}"
port="${host_port##*:}"
if [ "${port}" = "${host_port}" ]; then
  port="9001"
fi

echo "Waiting for RustFS at ${host}:${port} ..."
ready=""
for i in $(seq 1 60); do
  if (exec 3<>"/dev/tcp/${host}/${port}") 2>/dev/null; then
    ready=1
    break
  fi
  echo "  not ready (attempt ${i}/60)"
  sleep 2
done
if [ -z "${ready}" ]; then
  echo "RustFS did not become ready at ${host}:${port}" >&2
  exit 1
fi
echo "RustFS is ready."

go mod download && go mod verify

# Discover the packages that actually contain live acceptance tests, so a new
# test is picked up without touching this list. Live tests are the TestAcc*
# suite; TestLiveAcc* is also matched for forward compatibility.
packages=$(grep -rl --include='*_test.go' -E '^func (TestAcc|TestLiveAcc)' \
  internal provider 2>/dev/null \
  | xargs -n1 dirname | sort -u | sed 's|^|./|' | tr '\n' ' ')

if [ -z "${packages}" ]; then
  echo "no live test packages found" >&2
  exit 1
fi

echo "live test packages: ${packages}"
# shellcheck disable=SC2086
go test -v -count=1 -timeout 30m -run "${FILTER}" ${packages}
