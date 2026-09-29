# Build and explore the project

This guide follows the code from its API definition to a runnable controller.
It is intended for learning and local development, not production deployment.

## Requirements

- Go version from `go.mod` (currently Go 1.26)
- GNU Make
- Internet access the first time Go modules and Makefile tools are downloaded
- Docker and Kind only for building the image or running the cluster-level suite

The Makefile installs pinned helper binaries such as `controller-gen`,
`setup-envtest`, and `kustomize` under `bin/` when needed.

## Build the manager

```sh
make build
```

The output is `bin/manager`. This target also runs `manifests`, `generate`,
`fmt`, and `vet`; generated files or formatting may change in the checkout.
Review those changes after the build. CI separately checks that generated
files are current.

To build the container image:

```sh
make docker-build IMG=podhandoff:dev
```

## Make an API change

1. Edit the API type and its validation/defaulting markers in
   `api/v1alpha1/podhandoff_types.go`.
2. Update controller behavior and tests in `internal/controller` or the
   relevant signal adapter.
3. Regenerate the API artifacts:

   ```sh
   make manifests generate
   ```

4. Review the CRD under `config/crd/bases`, Helm's CRD under
   `charts/podhandoff/crds`, and generated Go code. This repository keeps the
   deployable copies committed.
5. Run the smallest appropriate test layer, then the wider suite when the
   change crosses component boundaries.

The API markers are part of the design: they become validation and defaults
that the API server enforces before objects reach the controller.

## Test layers

```sh
make test
```

Runs formatting, generation, vet, and the non-e2e Go tests. Controller tests
use envtest, which starts local Kubernetes API server and etcd binaries; the
Makefile downloads the required envtest assets on first use.

```sh
make test-e2e
```

Runs the e2e-tagged suite in a disposable Kind cluster. It requires Docker and
Kind; the Make target creates the named cluster if absent and deletes that
named cluster after the suite, including when it existed beforehand. Use a
disposable `KIND_CLUSTER` name and read `test/e2e` to understand what the
scenario creates.

The CI workflows are the source of truth for which layers currently run on
push and pull request. Passing these tests shows software behavior in test
clusters; it does not establish production readiness or workload value.

## Suggested reading order

1. `api/v1alpha1/podhandoff_types.go` — the custom API.
2. `cmd/main.go` — manager and component wiring.
3. `internal/signal/signal.go` — provider-neutral disruption signals.
4. `internal/signal/evictionwebhook/adapter.go` — eviction admission.
5. `internal/controller/podhandoff_controller.go` — reconciliation and
   temporary Deployment changes.
6. `internal/ownership/deployment.go` — safe ownership traversal.
7. `internal/controller/podhandoff_controller_test.go` and `test/e2e` —
   behavior at different test levels.

## Small exercises

- Trace an API field from its Go definition through generated CRD to sample
  YAML.
- Follow an eviction through the webhook, signal registry, reconciler, and
  readiness check.
- Change a test so the stand-in never becomes Ready; inspect the bounded
  release behavior.
- Add a signal adapter or validation rule and cover it with a focused test.

See [architecture and concepts](architecture.md) for why the code is split
this way, and [how the handoff works](how-it-works.md) for the lifecycle.
