# Upstream provenance

PodHandoff is derived from the Apache-2.0 licensed
[Understudy](https://github.com/kylan11/understudy) project.

| Field | Imported baseline |
|---|---|
| Upstream repository | `https://github.com/kylan11/understudy` |
| Release | `v0.4.2` |
| Commit | `c7f51607c91f9e40e654e19b33b2903fdf67d41a` |
| Import date | 2026-09-19 |
| License | Apache License 2.0; see [LICENSE](LICENSE) |

The new repository intentionally starts with fresh Git history. This file is
the permanent bridge back to the exact imported source. Add the upstream
repository as a read-only remote when a hosted PodHandoff repository exists:

```sh
git remote add upstream https://github.com/kylan11/understudy.git
git fetch upstream --tags
```

## Initial PodHandoff divergence

- renamed the project, Go module, API group, CRD, Helm chart, metrics and
  Kubernetes object names from Understudy to PodHandoff;
- changed the API group to `apps.podhandoff.io` and the kind to `PodHandoff`;
- verifies the Pod -> ReplicaSet -> Deployment controller-owner chain instead
  of trusting label selectors alone;
- records and restores pre-existing Pod deletion cost, and does not overwrite a
  newer third-party value;
- does not report a blocked eviction when `holdMode: off` or after release;
- treats an unobserved Deployment generation as a rollout in progress;
- documents and statically tests the Argo CD field-ownership contract.

Upstream measurements and historical behavior retained in documentation are
identified as upstream evidence. They are not treated as proof for a
PodHandoff release.
