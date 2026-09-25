#!/usr/bin/env bash
set -euo pipefail

chart="charts/podhandoff"
template="templates/sentinel.yaml"

rendered="$(helm template smoke "$chart" \
  --set sentinel.enabled=true \
  --show-only "$template")"

if ! grep -Fq 'command: ["/sentinel"]' <<<"$rendered"; then
  echo "error: enabled sentinel chart does not run /sentinel" >&2
  exit 1
fi

mapfile -t args < <(
  sed -n '/^[[:space:]]*args:$/,/^[[:space:]]*env:$/ {
    s/^[[:space:]]*- \(--[^[:space:]]*\)$/\1/p
  }' <<<"$rendered"
)

if [ "${#args[@]}" -eq 0 ]; then
  echo "error: no sentinel arguments found in rendered chart" >&2
  exit 1
fi

# --help makes the process exit after flag parsing, before it needs Kubernetes
# credentials. Any chart argument unknown to the binary still fails the check.
go run ./cmd/sentinel "${args[@]}" --help >/dev/null 2>&1

echo "Sentinel chart command matches the sentinel binary."
