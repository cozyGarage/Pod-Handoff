# PodHandoff drain comparison

## Finding

In two controlled trials, a one-replica HTTP canary had a 16.3–16.6 second
gap between successful responses when Kubernetes drained its only serving
node without PodHandoff. With PodHandoff enabled, all 538 sampled requests
returned HTTP 200. The protected drain took about 21–22 seconds while the
stand-in became ready, and the probe continued succeeding during that wait.

This is evidence for a narrow use case: a single-replica stateless service
that can safely run two instances briefly, but takes long enough to start that
evicting its only Pod first creates a user-visible gap. It is evidence from a
small synthetic lab experiment, not a high-availability or production claim.

## Results

| Trial | Mode | Requests | Failed | Failure rate | Longest gap between successful probes | Drain command |
|---|---|---:|---:|---:|---:|---:|
| 1 | Plain Kubernetes drain | 138 | 29 | 21.01% | 16.307 s | not recorded |
| 1 | PodHandoff | 251 | 0 | 0% | 0.219 s (normal probe spacing) | about 21 s to release |
| 2 | Plain Kubernetes drain | 160 | 30 | 18.75% | 16.551 s | 1 s |
| 2 | PodHandoff | 287 | 0 | 0% | 0.229 s (normal probe spacing) | 22 s |
| Both | Plain Kubernetes drain | 298 | 59 | 19.80% | 16.551 s | — |
| Both | PodHandoff | 538 | 0 | 0% | 0.229 s (normal probe spacing) | — |

The gap is the time between the last HTTP 200 before the failure run and the
first HTTP 200 after it. In the protected runs, there were no failures; the
reported interval is the largest ordinary interval between adjacent probes.
The probe checks status codes, not response latency.

Raw samples: [trial 1 baseline](baseline-01.csv),
[trial 1 PodHandoff](podhandoff-01.csv),
[trial 2 baseline](repeat-02/baseline.csv), and
[trial 2 PodHandoff](repeat-02/podhandoff.csv).

## Scenario

- Three Debian 13 VMs on one Proxmox node, running K3s `v1.35.8+k3s1`:
  one control-plane node and two workers, each 2 vCPU, 4 GiB RAM, and 32 GiB
  disk.
- A one-replica Go HTTP Deployment with a readiness endpoint, a 15-second
  initial readiness delay, and a NodePort Service on port 30080. The
  Deployment used `maxUnavailable: 0` and `maxSurge: 1`.
- A separate probe process on the control-plane VM sent requests about every
  0.2 seconds through the Service. It recorded UTC timestamps and HTTP status
  codes, with a one-second per-request timeout.
- Each run drained `podhandoff-work1` with
  `kubectl drain --ignore-daemonsets --delete-emptydir-data`. The canary
  replacement could schedule on `podhandoff-work2`.
- Protected runs used `holdMode: always`, one surge replica,
  `minSurgeTimeSeconds: 5`, and `readinessDeadlineSeconds: 90`.

Trial 1 ran at 21:32–21:36 UTC. Trial 2 ran at 22:06–22:09 UTC on 2026-09-28.
For trial 2, the plain drain completed at 22:07:11 UTC and the protected
drain at 22:09:03 UTC. During the protected drain, the eviction webhook
rejected retries while preparing the stand-in; after it became Ready on
`work2`, the retry was admitted.

The controller image was tagged `podhandoff-lab-controller:0.1.0`. Its source
revision was not recorded, so these results demonstrate the tested lab
configuration and should not be attributed to a specific Git commit.

The worker nodes were uncordoned after the experiment. The canary and
PodHandoff resource were left running in the disposable lab.

## Two-replica comparison

To check whether PodHandoff adds observed availability when the workload
already has redundancy, the same canary was run with two replicas spread
across `work1` and `work2`. The `work1` drain was repeated once without a
PodHandoff resource and once with it. Both runs returned HTTP 200 for every
sample:

| Mode | Requests | Failed | Failure rate | Longest success gap | Drain command |
|---|---:|---:|---:|---:|---:|
| Kubernetes only | 357 | 0 | 0% | 0.218 s | 2 s |
| PodHandoff | 310 | 0 | 0% | 0.225 s | 23 s |

