# ADR 008: Quota and RBAC Model for the Team Namespace

- **Status:** Accepted (updated by ADR-010)
- **Date:** 2026-06-03
- **Implemented:** yes (quota mapping and RBAC are built; the platform ceiling is now enforced by the webhook, see ADR-013)
- **Deciders:** Yu Ting
- **Related:** ADR-005 (controller as reconciler), ADR-007 (extensible
  self-service IDP; keep `ServiceClaim` small), ADR-010 (split Tenant from
  ServiceClaim)

> **Update (2026-07-15):** The platform-enforced quota ceiling that this ADR scopes to "M4's validating webhook" is now built. See ADR-013. Passages below that call it deferred or a "known gap" are historical.

> **Updated by ADR-010.** The quota and RBAC model here is unchanged in
> substance, but it now lives on the `Tenant` CRD, not on `ServiceClaim`
> (resources are a team-level allocation). The platform-enforced ceiling this ADR
> defers to "M4's validating webhook" is realized there as a Tenant-level check.

## Context

M2 extends the reconciler so one `ServiceClaim` produces not just a namespace
but also the RBAC and the resource limits a team needs to use it. Two design
questions came up, and both have real trade-offs worth recording.

1. How does a team express how much compute it wants, and how does that become a
   `ResourceQuota`?
2. Who gets access to the team namespace, and through which role?

The guiding principle from ADR-007 is that `ServiceClaim` stays small and
team-facing. Teams declare the "what"; the platform owns the "how" and hides it.

## Decision

### 1. Resources are free-form, not fixed tiers

The team declares totals in a small `spec.resources` block: `cpu`, `memory`, and
`pods`. There are no `small` / `medium` / `large` tiers.

We rejected tiers because a tier list pushes the sizing policy into the platform.
Every team whose need does not fit a tier has to wait for the platform team to
edit the tier list. That is the ticket-queue bottleneck a self-service platform
is supposed to remove. Free-form numbers are a legitimate "what" that only the
team knows, so exposing them does not leak the "how": the `ResourceQuota` object
is still hidden.

The cost of free-form is that, on its own, it is not governance. A team can ask
for any amount. The platform-enforced ceiling (reject asks over a limit) is a
separate concern and lands in **M4 as a validating webhook**. M2 deliberately
only turns the declaration into a quota. We are not pretending free-form is the
final story.

### 2. The quota mapping is opinionated and hidden

The team gives one number per resource. The controller maps each to specific
`ResourceQuota` keys, and the mapping encodes a scheduling opinion:

| Team declares | Quota keys set | Why |
|---|---|---|
| `cpu` | `requests.cpu` only | CPU is compressible. Setting only the request lets workloads burst above it. If the quota set `limits.cpu`, every pod would be forced to declare a CPU limit and could not burst. |
| `memory` | `requests.memory` and `limits.memory` (same value) | Memory is incompressible and cannot be reclaimed. Request and limit should match so a node-packing scheduler (for example Karpenter) plans on a fixed figure. |
| `pods` | `pods` | A plain cap on pod count. |

A field the team leaves unset adds no quota key. If the whole block is omitted,
no `ResourceQuota` is created and the namespace is uncapped, which the
`QuotaApplied` condition states plainly (`reason: NoResourcesDeclared`).

### 3. Bind a group to the built-in `edit` ClusterRole

The `RoleBinding` subject is a **group** named `team-<team>`, not a
ServiceAccount. The subject is human team members. In production that group maps
to an OIDC group; on local k3d it is notional. ServiceAccounts are for in-cluster
workloads authenticating to the API, which is a different need.

The role is the built-in `edit` ClusterRole, bound per namespace via a
`RoleBinding`. `edit` is enough to get the self-service loop working and it is
well understood. A custom idp role (to show RBAC design depth, or to remove
permissions `edit` grants that we do not want) can replace it later without
changing the reconcile loop: only the `RoleRef.Name` changes.

One consequence of using a built-in role: Kubernetes prevents privilege
escalation, so the controller may only create a `RoleBinding` to `edit` if it is
itself granted the `bind` verb on that ClusterRole. The controller's generated
ClusterRole carries exactly that grant (`bind` on `clusterroles`,
`resourceNames: [edit]`), nothing broader.

## Consequences

- `ServiceClaim` stays small: three optional numbers and a team name. No tier
  enum to maintain, no per-tier policy to keep in sync.
- The CPU-burst / memory-pinned mapping is a defensible, production-shaped
  decision with a clear rationale, not an arbitrary default.
- The namespace is uncapped until M4 adds the webhook. This is a known gap, not
  an oversight; it is called out in the verification runbook.
- Binding a group (not a ServiceAccount) is the correct model for human access
  and keeps the door open for OIDC group mapping in a real cluster.
- Swapping `edit` for a custom role later is a one-line change plus a new role
  definition. The escalation guard (`bind` on `edit`) would change with it.

## Alternatives considered

- **Fixed tiers (small/medium/large).** Simpler to validate and to reason about
  capacity, but reintroduces the platform team as a bottleneck for any team that
  does not fit a tier. Rejected for the reason in Decision 1.
- **Mirror the raw `ResourceQuota.hard` map in the spec.** Maximum flexibility
  and the least controller code, but it exposes `requests.cpu` /
  `limits.memory` mechanics directly to teams. That breaks the ADR-007
  abstraction. Rejected.
- **Bind a ServiceAccount instead of a group.** Wrong subject for human access;
  a ServiceAccount is for workloads. Rejected.
- **Write a custom RBAC role now.** Defers no real risk and adds scope to M2 for
  little gain yet. Deferred; `edit` is fine to start.
