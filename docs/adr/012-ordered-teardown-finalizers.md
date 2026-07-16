# ADR 012: Ordered teardown via two finalizers

- **Status:** Accepted
- **Date:** 2026-07-15
- **Implemented:** yes (`internal/controller/finalizers.go`, `reconcileDelete` in both controllers; proven by envtest)
- **Deciders:** Yu Ting
- **Related:** ADR-005 (controller as reconciler; named finalizers but did not build them), ADR-009 (scoped teardown to "M4"), ADR-010 Decision 4 (designed the two finalizers)

## Context

Earlier ADRs named finalizers three times (ADR-005, ADR-009, ADR-010) and built
none. Deletion relied on owner-reference garbage collection, which is unordered.
That leaves a real bug: delete a `Tenant`, the garbage collector removes the
`team-<x>` namespace at once, but the `ServiceClaim`'s ArgoCD `Application` (which
lives in the `argocd` namespace and is owned by the claim, not the Tenant)
survives and keeps trying to sync into a namespace that no longer exists.

A second bug surfaced while implementing this: the `Application` carried no
ArgoCD cascade finalizer, so deleting a `ServiceClaim` made ArgoCD simply forget
the workload. The `Deployment` and `Service` kept running in the team namespace,
unmanaged, forever.

## Decision

Two finalizers, plus ArgoCD's own cascade finalizer on the generated Application.

1. **`ServiceClaim` finalizer** `platform.idp.io/application-teardown`. On
   delete, it deletes the ArgoCD `Application` and waits until that object is
   actually gone before releasing. The Application carries ArgoCD's
   `resources-finalizer.argocd.argoproj.io`, added at creation, so deleting it
   makes ArgoCD prune the workload first. Without that cascade finalizer, "drain"
   is a word with no referent.

2. **`Tenant` finalizer** `platform.idp.io/tenant-teardown`. On delete, it
   blocks while any `ServiceClaim` references the Tenant (found through the
   `spec.team` field index), then deletes the namespace and releases. Blocking is
   deliberate: cascade-deleting the claims would let `kubectl delete tenant`
   silently destroy every running service a team has.

The load-bearing insight, which the earlier ADRs never stated: the Tenant's block
is what makes the claim's drain meaningful. The namespace survives, because the
Tenant's finalizer stops GC, exactly long enough for ArgoCD to prune each workload
into it before it goes away. Neither finalizer works without the other.

**Deadlock is impossible by construction, and this is proven, not asserted.** The
wait-for graph is a strict partial order: `Tenant < ServiceClaim < Application <
ArgoCD`, with no back edge. There is exactly one way to create a cycle, so it is
recorded as an invariant:

> **D1: the ServiceClaim's deletion path must never read or wait on its Tenant.**

The claim's deletion branch is therefore the first statement after the `Get`, with
a comment saying why, and a dedicated test deletes a claim whose Tenant does not
exist at all and proves the claim still tears down. A future change that adds the
back edge fails that test at once.

## Consequences

- Teardown is ordered: workloads drain before their namespace is removed, and a
  namespace is never removed out from under a running service.
- Deleting a `ServiceClaim` now prunes its workload instead of orphaning it.
- Blocking states are surfaced, not silent. If ArgoCD is down, the claim reports
  `ApplicationDrained=False` with the blocking finalizers listed verbatim, and the
  Tenant reports `ClaimsDrained=False` naming each remaining claim. The documented
  escape hatch is `kubectl patch application <svc> -n argocd -p '{"metadata":
  {"finalizers":null}}'`, which is what `argocd app delete --cascade=false` does.
- A failed list of claims returns an error and never releases the Tenant
  finalizer. Treating a failed list as "no claims remain" would delete the
  namespace under a running service; that path is guarded and commented.
- `kubectl delete tenant --cascade=foreground` still defeats the ordering
  (foreground GC deletes the namespace before the finalizer runs). Use the default
  cascade. This caveat is documented rather than worked around.

## Alternatives considered

- **Auto-force teardown after a timeout.** Rejected as a default: it would make
  teardown always complete, but it would also let a broken ArgoCD go unnoticed
  ("teardown completed" hides "ArgoCD is broken, come look"). Available later as an
  opt-in flag defaulting to disabled.
- **Drop the namespace's owner reference and make the finalizer the only
  deleter.** This would close the `--cascade=foreground` hole completely, but it
  loses the GC safety net: a force-removed finalizer or a controller uninstalled
  mid-teardown would orphan the namespace forever. Kept the owner reference.