Kubernetes alone kept the unaffected replica serving while it replaced the
evicted Pod. PodHandoff waited for a third Ready Pod before eviction, keeping
two Ready replicas through the handoff. This trial did not show a request
availability improvement over the two-replica baseline; it did show that
PodHandoff can preserve replica capacity during a planned drain, at the cost
of a longer drain. The probe did not measure throughput, so it cannot show
whether maintaining two Ready replicas improved capacity under load.

The [probe samples](multi-replica/probe.csv) and
[Pod watch](multi-replica/pods.watch) are preserved. The watch shows the
stand-in reached `1/1 Running` before the old Pod entered `Terminating`. It
also shows the Deployment briefly creating a second replacement while the old
Pod was terminating; PodHandoff scaled from 3 replicas back to 2 and that new
Pod was deleted before becoming Ready. This is one observed run, so repeat it
before deciding whether to change the scale-back timing.

The controller's Prometheus counters and process metrics were sampled just
before and after the protected two-replica run. Over about 58 seconds,
`process_cpu_seconds_total` rose from 3.81 to 3.91, resident memory stayed at
about 61 MiB, heap allocation rose from 6.5 to 9.4 MB, reconcile count rose
from 102 to 138, and reconcile errors stayed at 0. This is a pair of samples,
not a CPU profile; it does not identify function-level hot paths or capture
peak resource use. The manager does not expose pprof or a webhook-duration
histogram today.

## Unready replacement and deadline release

For this run, the canary ran on `work1`, the manager remained on `work2`, and
both canary-eligible workers were cordoned so no replacement could become
Ready. With a 15-second readiness deadline, the webhook returned HTTP 429
while the controller prepared a stand-in. The `HoldRelaxed` event was
recorded at 22:37:50.254 UTC, about 15 seconds after the `SurgeStarted` event
at 22:37:35.193 UTC. The drain then finished at 22:37:56 UTC without a Ready
replacement.

The probe recorded 139 failed samples and an 82.789-second gap between
successful responses. Most of that gap was after the deadline: the test kept
both workers unschedulable until we restored capacity, so it demonstrates the
limit of the deadline behavior. PodHandoff bounds how long it holds eviction;
it cannot keep serving after the only Pod is evicted when no replacement node
is available. See the [probe](unready-timeout/probe.csv) and
[PodHandoff events](unready-timeout/events.txt).

## Controller unavailable

We scaled the sole controller to zero, verified the webhook had no endpoints
and `failurePolicy` was `Ignore`, then drained the node running the canary.
The drain completed in 2 seconds. The replacement took about 15 seconds to
become Ready; the probe recorded 29 failed samples out of 127 and a
16.435-second success gap. This confirms the fail-open tradeoff: maintenance
can proceed, but PodHandoff provides no protection while its webhook is
unavailable. The [probe](controller-down/probe.csv) is preserved.

This tests an unavailable webhook, not a webhook that accepts a connection
and hangs until `timeoutSeconds` expires. That API-server timeout path still
needs an integration test.

## What this supports

Kubernetes reschedules a Pod after eviction, but for a one-replica Deployment
that still leaves a gap while the replacement starts and becomes Ready.
Deployment rollout settings do not make a node drain wait for a healthy
replacement. In this experiment, PodHandoff made the drain wait instead: the
operator prepared temporary capacity, and admission allowed the eviction
retry after the stand-in was Ready.

A plausible pilot candidate is a stateless internal API or control-plane
service with a slow cold start, a meaningful readiness check, and safe
short-lived overlap. If permanent redundancy is affordable or protection
from unannounced failures is required, use multiple replicas and suitable
failure-domain placement instead.

## Limits and next evidence

- The single-replica comparison has two trials per mode, against one
  synthetic service and planned worker drains. This is a useful signal, not a
  reliability estimate.
- The two-replica comparison is one trial per mode. It does not test
  throughput or whether preserving capacity changes user latency under load.
- The probe ran from another VM in the same cluster network. It did not
  represent an end-user path or record request latency, throughput, or
  application-level correctness.
- No VM crash, network partition, application crash, overloaded service,
  stateful workload, or GitOps reconciliation was tested.
- The test does not establish protection from unannounced failures. The
  eviction webhook cannot hold an eviction after the node or control plane
  has already failed.
- A production-oriented pilot should repeat the comparison more times, use
  an externally located probe, record latency and application errors, and
  exercise the webhook timeout, rollback, and cleanup paths.

The manifests and probe used for trial 1 are preserved here:
[canary](canary.yaml), [PodHandoff](podhandoff.yaml), and [probe](probe.sh).
