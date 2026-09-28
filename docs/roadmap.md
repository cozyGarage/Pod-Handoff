# Roadmap

PodHandoff is an early prerelease preparing for its first stage pilot. The
current goal is to establish repeatable evidence for a narrow use case: a
stateless Deployment that can safely run two instances briefly during an
announced node disruption. A chart or passing unit suite alone is not pilot
readiness.

## Implemented

- Readiness-gated temporary surge for Deployments
- Eviction webhook that holds matching evictions, with bounded relaxation and
  fail-open behavior
- Cordon, taint, eviction, and optional cloud-sentinel signals
- Cleanup that restores PodHandoff-owned Deployment and Pod state
- Kind scenarios for normal handoff and deadline escape
- Additional Kind scenarios for rollback, controller fail-open, and external
  HTTP probing; these additions still need to be run and reviewed
- Argo CD field-ownership guidance and a static contract check; no live Argo CD
  coexistence result yet

The AWS sentinel has been validated against real spot interruptions. GCP and
Azure probes have unit coverage only and remain experimental. The Helm chart
is marked prerelease. There are no PodHandoff stage or production availability
results.

## Gates before a stage pilot

1. Run the unit/envtest, lint, build, and Kind suites from a clean checkout.
   Review and fix failures, including the newer rollback, fail-open, and probe
   scenarios.
2. Prove Argo CD's scoped `ignoreDifferences` and
   `RespectIgnoreDifferences=true` behavior during an active surge, including
   recovery of the Deployment's baseline replicas and convergence of an
   unrelated change. The static `make gitops-check` is not this proof.
3. Complete the disposable TestLab drill with a synthetic canary, external
   probe, normal handoff, deadline escape, rollback, cleanup, and captured
   evidence. TestLab routing and credentials stay outside this repository.
4. Choose a stage workload only after confirming that temporary overlap is
   safe. State the limits and record probe results; do not claim zero downtime.
5. Complete license, SBOM, test, and release-note gates before promoting the
   prerelease chart or image.

## Later work

- Validate GCP and Azure sentinels against their real interruption mechanisms.
- Consider Azure Scheduled Event approval after traffic readiness.
- Revisit interception if drainers adopt the Kubernetes EvictionRequest API.
- Consider a `kubectl podhandoff status` command if incident use shows a need.
