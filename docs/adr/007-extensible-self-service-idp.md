# ADR 007: idp-platform-lab is an Extensible Self-Service IDP, not an Argo Ecosystem Demo

- **Status:** Accepted
- **Date:** 2026-05-24
- **Implemented:** yes (identity and the separate-capability-CRD extension model; no second capability CRD exists yet, by design)
- **Deciders:** Yu Ting
- **Supersedes:** ADR-000 (Use Argo Ecosystem)
- **Related:** ADR-005 (Custom CRD + Go controller as reconciler)

## Context

ADR-000 framed idp-platform-lab as a project "built on the Argo ecosystem": ArgoCD for
CD, Argo Workflows for platform automation, Argo Events for event-driven
coordination. ADR-005 already pulled the reconciler out of Argo Workflows and
into a custom Go controller. After that change, the rest of the Argo framing was
reassessed and two things became clear.

**First, Argo Workflows and Argo Events are not load-bearing.** The work the
platform actually does maps to other tools:

- Reconciliation is the custom Go controller (ADR-005).
- Continuous delivery is ArgoCD.
- CI and image build are GitHub Actions or Azure DevOps, which run outside the
  cluster and predate any platform install.

That leaves Argo Workflows with one honest role: finite, DAG-shaped jobs such
as scheduled maintenance, batch migrations, or ML training. Those are useful,
but they are not part of the platform's core value. Argo Events only makes sense
as a bridge for external event sources, which the core reconcile loop does not
need (controller-runtime's informer handles in-cluster watches). Keeping either
one in the core design "for ecosystem completeness" would repeat the ADR-004
mistake: asserting value that has not been built or needed yet.

**Second, the project's identity is not "the Argo ecosystem".** The value of
idp-platform-lab is an extensible self-service abstraction. A team declares WHAT it wants
through a single `ServiceClaim` CR, and the platform decides HOW: namespace,
RBAC, quota, and an ArgoCD Application. ArgoCD is the CD engine behind that, an
implementation detail, not the identity. Naming the project after Argo would
tie it to a tool choice that should be swappable. The rename to idp-platform-lab (Spanish
for "path", as in the paved-road golden path) reflects this.

This reframing needs to be written down because it changes what the project
claims to be, and because it forces a decision the "buffet" framing has been
asserting without designing: how do new self-service capabilities get added
without bloating the API or the controller?

## Decision

**idp-platform-lab is a Kubernetes-native Internal Developer Platform whose value is an
extensible self-service abstraction.** Developers declare what they want in a
single `ServiceClaim` CR, and a custom Go controller reconciles it into an
isolated, governed environment (namespace, RBAC, quota) wired to ArgoCD for
delivery.

Concretely:

- **ArgoCD is the CD engine, an implementation detail.** Team-facing contracts
  never reference ArgoCD CRDs. The platform could swap the delivery engine
  without changing the `ServiceClaim` contract.
- **CI and image build live outside the platform** (GitHub Actions / Azure
  DevOps). They are not a platform concern.
- **Argo Workflows is optional and not core.** If a finite-job runner is ever
  needed (scheduled jobs, batch maintenance, ML training), Argo Workflows is a
  reasonable choice for that bounded role. It is not the reconciler and not part
  of the core design.
- **Argo Events is dropped.** The Kubernetes API server's watch mechanism, via
  controller-runtime's informer/cache, is the reconcile trigger. Argo Events
  would only return if an external event source (a webhook) ever needs bridging,
  which is not in the core design.

### Extension model: separate capability CRDs

New self-service capabilities are added as **separate capability CRDs**, each
with its own reconciler and OpenAPI schema. The base `ServiceClaim` owns the
environment (namespace, RBAC, quota, the ArgoCD Application). A future database
capability would be a `DatabaseClaim` CRD with its own fields (engine, version,
storage) and its own reconciler; a cache capability would be a `CacheClaim`, and
so on. Teams compose the CRs they need.

This keeps each capability's API and reconcile logic isolated. Adding a
capability does not grow the `ServiceClaim` schema or the `ServiceClaim`
reconciler. It is the model used by Crossplane, cert-manager, and most
production operators, so it carries a clear mental model.

A **provider/plugin layer is explicitly not built now.** It becomes relevant
only when a single capability grows a second interchangeable backend, for
example a `DatabaseClaim` that can be satisfied by an in-cluster CloudNativePG
cluster or by a cloud RDS instance. At that point the CRD stays the stable
contract and an internal Go provider interface selects the backend. Until a real
second backend exists, building that abstraction would assert value that has not
been earned, which is the trap ADR-004 fell into.

This decision supersedes ADR-000.

## Alternatives Considered

### Keep the Argo ecosystem framing (ADR-000)

Retain "built on the Argo ecosystem" with Argo Workflows and Argo Events as
first-class components.

**Why not kept:**

- It ties the project identity to a tool choice that should be an
  implementation detail. The value is the abstraction, not the vendor.
- It keeps two components (Workflows, Events) in the core design that do no
  core work. That is the same "asserting unbuilt value" pattern ADR-004 was
  reversed for.
- It describes a stack, not a design. "I built on the Argo ecosystem" names
  a stack; "I built an extensible self-service control plane and ArgoCD is one
  swappable engine inside it" describes a design.

### Single monolithic ServiceClaim with growing fields

Add every capability as new fields on the one `ServiceClaim` CR, with the one
reconciler mapping each field to child resources.

**Why not chosen:**

- The schema and the reconciler grow without bound. Every new capability touches
  the same type and the same reconcile function, which becomes a maintenance and
  testing burden.
- Capabilities with different lifecycles (a database outlives a redeploy) are
  awkward to model as fields on a single object.
- It is simpler to demo on day one, but the whole point of this ADR is that the
  extensibility has to be designed, not asserted.

### Provider/plugin model as the headline extension mechanism

Keep one `ServiceClaim` API and add capabilities as internal Go providers
registered into the controller, rather than as new CRDs.

**Why not chosen now:**

- There is no second backend for any capability yet, so the provider interface
  would be an abstraction with a single implementation. Building it now would
  assert flexibility that has not been needed, which is the ADR-004 trap.
- It hides capabilities behind code rather than the Kubernetes API. Separate
  CRDs make each capability visible to `kubectl`, RBAC, and schema validation
  for free.
- The provider pattern is still available later, scoped inside a specific CRD,
  exactly when a real second backend appears. That is the right time to build
  it.

## Consequences

### Positive

- **Clear identity.** The project is an extensible self-service IDP. The
  one-line thesis is clear and easy to state.
- **Modular API and reconcilers.** Each capability is isolated. A bug in a
  future `DatabaseClaim` reconciler cannot break `ServiceClaim` reconciliation.
- **Industry-standard mental model.** Separate capability CRDs is how
  Crossplane and cert-manager extend. It is easy to reason about.
- **Smaller, honest core.** The core design is controller + ArgoCD. No
  components that do no core work.

### Negative

- **More CRDs and reconcilers over time.** Each new capability is a new CRD plus
  a new reconciler, which is more code than adding a field. Accepted: the
  isolation is worth it, and capabilities are added deliberately, not in bulk.
- **CR relationships need design.** When `DatabaseClaim` and `ServiceClaim`
  coexist, their ownership and ordering (does one reference the other? who owns
  teardown?) must be designed. This is real work, deferred until the second CRD
  is actually added.
- **Less visible "full Argo ecosystem" surface.** The demo shows fewer Argo
  components. Mitigated: the demo shows a real reconcile loop and a clean
  abstraction, which is higher signal than a wide tool inventory.
