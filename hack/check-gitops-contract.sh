#!/usr/bin/env sh
# Copyright 2026 The PodHandoff Authors.
# SPDX-License-Identifier: Apache-2.0

set -eu

doc="docs/gitops.md"
controller="internal/controller/podhandoff_controller.go"

grep -Eq 'AnnotationBaseReplicas[[:space:]]*=[[:space:]]*"podhandoff.io/base-replicas"' "$controller"
grep -Fq -- '- /spec/replicas' "$doc"
grep -Fq -- '- /metadata/annotations/podhandoff.io~1base-replicas' "$doc"
grep -Fq -- '- RespectIgnoreDifferences=true' "$doc"

printf '%s\n' "GitOps field-ownership contract is consistent."
