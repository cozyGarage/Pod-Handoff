#!/usr/bin/env bash
set -euo pipefail

output="${1:-dist/podhandoff.cdx.json}"
mkdir -p "$(dirname "$output")"

# Pin the generator so the emitted inventory is reproducible for a given Go
# module graph. License detections are recorded as evidence, not asserted facts.
go run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.10.0 \
  mod -licenses -json -output "$output" .

test -s "$output"
