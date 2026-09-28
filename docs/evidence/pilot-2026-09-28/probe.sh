#!/bin/bash
out=$1
url=http://10.60.0.109:30080/
echo timestamp_utc,http_status > "$out"
while :; do
  timestamp=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
  status=$(curl --silent --output /dev/null --write-out "%{http_code}" --max-time 1 "$url" 2>/dev/null || true)
  [ -n "$status" ] || status=000
  echo "$timestamp,$status" >> "$out"
  sleep 0.2
done
