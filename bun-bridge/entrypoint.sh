#!/bin/sh
set -eu

/app/bun-bridge &
bun_pid=$!
for _ in $(seq 1 50); do
  wget -q -O /dev/null http://127.0.0.1:8787/healthz && break
  sleep 0.1
done
if ! wget -q -O /dev/null http://127.0.0.1:8787/healthz; then
  echo "bun bridge did not start" >&2
  kill "$bun_pid" 2>/dev/null || true
  exit 1
fi

/app/opencode-proxy &
app_pid=$!

cleanup() {
  kill "$bun_pid" "$app_pid" 2>/dev/null || true
}
trap cleanup INT TERM EXIT

# Keep the container tied to both services. If either exits, restart the container.
while kill -0 "$bun_pid" 2>/dev/null && kill -0 "$app_pid" 2>/dev/null; do
  sleep 1
done

echo "backend or bun bridge exited" >&2
exit 1
