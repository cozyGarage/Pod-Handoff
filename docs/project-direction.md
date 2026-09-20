# PodHandoff project direction

PodHandoff is not a general high-availability product and it does not promise
survival of an unannounced node failure. Its distinct problem is narrower and
operationally useful: preserve a ready endpoint for a **planned Kubernetes
disruption** without paying for a permanent second application replica.

## Product thesis

For a stateless Deployment that can safely overlap old and replacement Pods,
the reliability question during a drain is not “how quickly can Kubernetes
reschedule?” It is “can the old endpoint remain available until the replacement
is demonstrably ready?” PodHandoff coordinates that handoff, then releases its
temporary capacity immediately after the disruption.

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

## Pilot narrative

The first public demonstration should use a synthetic one-replica HTTP
Deployment behind a Service with a meaningful readiness endpoint. Show the
baseline, a normal worker drain, a replacement becoming ready on a second
worker, the held eviction being admitted, and scale-back to one replica.

The equally important second demonstration is the failure path: make the
replacement unschedulable and show the readiness deadline relaxing the hold so
the drain cannot wedge indefinitely. Include the GitOps ownership fixture and
the controller fail-open posture. This is a stronger and more credible story
than claiming universal zero downtime.

The TestLab sequence lives outside this public repository because it contains
personal-lab routing. The portable acceptance criteria are: a disposable
multi-worker cluster, a synthetic stateless canary, an external probe, normal
handoff, deadline escape, rollback, cleanup, and captured evidence.

## Near-term roadmap

1. Make the Kind e2e suite reproduce the normal handoff, deadline escape,
   rollback, and controller-replica-loss scenarios.
2. Publish a versioned Helm chart and image only after the license and SBOM
   gates, tests, and release notes are complete.
3. Add a portable GitOps fixture that proves narrowly scoped Argo CD coexistence.
4. Run the full synthetic TestLab drill before considering any stage pilot.
5. Keep cloud sentinels optional and experimental until each provider is
   validated against its real interruption mechanism.

## Upstream provenance

PodHandoff began as a derivative of Understudy. That provenance is permanent
for code and other material derived from it; see [UPSTREAM.md](../UPSTREAM.md).
The public project earns its own identity through a distinct product boundary,
tested behavior, maintainer decisions, and releases—not by hiding that origin.
