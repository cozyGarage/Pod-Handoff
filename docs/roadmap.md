# Roadmap

PodHandoff is an early prerelease preparing for its first stage pilot. The
current goal is to establish repeatable evidence for a narrow use case: a
stateless Deployment that can safely run two instances briefly during an
announced node disruption. A disposable TestLab comparison now shows the
expected drain behavior across two trials per mode; it is an initial signal,
not stage or production readiness. See the [pilot report](evidence/pilot-2026-09-28/README.md).

## Implemented

- Readiness-gated temporary surge for Deployments
- Eviction webhook that holds matching evictions, with bounded relaxation and
  fail-open behavior
- Cordon, taint, eviction, and optional cloud-sentinel signals
- Cleanup that restores PodHandoff-owned Deployment and Pod state
- Kind scenarios for normal handoff and deadline escape
- Additional Kind scenarios for rollback, controller fail-open, and external
  HTTP probing; these additions still need to be run and reviewed
- Disposable TestLab comparison: two plain-drain trials and two PodHandoff
  trials against a one-replica HTTP canary; raw samples and limits are in the
  [pilot report](evidence/pilot-2026-09-28/README.md)
- Follow-up lab runs covered a two-replica target, an unready replacement
  deadline, and fail-open with the controller unavailable. The actual
  webhook request-timeout path and GitOps coexistence are not yet tested.
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
   evidence. Normal drain, deadline escape, controller-unavailable behavior,
   and a two-replica comparison are captured. A truly external probe, rollback,
   and cleanup still need evidence. TestLab routing and credentials stay
   outside this repository.
4. Repeat the two-replica comparison under controlled load. Measure throughput,
   p50/p95/p99 latency, errors, and per-Pod CPU while draining with and without
   PodHandoff. Choose a load where one surviving Pod can be compared against an
   explicit service target; if it still meets the target, record that PodHandoff
   adds no demonstrated user value for that workload. Do not add automatic
   load-based scaling until this experiment shows a gap and enough lead time to
   act safely.
5. Choose a stage workload only after confirming that temporary overlap is
   safe. State the limits and record probe results; do not claim zero downtime.
6. Complete license, SBOM, test, and release-note gates before promoting the
   prerelease chart or image.

## Later work

- Validate GCP and Azure sentinels against their real interruption mechanisms.
- Consider Azure Scheduled Event approval after traffic readiness.
- Revisit interception if drainers adopt the Kubernetes EvictionRequest API.
- Consider a `kubectl podhandoff status` command if incident use shows a need.

## Data-led early warning (research)

Treat prediction as a staged safety feature, not a promise to prevent sudden
crashes. Start with measurements and alerting:

1. Record surge-to-Ready time, held-eviction duration, deadline relaxations,
   webhook outcomes, Pod restarts/OOM kills, readiness failures, and node
   conditions around each handoff. Correlate logs and events by workload and
   disruption; keep high-cardinality identifiers out of metric labels.
2. Use the observed readiness-time distribution to tune deadlines and
   distinguish normal slow starts from a replacement that is stuck. Alert on
   repeated readiness failures, CrashLoopBackOff/OOMKill, resource pressure,
   and long or relaxed eviction holds before taking automatic action.
3. First act only on authoritative warnings already emitted by Kubernetes or
   the cloud provider (for example a disruption taint or termination notice).
   A learned score or raw-log parser stays alert-only until false positives
   and lead time are measured across workloads.
4. Test node drain, unready/unschedulable replacements, webhook timeout,
   controller loss, OOM/restart, and abrupt node loss. Only consider automatic
   pre-warming when the warning arrives early enough, the workload is safe to
   overlap, and the action can expire and fail open.

An abrupt node or application failure can happen before any useful signal.
Keep replicas and failure-domain placement as the protection for that case;
PodHandoff can only act on disruption it observes in time.
