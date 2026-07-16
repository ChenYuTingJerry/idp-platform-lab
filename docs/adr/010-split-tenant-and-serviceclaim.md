# ADR 010: Split provisioning (Tenant) from workload (ServiceClaim)

- **Status:** Accepted
- **Date:** 2026-06-24
- **Implemented:** yes (the Tenant/ServiceClaim split, the two finalizers, and the webhook are all built, see ADR-012 and ADR-013)
- **Deciders:** Yu Ting
- **Related:** ADR-005 (controller as reconciler), ADR-006 (ServiceClaim naming;
  updated by this ADR), ADR-007 (extensible IDP; CR relationships need design),
  ADR-008 (quota + RBAC model; ownership moves here), ADR-009 (workload sync via
  ArgoCD Application; unchanged in substance)

> **Update (2026-07-15):** The two finalizers (Decision 4) and the validating webhook (Decision 5) are now implemented; see ADR-012 and ADR-013. Name and non-negative validation also moved into the CRD as CEL rules. Passages below that describe these as M4 or "designed, not built" are historical.

## Context

After M3, one `ServiceClaim` reconciles into a team `Namespace`, a `RoleBinding`,
a `ResourceQuota`, and an ArgoCD `Application`. That works for one service per
team, but the model conflates two different granularities on one object, and it
is actually broken for the realistic case.

A `ServiceClaim` is named after a **service** (`svc = claim.Name`) and carries
service-level fields (`image`, `replicas`). Yet it also creates **team-level**
resources: the namespace `team-<team>`, the RoleBinding `team-edit`, and the
ResourceQuota `team-quota`. Those are one-per-team, not one-per-service.

The break: when a team creates a **second** `ServiceClaim`, the second reconcile
calls `controllerutil.SetControllerReference(claim, obj, ...)` on the shared
namespace / RoleBinding / ResourceQuota. Those objects already have the first
claim as their controller owner, so the call returns `AlreadyOwnedError`, the
reconcile fails, and the second service never goes `Ready`. On top of that, each
claim writes the shared `team-quota` from its own `spec.resources`, so two claims
would fight over one quota. In practice, **a team can have only one
ServiceClaim** today, even though ADR-006 says "a team can have multiple claims."

ADR-007 already flagged this: "When a second CRD is added, their ownership and
ordering (does one reference the other? who owns teardown?) must be designed.
This is real work, deferred until the second CRD is actually added." This ADR is
that work.

## Decision

Split the one CRD into two, along the team / service seam.

### 1. `Tenant` (new, cluster-scoped): the team-level provisioning resource

A `Tenant` owns the team namespace, the RoleBinding, and the ResourceQuota. One
per team. It is where a team declares its allocation.

- **The Tenant name is the team name.** There is no `spec.team` field. One Tenant
  per team then becomes structural: the API server enforces unique names, so the
  invariant needs no controller logic to police. The namespace stays
  `team-<tenant.Name>`, so the workloads path and the ArgoCD destination are
  unchanged.
- `spec.resources` (the `cpu` / `memory` / `pods` block) moves from ServiceClaim
  to Tenant. Resource allocation is a team-level "what", shared by all the team's
  services, so it belongs on the per-team object.
- Because a Tenant is the single owner of the shared resources, the
  `AlreadyOwnedError` cannot happen. This is the fix.

### 2. `ServiceClaim` (slimmed): the service-level workload resource

`ServiceClaim` keeps `team` (now a **reference** to a Tenant by name), `image`,
and `replicas`. It drops `spec.resources`. It only creates the ArgoCD
`Application`. Many ServiceClaims can point at one Tenant.

### 3. Create ordering is "pending", not "rejected"

A ServiceClaim whose Tenant does not exist yet (or is not `Ready`) does not
error and does not create an Application. It reports `TenantReady=False` and
waits, re-reconciling when the Tenant becomes `Ready`. We do **not** reject it at
the webhook.

The reason is declarative apply: a user (or ArgoCD) commonly applies a folder
holding both the Tenant and its ServiceClaims in one shot, and apply order inside
a batch is not guaranteed. A hard reject would make a valid bundle fail depending
on ordering. "Referenced object not created yet" is a not-ready state, not an
invalid one. This also stops a ServiceClaim from pointing ArgoCD at a namespace
that does not exist yet.

