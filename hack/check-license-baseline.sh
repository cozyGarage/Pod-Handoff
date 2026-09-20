#!/usr/bin/env bash
set -euo pipefail

test -f LICENSE
grep -Fq 'Apache License' LICENSE
grep -Fq 'Version 2.0, January 2004' LICENSE
test -f UPSTREAM.md
grep -Fq 'Understudy' UPSTREAM.md
grep -Fq 'Apache License 2.0' UPSTREAM.md

# This identifies dependencies whose licenses cannot be determined by the Go
# module graph. Policy decisions remain a maintainer review, not an allow-list
# guessed by a script.
go run github.com/google/go-licenses@v1.6.0 check ./...
