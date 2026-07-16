# ADR 009: Workload Sync via a Controller-Rendered ArgoCD Application

- **Status:** Accepted
- **Date:** 2026-06-18
- **Implemented:** partial (the unstructured Application and kustomize overrides are built; "manifests in a separate repo" is superseded by ADR-011; the read-back verification is deferred, see ADR-014)
- **Deciders:** Yu Ting
- **Related:** ADR-005 (controller as reconciler; controller + ArgoCD division of
  labor), ADR-007 (extensible self-service IDP; teams never see ArgoCD), ADR-008
  (keep `ServiceClaim` small)

> **Update (2026-07-15):** Two things changed since this was written. (1) Workload manifests now live IN this repo under workloads/<team>/<svc>/, not in a separate workloads repo; see ADR-011. (2) Ordered teardown, described here as an M4 finalizer, is now built; see ADR-012. The Application now also carries platform.idp.io/team and /claim labels and ArgoCD's resources-finalizer. Passages below describing a separate repo or GC-based unordered teardown are historical.

## Context

M3 closes the loop: a `ServiceClaim` should not just carve out a namespace, it
should get the team's workload running. ADR-005 already set the division of
labor. The controller is the reconciler; ArgoCD is the workload syncer. M3 is
where that line gets drawn in code, and a few questions came up that are worth
recording.

1. Where do the workload manifests live, and who writes them?
2. How does the controller create the ArgoCD `Application` without pulling the
   whole argo-cd Go module into the build?
3. How does a team's declared `image` and `replicas` reach the workload without
   the team editing YAML (which would break the ADR-007 abstraction)?
4. The `Application` lives in the `argocd` namespace, but the claim is
   cluster-scoped. Does ownership and teardown still work?

## Decision

### 1. Workload manifests live in a separate repo; the controller renders, it does not author

The manifests live in a separate Git repo, under
`workloads/<team>/<svc>/`, where `<svc>` is the claim name. The platform owns a
small Kustomize base per service there. The controller does **not** generate or
commit manifests. It creates one ArgoCD `Application` that points ArgoCD at that
path and lets ArgoCD sync.

The repo URL is **platform config**, passed to the controller as
`--workloads-repo-url` (and `--workloads-target-revision`). It is deliberately
**not** a field on the `ServiceClaim`. The team declares *what* (a service, an
image, a size); the platform decides *where the manifests come from*. Putting the
repo URL on the claim would leak a "how" and break ADR-007.

This keeps the SDD boundary intact, but it does surface a real tension: someone
still has to put a base manifest at `workloads/<team>/<svc>/`. For M3 the platform
seeds it. Who owns that path long term (platform-templated vs team-committed) is
left open and revisited when the extension model (ADR-007) is built out. The
honest position for now: the controller's contract is "point ArgoCD at a path";
the path's contents are a separate concern.

### 2. The Application is an unstructured object, not a typed import

The controller builds the `Application` as an `unstructured.Unstructured` with
the `argoproj.io/v1alpha1` GVK set, and applies it with the same
`controllerutil.CreateOrUpdate` pattern the other reconcile steps use. It does
**not** import `github.com/argoproj/argo-cd/v2`.

The argo-cd module drags in a large dependency tree and frequently needs `go.mod`
replace directives to resolve Kubernetes version conflicts. For a controller that
only sets a handful of fields, that maintenance cost is not worth a typed struct.
The cost of unstructured is no compile-time field checking; we cover that with
envtest assertions on the exact field paths instead. The REST mapper resolves the
kind because the ArgoCD CRD is installed in the cluster, so no scheme
registration is needed.

### 3. image and replicas become Kustomize overrides

The team's `spec.image` and `spec.replicas` are passed to ArgoCD as Kustomize
overrides on the `Application`:

- `spec.source.kustomize.images: ["app=<image>"]`
- `spec.source.kustomize.replicas: [{name: <svc>, count: <replicas>}]`