### 4. Teardown ordering is a finalizer's job, not garbage collection's

A `ServiceClaim` does **not** carry an owner reference to its `Tenant`. Owner-ref
GC gives no ordering and would let deleting a Tenant silently cascade a team's
services away with no chance to drain ArgoCD first. Instead:

- The team resources (namespace, RoleBinding, quota) are owned by the `Tenant`,
  so GC still handles the simple case.
- The ArgoCD `Application` is owned by its `ServiceClaim`, same as today.
- Two finalizers add the **ordering** and the **cross-CRD safety** that GC does
  not:
  - The ServiceClaim finalizer removes the `Application` first, so ArgoCD drains
    the workload before the namespace can go away.
  - The Tenant finalizer blocks deletion while any ServiceClaim still references
    the team. It sits in `Terminating` until the last one is gone, then deletes
    the namespace. The RoleBinding and quota live inside the namespace, so they
    die with it. (The ROADMAP's "Namespace then RBAC" ordering predates the
    split; post-split the RBAC is intra-namespace, so namespace teardown covers
    it.)

### 5. The webhook realizes ADR-008's deferred quota ceiling

ADR-008 deferred the platform-enforced cap (reject an oversized ask) to "M4's
validating webhook." That ceiling now lives on the `Tenant` (since `resources`
moved there): a validating webhook rejects a Tenant whose resources exceed a
configured maximum, and rejects a name that would make `team-<name>` an invalid
namespace.

## Consequences

- A team can finally have many services: many ServiceClaims, one Tenant, no
  ownership fight. This is the multi-claim behavior ADR-006 always claimed.
- Concerns are separated cleanly. Provisioning (namespace, RBAC, quota) is a
  Tenant; deployment (image, replicas, ArgoCD Application) is a ServiceClaim.
  This is the shape ADR-007 wants for the extension model.
- It is a breaking change to the API: `ServiceClaim.spec.resources` is gone, and
  a Tenant must exist for a team before its ServiceClaims go `Ready`. This is
  fine at `v1alpha1` (pre-1.0). No conversion webhook; the samples and docs are
  updated instead.
- The team-facing contract is still spec-driven. Teams apply a Tenant and some
  ServiceClaims. They never see the namespace, RBAC, quota, ArgoCD, or Kustomize.
- Two objects now have a lifecycle relationship, which is genuinely more moving
  parts (a reference, a pending state, two finalizers). The finalizer dependency
  is one-directional (a Tenant waits on its claims; a claim never waits on its
  Tenant during its own deletion), so there is no mutual deadlock.

## Alternatives considered

- **Keep one CRD; one claim = one service; share the team resources with
  reference counting and quota aggregation.** Keeps the current object but makes
  the controller own shared resources without a single owner, delete the
  namespace only when the last claim is gone, and sum every claim's resources
  into one quota. Rejected: the most controller logic (aggregation plus
  ref-counting) for the least conceptual clarity. The team allocation is still
  smeared across per-service objects.
- **Keep one CRD; one claim = one whole team, with a `services` list.** The claim
  goes back to team granularity and carries `services: [{name, image,
  replicas}]`. Single owner, no conflict, clean teardown. Rejected: changing one
  service means re-applying the whole team's list, and it contradicts ADR-006's
  per-service claim. It also does not match how teams think ("I deploy a
  service"), which is what the ServiceClaim name promises.
- **Keep one CRD; enforce one-claim-per-team at the webhook.** Simplest, but it
  just formalizes the current limitation instead of removing it. A team still
  cannot run two services. Rejected: it solves the symptom, not the need.

## What this updates

- **ADR-006** ("a team can have multiple claims") becomes true for the first
  time, and this ADR explains why it was not.
- **ADR-008**'s quota and RBAC model is unchanged in substance, but it now lives
  on the `Tenant`, and its deferred webhook ceiling is realized here.
- **ADR-009** is unchanged in substance: the `Application` is still owned by the
  `ServiceClaim`, still built as unstructured, still targets `team-<team>`. Only
  the namespace's provisioner changed (now the Tenant), which is why ADR-009's
  "ordered teardown is M4" line is fulfilled by this work.
