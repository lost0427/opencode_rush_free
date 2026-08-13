#!/bin/sh
set -eu

/app/bun-bridge &
for _ in $(seq 1 50); do
  wget -q -O /dev/null http://127.0.0.1:8787/healthz && break
  sleep 0.1
done
if ! wget -q -O /dev/null http://127.0.0.1:8787/healthz; then
  echo "bun bridge did not start" >&2
  exit 1
fi
exec /app/opencode-proxy
