# PodHandoff

PodHandoff is an educational Kubernetes operator written in Go. It is a
hands-on example of how a custom resource, controller, watches, and an
admission webhook can coordinate around a Pod eviction.

The demonstration targets a narrow case: a Deployment receives an announced
node disruption, its replacement needs time to become Ready, and the old Pod
can safely overlap with the replacement. PodHandoff temporarily scales the
Deployment and delays eviction until a ready stand-in exists or a deadline is
reached.

This project is **not a general high-availability solution**. Critical
services should normally use multiple replicas, suitable placement, and
autoscaling where appropriate. PodHandoff cannot prevent sudden node or
application failures, preserve in-flight requests, or make unsafe overlap
safe. Treat it as a learning project, not production-ready software.

## Learning path

1. [Architecture and Kubernetes concepts](docs/architecture.md)
2. [Build, change, and exercise the project](docs/building.md)
3. [Detailed handoff flow](docs/how-it-works.md)
4. [Historical lab experiment and limitations](docs/evidence/pilot-2026-09-28/README.md)

The example is useful for studying:

- custom resources and generated CRDs
- controller-runtime managers, watches, and reconciliation
- validating admission webhooks and the Pod eviction subresource
- readiness, finalizers, owner references, and deletion cost
- fail-open behavior and bounded retries
- testing controllers with envtest and Kind

## Build

The repository uses the Go version in [go.mod](go.mod) (currently Go 1.26).
From a checkout:

```sh
make build
```

This builds `bin/manager` and runs code generation, formatting, and `go vet`.
For the test layers and required local tools, see the [build guide](docs/building.md).

## Repository map

| Path | What to explore |
|---|---|
| `api/v1alpha1` | Custom resource types and schema markers |
| `cmd/main.go` | Manager startup and component wiring |
| `internal/controller` | Reconciliation and Deployment lifecycle |
| `internal/signal` | Disruption observations and adapters |
| `internal/ownership` | Verifying Pod ownership through ReplicaSets |
| `config/crd` and `config/webhook` | Generated Kubernetes API resources |
| `test` | Unit, envtest, Kind, and synthetic workload examples |
| `charts/podhandoff` | Helm packaging example |

PodHandoff derives from [Understudy](UPSTREAM.md); the repository records its
provenance and local changes there.
