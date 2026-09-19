# GitOps coexistence

PodHandoff temporarily owns exactly two fields on each protected Deployment:

- `/spec/replicas`
- `/metadata/annotations/podhandoff.io~1base-replicas`

Argo CD automated self-healing must ignore those fields while respecting the
ignore rules during sync. Scope the rule to each protected Deployment; do not
create a cluster-wide exception.

```yaml
spec:
  ignoreDifferences:
    - group: apps
      kind: Deployment
      name: <deployment>
      namespace: <namespace>
      jsonPointers:
        - /spec/replicas
        - /metadata/annotations/podhandoff.io~1base-replicas
  syncPolicy:
    syncOptions:
      - RespectIgnoreDifferences=true
```

The owning Application or ApplicationSet must carry this configuration before
the pilot. Prove the contract during an active surge:

1. Argo CD remains `Synced` and does not reset the temporary replica count.
2. PodHandoff records the original replica count in the annotation.
3. The replacement becomes Ready on a non-doomed node.
4. After eviction, PodHandoff restores the replica count and removes only its
   own annotation.
5. Argo CD still converges an unrelated intentional Deployment change.

Do not use this pattern on a Deployment whose replicas are actively owned by an
HPA until field ownership has been tested explicitly. Flux support also remains
unverified; do not infer an equivalent rule from this Argo CD example.

`hack/check-gitops-contract.sh` is a static contract test. It fails CI if the
controller annotation or the documented Argo CD paths drift apart.