This is a hidden contract between the controller and the workloads repo: the base
`Deployment` must be named `<svc>` (the claim name) and its container image must
be the literal placeholder `app`, so the overrides match and replace them. An
override is only set when the field is present, so an unset image or replica count
falls back to whatever the base manifest declares.

We chose Kustomize over Helm because the rest of the project is Kustomize-native
(the ArgoCD install and the controller's own `config/` both use it) and a plain
base needs no chart boilerplate. The trade-off is the name-matching contract
above; Helm parameters would avoid it but add a chart per service.

### 4. The claim owns the Application across the namespace boundary

The `Application` is created in the `argocd` namespace (where ArgoCD watches by
default), but it carries a controller owner reference back to the cluster-scoped
`ServiceClaim`. This is allowed: a namespaced object may have a cluster-scoped
owner, and garbage collection resolves it correctly. The same pattern already
works in M2, where the cluster-scoped claim owns the `RoleBinding` and
`ResourceQuota` inside the team namespace.

So deleting a claim garbage-collects its Application. What M3 does **not** do is
*ordered* teardown. GC gives no ordering, so on delete the namespace and the
Application may be collected in any order. Ordered teardown (Application ->
namespace -> RBAC) needs a finalizer and is scoped to M4. For M3, GC-on-delete is
enough, and the Application also carries `platform.idp.io/claim` and
`platform.idp.io/team` labels so a future finalizer (and humans) can find it.

### 5. The ArgoAppCreated condition reflects creation, and carries health

A new `ArgoAppCreated` condition goes `True` as soon as the Application is
applied, and the aggregate `Ready` includes it. The condition does **not** gate
`True` on ArgoCD reporting the workload healthy, for two reasons: envtest has no
ArgoCD controller, so health never populates there; and gating on health would
make `Ready` flap every time ArgoCD moves between `Progressing` and `Healthy`.
Instead, the controller reads ArgoCD's live `status.health.status` and
`status.sync.status` off the Application and surfaces them in the condition
*message*. The `Owns` watch re-queues the claim whenever ArgoCD writes status, so
the message tracks the live state without `Ready` flapping.

## Consequences

- The controller stays small and dependency-light: no argo-cd Go module, one new
  reconcile step shaped like the existing three.
- The SDD contract holds. Teams set `image`, `replicas`, `resources`, and a team
  name. They never see ArgoCD, the repo URL, or Kustomize.
- There is a naming contract with the workloads repo (base `Deployment` named
  `<svc>`, image placeholder `app`). It is documented here and in the reconciler;
  if a base does not follow it, the overrides silently do nothing.
- Deletion is GC-based and unordered until M4 adds a finalizer. Called out, not an
  oversight.
- envtest needs the ArgoCD Application CRD. It is vendored at
  `test/testdata/crds/applications.yaml`, rendered from the argo-cd Helm chart. It
  must be re-rendered if the chart version changes.

## Alternatives considered

- **Import the typed argo-cd API.** Compile-time safety and IDE help, but a heavy
  dependency tree and recurring `go.mod` replace churn for a few fields. Rejected
  in favor of unstructured plus envtest assertions.
- **Render a Deployment in the controller and apply it directly.** Makes the
  `ServiceClaim` the source of truth, but cuts ArgoCD out of the workload path,
  which contradicts ADR-005 and the project identity. Rejected.
- **Put the workload manifests in the platform repo.** Simpler to demo, but mixes
  team workload state with platform code and breaks the "manifests live
  separately" goal. Rejected; the workloads repo is separate.
- **Drop the owner reference and link only by label.** Considered because the
  Application sits in another namespace, but the cluster-scoped owner makes the
  owner reference legal and gives GC for free. Kept the owner reference; labels
  are an addition, not a replacement.
- **Gate `Ready` on ArgoCD health.** More honest end-to-end, but flaps and is
  untestable in envtest. Rejected in favor of created-gates-Ready,
  health-in-message.
