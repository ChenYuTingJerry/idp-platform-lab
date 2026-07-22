# Roadmap

This lab is built in milestones. Each milestone is a coherent slice with its own
verification. The status here matches the code; the per-capability detail is in the
README table and the ADRs.

## Done

**M0 — Local environment.** k3d cluster, embedded registry on port 5050,
cert-manager, ArgoCD, the `task` runner, and the first ADRs. (ADR-001, ADR-002,
ADR-003)

**M1 — Controller scaffold.** kubebuilder project, the first reconciler, status
write-back, deploy to the cluster. (ADR-005, ADR-006)

**M2 — RBAC, quota, idempotency.** The Tenant creates a RoleBinding and a
ResourceQuota; every apply is idempotent, proven by envtest asserting the object
`resourceVersion` does not move on a second reconcile. (ADR-008)

**M3 — ArgoCD integration.** The ServiceClaim renders an ArgoCD `Application`
(unstructured, no argo-cd Go module) that syncs the workload. (ADR-009)

**M4 — Lifecycle and governance.** The Tenant / ServiceClaim split (ADR-010),
ordered teardown via two finalizers, proven deadlock-free (ADR-012), CEL validation
in the CRD plus a cert-manager-signed validating webhook for the quota ceiling
(ADR-013), and the workloads moved into this repo (ADR-011).

## Deferred, and recorded as deferred

These are designed and written down, not built. The discipline is that a deferred
thing is never described as done.

- **Read-back verification of the deployed workload** (the silent kustomize
  no-op). Deferred with two real fixes on record. (ADR-014)
- **A report-only condition for a pre-existing over-ceiling Tenant.** (ADR-013)
- **The `expose` / Ingress abstraction.** Designed in ADR-003, not built.

## Feature-frozen at M4

This lab is complete at M4. Its point was depth on the controller /
reconcile-loop pattern, and M0–M4 deliver that end to end: team provisioning,
RBAC, quota, ArgoCD sync, ordered teardown, and two-layer validation. There is
no M5.

## Out of scope

Real CI/CD, multi-cluster, SSO, HPA, NetworkPolicies, TLS at the ArgoCD server.
For a production IDP, adopt Capsule plus Crossplane or kro instead of extending
this lab (ADR-015).

Observability of the reconcile loop (OpenTelemetry spans) is out of scope too.
An earlier plan made it the seam that would join this lab to
`otel-platform-lab`. That seam is dropped on purpose. The two are separate
labs, each meant to stand on its own, and tying idp's "done" state to another
repo's maturity served a demo, not either lab's depth. controller-runtime's
built-in reconcile metrics already cover the aggregate view.
