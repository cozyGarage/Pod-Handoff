# PodHandoff project direction

PodHandoff coordinates a Deployment's handoff during an announced Kubernetes
node disruption. Its purpose is to keep a ready replacement available before
the old Pod is evicted, while avoiding the cost of a permanently running
second application replica.

It is not a general high-availability product. It cannot protect against an
unannounced node failure, an application failure, unsafe duplicate work, or
requests already in flight to the departing Pod.

## Product thesis

For a Deployment that can safely overlap old and replacement Pods, the question
during a drain is whether the old Pod can remain until its replacement is
demonstrably ready. PodHandoff observes the disruption, adds temporary
capacity, gates eviction on readiness when time permits, and returns the
Deployment to its baseline after the handoff.

This makes the cost model explicit:

| Model | Normal replicas | Disruption behavior | What it does not cover |
|---|---:|---|---|
| One replica | 1 | eviction then reschedule | planned and unannounced outages |
| PodHandoff | 1 | temporary ready stand-in before eviction | unannounced node, storage, network, or application failure |
| Permanent HA | 2 or more | capacity already exists | application-level correctness still required |

## Non-negotiable design principles

1. **Readiness is the release gate.** A `Running` Pod is not a substitute for
   a serving endpoint.
2. **Fail open, not cluster-stuck.** If admission or reconciliation cannot
   make progress, Kubernetes must be able to continue a drain.
3. **Own the minimum state.** PodHandoff changes only the target replica count
   and its own base-replica annotation, then restores only what it still owns.
4. **Respect controller boundaries.** Verify the Pod → ReplicaSet → Deployment
   owner chain; never infer ownership from labels alone.
5. **GitOps is part of correctness.** A temporary surge is not valid if the
   reconciler immediately removes it. Field-level ownership must be explicit
   and testable.
6. **Evidence before claims.** Unit, envtest, Helm, Kind, and disposable-lab
   results are separate from stage and production evidence.
7. **Workload safety beats availability marketing.** Do not protect databases,
   payment executors, schedulers, queue consumers, or any single-writer
   workload without proof that temporary overlap is safe.

## Current maturity

The project is an early prerelease, before its first stage pilot. The core
controller, admission webhook, optional cloud sentinel, and Kind scenario are
implemented. The Kind suite contains normal handoff, deadline escape,
rollback, controller fail-open, and external HTTP probe scenarios; the newest
scenarios still need a run and review. The GitOps contract is documented and
statically checked, but has not been proven with a live Argo CD sync. No
PodHandoff stage or production availability results exist.

## Pilot acceptance

Use a synthetic one-replica HTTP Deployment behind a Service with a meaningful
readiness endpoint. Capture baseline traffic, a normal worker drain, the
replacement becoming ready on another worker, eviction release, and scale-back
to one replica. Also make the replacement unschedulable and show the hold
relaxing at its deadline, then verify rollback and controller fail-open.

Before a stage pilot, run the Kind suite, prove the scoped Argo CD ownership
rule during an active surge, and complete the disposable TestLab drill with an
external probe, cleanup, and captured evidence. The TestLab sequence remains
outside this repository because it contains personal-lab routing. Keep the
first stage pilot limited to a synthetic or explicitly approved stateless
workload; do not infer production readiness from unit tests or a Kind demo.

## Upstream provenance

PodHandoff began as a derivative of Understudy. That provenance is permanent
for code and other material derived from it; see [UPSTREAM.md](../UPSTREAM.md).
The public project earns its own identity through a distinct product boundary,
tested behavior, maintainer decisions, and releases—not by hiding that origin.
