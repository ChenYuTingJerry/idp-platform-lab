# ADR 015: Build vs adopt, and where idp sits next to Capsule, Crossplane, Kratix and kro

- **Status:** Accepted
- **Date:** 2026-07-16
- **Implemented:** n/a (this ADR records reasoning, not code)
- **Deciders:** Yu Ting
- **Related:** ADR-004 to ADR-005 (the reconciler build-vs-adopt reversal), ADR-007 (identity as an extensible IDP)

## Context

Almost everything idp does has an off-the-shelf tool that does the same thing,
often more maturely. That is not a weakness to hide. It is worth writing down
exactly which tool covers which piece, so the honest answer to "are you
reinventing the wheel?" is on record.

## The mapping

| idp piece | What productizes it | How close |
|---|---|---|
| `Tenant` to namespace + RBAC + ResourceQuota | **Capsule** (`Tenant` CR) | Very close. Capsule also does multi-namespace, LimitRange, NetworkPolicy, and admission for naming and quota. idp's Tenant is roughly a subset of Capsule's. |
| `ServiceClaim` to a set of concrete resources | **Crossplane** (XRD + Composition), **Kratix** (`Promise`), **kro** (`ResourceGraphDefinition`) | Close. "One abstract claim expands into concrete resources" is the entire reason these exist. |
| Ordered teardown via finalizers | Crossplane / kro handle finalizers, deletion order, and readiness for you | Built in. |
| Quota ceiling + name validation | Capsule's admission, or a Kyverno / Gatekeeper policy | Built in or one policy. |

So a working IDP could be assembled from Capsule plus one of Crossplane, Kratix
or kro with little or no Go code. **kro** is the closest single tool: you declare a
CRD and the graph of resources it expands into, with dependency order and
readiness, and write no controller.

## Decision

Build the reconciler by hand anyway, and keep this comparison as an asset rather
than an embarrassment.

The reason is the one ADR-005 already recorded: **this lab exists to build the
reconciler skill, not to run a platform.** Every tool in the table above is built
on the controller pattern idp builds from scratch: watches, owner references,
status conditions, idempotent apply, finalizers. Adopting one of them would mean
*configuring* someone else's reconcile loop when *authoring* one is the exact thing
worth learning. The value is not "no tool can do this". The value is being able to
explain the loop from the inside, and to say precisely where the boundary with each
off-the-shelf tool falls.

Two honest caveats belong here:

- **One piece is genuinely bespoke, and it half-patches a self-inflicted problem.**
  The read-back verification (ADR-014) exists because idp renders an ArgoCD
  Application with kustomize overrides against a separate base, which can silently
  no-op. A Crossplane-style composition that templates the Deployment directly
  would not have that failure mode at all. That is deferred for exactly this
  reason.
- **For production, adopt.** Capsule and Crossplane are CNCF projects with many
  maintainers and real hardening. idp is a lab. If this were a platform teams
  depended on, the right move would be Capsule plus Crossplane or kro, not this
  code.

## Consequences

- The project can answer build-vs-adopt from a position of having done both:
  hand-written the loop, and mapped where each tool would replace it.
- When a new capability is added, the first question is "does an existing tool own
  this better?" (ADR-007's extension model already points that way: a new
  capability is a new CRD, and that CRD might well be a Crossplane Composition
  rather than more hand-written Go).

## When adopting is clearly right

- Cloud resource provisioning (databases, buckets, queues): use Crossplane. That is
  its home ground, and ADR-000 already noted it as a "could evolve to", not a
  "start with".
- Namespace-as-a-service with mature multi-tenancy controls: use Capsule.
- A declarative "CRD to resource graph" with no custom logic: use kro.

The judgment is always the same one from ADR-005: is this the skill the system
exists to build? If yes, build it. If no, adopt it.
