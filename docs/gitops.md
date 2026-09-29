# GitOps field ownership (unverified example)

This page is a learning example of field ownership conflicts. During a handoff,
PodHandoff temporarily changes two fields on the target Deployment:

- `/spec/replicas`
- `/metadata/annotations/podhandoff.io~1base-replicas`

If a GitOps reconciler continually reapplies a different replica count or
removes the annotation, it can fight the controller. The example below shows
one scoped Argo CD configuration that may avoid that conflict. It has not been
verified in a live Argo CD installation; treat it as something to test in a
disposable cluster, not a production recipe. Scope ignore rules to the
protected Deployment rather than the whole cluster.

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

To study the interaction, test during an active surge:

1. Argo CD remains `Synced` and does not reset the temporary replica count.
2. PodHandoff records the original replica count in the annotation.
3. The replacement becomes Ready on a non-doomed node.
4. After eviction, PodHandoff restores the replica count and removes only its
   own annotation.
5. Argo CD still converges an unrelated intentional Deployment change.

The HPA interaction is also unverified because both the HPA and PodHandoff can
write replica count. Flux behavior has not been tested; do not assume this
Argo CD example applies to it.

`hack/check-gitops-contract.sh` is a static contract test. It fails CI if the
controller annotation or the documented Argo CD paths drift apart.
