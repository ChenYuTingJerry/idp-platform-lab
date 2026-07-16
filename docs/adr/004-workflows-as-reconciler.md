# ADR 004: Argo Workflows as Spec Reconciler (not Custom CRD + Operator)

- **Status:** Superseded by ADR-005
- **Date:** 2026-04-30
- **Implemented:** no (superseded by ADR-005; intentionally never built)
- **Deciders:** Yu Ting
- **Related:** ADR-000 (Argo ecosystem)

## Context

idp-platform-lab's core mechanism is reconciling team-submitted ServiceSpecs
into running Kubernetes resources (namespace, RBAC, ResourceQuota,
ArgoCD Application, Kustomize overlays).

The Kubernetes-native pattern for this is a **custom CRD + Operator**:
define a `ServiceSpec` CRD, write a controller in Go using
controller-runtime, deploy it as a Deployment, and let it watch CRDs
and reconcile.

This is the "right answer" in many production contexts. idp-platform-lab
intentionally chooses a different path.

## Decision

Use **Argo Workflows as the reconciler**:
- ServiceSpec is plain YAML in a Git repo (no CRD)
- Argo Events watches the Git repo for changes
- Argo Workflow templates ("Spec Processor") read the spec, validate,
  render manifests, and apply them
- ArgoCD syncs the resulting manifests to the cluster

Explicitly **not** writing a custom CRD + Operator.

## Alternatives Considered

### Custom CRD + Go Operator (controller-runtime / kubebuilder)

The Kubernetes-native answer. Define `kind: ServiceSpec` as a CRD,
write a Go reconciler that watches it, owns child resources, handles
finalizers, etc.

**Why not chosen for MVP:**
- **Time cost.** Scaffolding a kubebuilder project, writing the
  reconciler logic, building/pushing a controller image, RBAC for
  it, lifecycle management — 3-4 days minimum, leaves no room for
  Argo Events integration, auto-rollback, blog, ADRs in a 2-week budget.
- **Readability.** Argo Workflow YAML is readable by anyone on the
  team. A Go reconciler requires Go literacy to debug.
- **Iteration speed.** Changing a workflow is editing YAML; changing
  a reconciler is rebuild + push + restart pod.
- **Demo legibility.** "Look at this Workflow that takes a spec and
  produces these resources" is a one-screen demo. "Look at this Go
  reconciler" is a deep dive.

This is the **explicit trade-off** of the project: choosing the right
tool for an MVP timeframe rather than the conventionally "correct" tool.
The decision is reversible — see Consequences.

### Crossplane + Composition

Crossplane's `Composition` and `XRD` (Composite Resource Definition)
provide a CRD-driven abstraction layer specifically designed for this
pattern.

**Why not chosen:**
- Steeper learning curve than Argo Workflows for someone already
  experienced with Workflows (per ADR-000 context).
- Crossplane's strongest fit is cloud resource provisioning (databases,
  buckets, queues). For Kubernetes-native workloads, the Composition
  model is more abstract than necessary.
- Listed as "could evolve to" — if idp-platform-lab grew into a real platform
  needing cloud resource provisioning, Crossplane becomes attractive.

### Helm umbrella chart per team

Treat each team as a Helm release with values supplied by the team.

**Why not chosen:**
- Inverts the SDD model — teams become Helm-aware. ServiceSpec abstracts
  Helm precisely so teams don't need to learn it.
- No reconcile loop on spec changes; relies on `helm upgrade` being run.

### Configuration management tool (Ansible, Pulumi)

Imperative scripts triggered on spec changes.

**Why not chosen:** loses the Kubernetes-native feedback loop. ArgoCD
can't observe drift on resources Ansible created out of band.

## Consequences

### Positive

- **MVP-feasible.** Workflow templates are YAML. The Spec Processor can
  ship in days, not weeks.

- **Inspectable.** Anyone can read the workflow DAG and understand the
  reconcile logic. No Go knowledge required.

- **Argo Events integration is native.** Events can directly trigger
  Workflows — no extra glue between event source and reconciler.

- **Reuses existing Argo expertise.** Per ADR-000, Argo Workflows is
  a known quantity. No new control plane to learn.

- **Composable.** Sub-workflows for "create namespace", "render
  overlays", "register ArgoCD App" can be reused in different
  reconcile flows (initial creation, update, rollback, teardown).

### Negative

- **Not a true continuous reconciler.** A Go Operator runs continuously
  and reconciles drift. Argo Workflows runs on trigger (event-driven),
  meaning if someone hand-edits a generated resource, the Workflow
  doesn't notice. **Mitigation: ArgoCD itself acts as the drift detector**
  — the generated `Application` has `selfHeal: true`, so ArgoCD reverts
  drift on the actual workload resources. The Spec Processor only needs
  to run when the spec changes.

- **No `status` subresource.** A real Operator would write back
  reconcile status onto the CR. With ServiceSpec-as-YAML, status lives
  in Workflow logs and ArgoCD Application status. Acceptable for the
  MVP; a future Operator migration would add `kind: ServiceSpec` with
  a proper status field.

- **Spec validation is workflow-internal.** A CRD with OpenAPI schema
  rejects malformed specs at `kubectl apply` time. Our setup validates
  inside the Workflow — errors surface later. **Mitigation:** JSON
  Schema validation as the first step of the Spec Processor; Git
  pre-commit hooks could also lint specs. (Future work.)

- **Reversible, not free.** Migrating to a real CRD + Operator later
  requires defining the CRD, writing the controller, and porting the
  Workflow logic. Ports cleanly because the Workflow steps are already
  modular reconcile actions — they map almost 1:1 to Operator reconcile
  loop steps.
