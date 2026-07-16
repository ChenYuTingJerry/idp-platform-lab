# ADR 000: Use Argo Ecosystem (ArgoCD + Argo Workflows + Argo Events)

- **Status:** Superseded by ADR-007
- **Date:** 2026-04-30
- **Implemented:** no (superseded by ADR-007; Argo Workflows and Argo Events were never installed)
- **Deciders:** Yu Ting (Platform Engineer)
- **Supersedes:** N/A (root decision)
- **Superseded by:** ADR-007 (idp-platform-lab is an extensible self-service IDP, not an Argo ecosystem demo)

## Context

idp-platform-lab is a spec-driven Internal Developer Platform. It needs to perform
three distinct categories of work:

1. **Continuous deployment** — sync declarative state from Git to Kubernetes
2. **Platform automation** — execute multi-step pipelines (validate spec,
   render manifests, build images, register applications, set up RBAC)
3. **Event-driven coordination** — react to Git changes, K8s health signals,
   external webhooks, and trigger appropriate workflows

These three concerns are different in nature and benefit from being handled
by different tools with different abstractions:
- (1) is a continuous reconciler comparing desired vs actual state
- (2) is a finite, parameterized DAG with inputs and outputs
- (3) is a pub/sub mechanism with filters and triggers

## Decision

Use the **Argo ecosystem** as the foundation:
- **ArgoCD** for GitOps continuous deployment
- **Argo Workflows** for platform automation pipelines (Spec Processor,
  rollback workflows, image builds)
- **Argo Events** for event-driven coordination (spec changes, health
  monitoring, webhook triggers)

## Alternatives Considered

### Flux + Tekton + (custom event glue)

The most direct alternative. Flux is the other major CNCF GitOps tool;
Tekton is the de facto pipeline standard; event handling would need to
be assembled from Flux notification controller, Kubernetes watches,
or a custom controller.

| Aspect | Argo stack | Flux + Tekton |
|---|---|---|
| GitOps maturity | Mature, large UI ecosystem | Mature, more modular/CLI-centric |
| Pipeline DAG model | Native to Workflows | Tekton has DAG, more verbose |
| Event handling | First-class via Argo Events | Notification controller + glue |
| Single-vendor cohesion | All three projects share Argo CRDs and conventions | Three separate CNCF projects with different idioms |
| My hands-on experience | Have run ArgoCD and Argo Workflows in production | Limited |

**Why not chosen:** the three Argo projects share design language
(`argoproj.io` CRDs, similar conventions, shared community), so the
integration story is genuinely cohesive — not just three tools that
happen to be in the same diagram. Flux + Tekton + event-glue would
require me to design the cross-tool contracts myself, which adds
implementation cost for no real gain.

### Backstage as the IDP shell

Backstage is the dominant IDP framework. It provides scaffolding,
service catalog, and a developer portal UI.

**Why not chosen:** Backstage is a portal layer, not a control plane.
It would sit *on top* of something like idp-platform-lab, not replace it.
Backstage also implies a heavier upfront investment (TypeScript plugin
development, plus the catalog data model) that doesn't fit a 1–2 week
MVP. The portal can be added later as an interface to the same
ServiceClaim contract.

### Crossplane

Crossplane offers a CRD-driven platform abstraction with composition,
which arguably overlaps with my "spec-driven" goal more directly than
Argo Workflows.

**Why not chosen:** Crossplane shines for cloud resource provisioning
(databases, queues, buckets) where its provider model adds real value.
For a Kubernetes-native MVP focused on application deployment and
multi-team isolation, Crossplane introduces a steeper learning curve
than the demo justifies. The Spec Processor workflow approach is
intentionally simpler and more readable. **Crossplane is on the
"could evolve to" list, not the "should start with" list.**

### Jenkins X / GitLab CI / GitHub Actions

Traditional CI/CD systems with GitOps add-ons.

**Why not chosen:** these are CI-first tools with deployment bolted on.
idp-platform-lab's design starts from the Kubernetes-native side. Pipelines
running outside the cluster also miss the in-cluster reconcile loop
that's central to the spec-driven story.

## Consequences

### Positive

- **Cohesive design language.** All three components use `argoproj.io`
  CRDs. A `Workflow` CR can be triggered by an `EventSource`/`Sensor`
  pair and deployed by an ArgoCD `Application` — no impedance mismatch.

- **Production-grade individually.** ArgoCD powers GitOps at companies
  like Intuit, Adobe, Red Hat. Argo Workflows runs ML pipelines at NVIDIA
  and BlackRock. Argo Events is used for event-driven CI at scale. The
  stack is not experimental.

- **Clear separation of concerns**:
  "I picked the right tool for each kind of work — continuous reconciliation,
  finite pipelines, pub/sub. Conflating them into one tool would force
  the wrong abstraction somewhere."

- **My existing experience** with ArgoCD and Argo Workflows in production
  reduces implementation risk. Argo Events is the only new piece, which
  is a manageable scope.

- **Healthy ecosystem.** All three projects are CNCF-graduated/incubating
  with active maintainer communities and predictable release cadences.

### Negative

- **Operational footprint** — three controllers to install, monitor,
  and upgrade. Mitigated by Helm/manifest-based installs and pinned
  versions. For larger teams this would warrant an Argo upgrade SOP.

- **Argo Workflows v4 breaking changes** — v4.0 introduced schema
  migrations (singular → plural sync primitives, removed Python SDK).
  We pin to v4.0.5 and use the new schema from day one to avoid future
  migration debt. Documented in ADR-XXX (Argo Workflows v4 migration).

- **Lock-in to argoproj CRDs.** Migrating off this stack would require
  rewriting workflow templates, ArgoCD Applications, and EventSource/
  Sensor definitions. The ServiceClaim abstraction layer mitigates this:
  team-facing contracts don't reference Argo CRDs, so the platform
  internals could be swapped without changing team workflows.
