# Scenarios

What idp-platform-lab handles, what is deferred, and what it deliberately does
not do. The list is organized by user-facing scenario. Each row names the
scenario, the platform capability behind it, and where to read more.

This file is an index by scenario, not a second source of truth. The
authoritative status lives in the [README status table](../README.md) and the
ADRs; the step-by-step proof lives in [verification.md](verification.md). If a
row here ever disagrees with those, they win. For *how* the loop works read
[concepts.md](concepts.md); for *why* the design is this way read the ADRs.

## Covered (M0–M4)

| Scenario | What the platform does | Reference |
|---|---|---|
| A team gets an allocation | Applying a `Tenant` creates a `team-<name>` namespace, a `RoleBinding` (team group to the built-in `edit` role), and a `ResourceQuota` from `spec.resources`, all owned by the Tenant. | [ADR-008](adr/008-quota-and-rbac-model.md), [ADR-010](adr/010-split-tenant-and-serviceclaim.md) |
| A team deploys a service | A `ServiceClaim` waits for its Tenant to be `Ready`, then creates an ArgoCD `Application` that syncs the workload from `workloads/<team>/<svc>/`. The team never sees ArgoCD. | [ADR-009](adr/009-workload-sync-via-argocd-application.md), [ADR-011](adr/011-workloads-in-platform-repo.md) |
| A team runs many services | Many `ServiceClaim`s reference one `Tenant` by `spec.team`. The Tenant/ServiceClaim split is what makes one-to-many work; a field index on `spec.team` makes the Tenant-to-claims lookup cache-served. | [ADR-010](adr/010-split-tenant-and-serviceclaim.md), [ADR-012](adr/012-ordered-teardown-finalizers.md) |
| A claim is applied before its Tenant is ready | The claim is *pending*, not rejected: it reports `TenantReady=False` and re-reconciles when the Tenant flips `Ready`. Apply order inside a batch does not matter. | [ADR-010](adr/010-split-tenant-and-serviceclaim.md) §3 |
| A team removes one service | Deleting a `ServiceClaim` runs a finalizer that removes the ArgoCD `Application` and waits for ArgoCD to prune the workload before the finalizer clears. | [ADR-012](adr/012-ordered-teardown-finalizers.md) |
| A team removes its whole allocation | Deleting a `Tenant` blocks while any `ServiceClaim` still references it (so `kubectl delete tenant` cannot silently destroy running services), then deletes the namespace, RBAC and quota. Proven deadlock-free. | [ADR-012](adr/012-ordered-teardown-finalizers.md) |
| A team asks for too much quota | A cert-manager-signed validating webhook rejects a `Tenant` over the platform ceiling at apply time. CEL rules on the CRD reject an invalid name or negative values. | [ADR-013](adr/013-validation-two-layers.md) |
| A team edits a resource mid-incident | `Tenant` and `ServiceClaim` live in etcd, not Git, so `kubectl edit` works. The reconcile loop is level-triggered and every apply is idempotent. | [ADR-005](adr/005-controller-as-reconciler.md), [concepts.md](concepts.md) |
| Someone hand-edits a generated resource | The controller `Owns` the namespace, RoleBinding and quota, so drift on any of them re-triggers a reconcile. Workload drift is corrected by ArgoCD `selfHeal`, not the controller. | [ADR-008](adr/008-quota-and-rbac-model.md), verification.md (M2) |

## Deferred (designed, recorded as deferred)

These are written down, not built. The discipline is that a deferred thing is
never described as done.

| Scenario | Why deferred | Reference |
|---|---|---|
| The controller confirms the workload actually deployed | idp renders the ArgoCD Application with kustomize overrides against a separate base, which can silently no-op. Read-back verification is designed with two real fixes on record, but not built. | [ADR-014](adr/014-defer-workload-verification.md) |
| A Tenant that was already over the ceiling gets a report-only condition | The webhook rejects new over-ceiling asks, but a Tenant that predates the ceiling gets no report-only status yet. | [ADR-013](adr/013-validation-two-layers.md) |
| A team declares an external route (`expose` / Ingress) | The abstraction is designed but not built, so teams cannot yet declare ingress. | [ADR-003](adr/003-traefik-ingress.md) |

## Out of scope (adopt instead)

These are deliberate non-goals. For a production IDP the right move is to adopt
a mature tool, not to extend this lab (see [ADR-015](adr/015-build-vs-adopt.md)).

| Scenario | Adopt instead / why | Reference |
|---|---|---|
| Observability of the reconcile loop (OpenTelemetry spans) | Dropped on purpose. idp and `otel-platform-lab` are separate labs; controller-runtime's built-in reconcile metrics already cover the aggregate view. | [ROADMAP](../ROADMAP.md) "Out of scope" |
| Provisioning cloud resources (databases, buckets, queues) | Use **Crossplane**. That is its home ground; idp manages in-cluster resources only. | [ADR-000](adr/000-use-argo-ecosystem.md), [ADR-015](adr/015-build-vs-adopt.md) |
| Full namespace-as-a-service (multi-namespace, LimitRange, NetworkPolicy, naming admission) | Use **Capsule**. idp's `Tenant` is roughly a subset of Capsule's. | [ADR-015](adr/015-build-vs-adopt.md) |
| Multi-cluster, SSO, HPA, NetworkPolicies, real CI/CD, TLS at the ArgoCD server | Out of MVP scope for this lab. | [ROADMAP](../ROADMAP.md) "Out of scope" |
| A workflow engine, a developer portal, or external cloud composition | idp is a control plane (CRD + controller), not Argo Workflows, not Backstage, not Crossplane. They could coexist. | [concepts.md](concepts.md) "What this is not" |
