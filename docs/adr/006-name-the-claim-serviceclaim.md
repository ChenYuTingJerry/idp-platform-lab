# ADR 006: Name the team-facing CR `ServiceClaim`

- **Status:** Accepted (updated by ADR-010)
- **Date:** 2026-05-18
- **Implemented:** yes
- **Deciders:** Yu Ting
- **Related:** ADR-005 (controller as reconciler), ADR-010 (split Tenant from
  ServiceClaim)

> **Updated by ADR-010.** This ADR says "a team can have multiple claims." That
> was not actually true until ADR-010 split the team-level provisioning into a
> separate `Tenant` CRD. Before that split, a second `ServiceClaim` for a team
> failed with `AlreadyOwnedError` on the shared namespace. `ServiceClaim` is now
> a purely service-level resource; the team namespace, RBAC, and quota belong to
> the `Tenant`.

## Context

ADR-005 introduced a custom CRD that teams apply to declare the
service they want. The earliest draft called the kind `ServiceSpec`.
That name has two problems.

First, it conflicts with `k8s.io/api/core/v1.ServiceSpec`. Any
controller file that imports `corev1` and our own API package has two
`ServiceSpec` types in scope and must use an import alias on every
reference. This is daily friction with no design value.

Second, the name breaks Kubernetes naming convention. Kinds in
Kubernetes are nouns describing what the resource is, not the schema
field they expose. `Pod`, `Deployment`, `Application` (ArgoCD),
`Certificate` (cert-manager), `Workflow` (Argo Workflows). The string
"Spec" is the field name inside the resource (`.spec`), not part of
the kind. `ServiceSpec.Spec.Image` reads awkwardly.

Beyond mechanics, the name should describe what the resource *is*
from the team's perspective. The CR is the contract a team uses to
ask the platform for a service. The team declares; the platform
fulfills. That pattern has a precedent in Kubernetes: the
`PersistentVolumeClaim` model.

## Decision

Name the team-facing CR `ServiceClaim`.

- Group: `platform.idp.io`
- Version: `v1alpha1`
- Scope: cluster-scoped (a team can have multiple claims; the
  controller routes to per-team namespaces)
- File location after kubebuilder scaffold:
  `api/v1alpha1/serviceclaim_types.go`

A team writes:

```yaml
apiVersion: platform.idp.io/v1alpha1
kind: ServiceClaim
metadata:
  name: api-gateway
spec:
  team: alpha
  image: nginx:1.25
  replicas: 2
```

The controller fulfills the claim by creating a Namespace,
RoleBinding, ResourceQuota, and ArgoCD Application. The
`ServiceClaim` is the contract; the resulting infrastructure is the
fulfillment.

## Alternatives Considered

### `ServiceSpec` (original draft)

The name we started with. Rejected for the reasons above:
`corev1.ServiceSpec` conflict, naming convention mismatch, awkward
field access.

### `PlatformService`

Considered. Pros: clear, no conflicts, ties to the Internal
Developer Platform framing. Cons: less precise about the
"declarative contract" semantics. A team writing
`PlatformService` reads like they're declaring a service that
*is* a platform-service, rather than *claiming* one. The Claim
suffix carries that meaning natively.

### `Application`

Used by ArgoCD. Would conflict in any controller code that imports
both ArgoCD's API package and ours. Same reason as
`corev1.ServiceSpec`.

### `Workload`

Used by KCP and Cluster API in related but different roles.
Reasonable name but emphasises the execution unit rather than the
team-platform contract. Less aligned with the SDD framing.

### `PlatformWatchmen`

Briefly considered. Rejected: "Watchmen" describes the controller's
behaviour (watching) not the resource's identity (a declarative
claim). The controller watches `ServiceClaim` resources; the
resource is not itself a watcher.

## Consequences

### Positive

- **No naming collisions** with K8s core, ArgoCD, cert-manager,
  Crossplane, or any other operator we're likely to integrate with.
- **PVC precedent** gives users an immediate mental model. Anyone
  who has written a PVC will read `ServiceClaim` and understand the
  shape: declare what you want, the system fulfills.
- **Clean Go code** with no import aliases. `serviceclaim.Spec`
  reads naturally.
- **Future room for a Crossplane-style two-tier model** if needed:
  `ServiceClaim` (namespace-scoped, team-facing) could pair with a
  cluster-scoped `Service` composite later. Not built now, not
  blocked either.

### Negative

- **Historical docs use `ServiceSpec`.** ADR-004 (superseded),
  04-30 / 05-03 / 05-07 journal entries reference the older name.
  These are not renamed; they record what was true at the time.
  Future readers see the evolution.
