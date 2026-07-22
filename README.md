# idp-platform-lab

A Kubernetes-native Internal Developer Platform (IDP), built as a controller from
scratch. A team declares what it wants through two custom resources, and Go
controllers I wrote turn that into a governed, isolated environment.

This is a **lab**, one in a series (see also `otel-platform-lab`). Its point is
depth on the controller / reconcile-loop pattern, not a platform anyone should
run in production. For that, adopt Capsule plus Crossplane or kro; ADR-015 says
exactly where each tool would replace this code.

## The idea

A team declares intent, the platform decides how:

- A **`Tenant`** is the team's allocation. Its name is the team name. The Tenant
  controller creates a `team-<name>` namespace, a `RoleBinding` (the team group to
  the built-in `edit` role), and a `ResourceQuota` from `spec.resources`.
- A **`ServiceClaim`** is one service. The ServiceClaim controller waits for its
  Tenant to be Ready, then creates an ArgoCD `Application` that syncs the workload
  from `workloads/<team>/<svc>/` in this repo. ArgoCD does the sync and self-heal.

Teams never see ArgoCD, Kustomize, the repo URL, or RBAC details. That abstraction
is the platform's value (ADR-007).

```
Team applies Tenant + ServiceClaim(s)
  -> Tenant controller: Namespace + RoleBinding + ResourceQuota  (owned by the Tenant)
  -> ServiceClaim controller (once the Tenant is Ready): ArgoCD Application  (owned by the claim)
  -> ArgoCD syncs the workload manifests from workloads/<team>/<svc>/ into the namespace
  -> each controller writes status back onto its own CR
```

## What is built (honest status)

Every ADR carries an explicit `Implemented:` line. This table is the summary. An
ADR may design something not yet built; the repo never reads as if it is built.

| Capability | Status | ADR |
|---|---|---|
| Tenant: namespace, RBAC, ResourceQuota | done | ADR-008, ADR-010 |
| ServiceClaim: ArgoCD Application (unstructured, no argo-cd Go module) | done | ADR-009 |
| Tenant / ServiceClaim split (fixes the `AlreadyOwnedError` bug) | done | ADR-010 |
| `spec.team` field index for the Tenant-to-claims lookup | done | ADR-012 |
| Ordered teardown via two finalizers (deadlock-free, proven) | done | ADR-012 |
| Workloads in this repo, ArgoCD pulls over anonymous HTTPS | done | ADR-011 |
| CEL validation in the CRD (name to valid namespace, non-negative) | done | ADR-013 |
| Validating webhook: quota ceiling + terminating-Tenant, cert-manager signed | done | ADR-013 |
| Controller reads back the deployed workload (the silent kustomize no-op) | **deferred** | ADR-014 |
| `expose` / Ingress abstraction | **not built** | ADR-003 |

The build-vs-adopt reasoning, and where idp sits next to Capsule, Crossplane,
Kratix and kro, is ADR-015.

## Quick start

Prerequisites: Docker, k3d, kustomize, cert-manager-compatible cluster access,
Go 1.26, the `task` runner.

```bash
task up          # k3d cluster + registry + cert-manager + ArgoCD
task deploy       # build, push, and deploy the controller

# apply a team and a service (Tenant first, then the claim)
kubectl apply -f config/samples/platform_v1alpha1_tenant.yaml
kubectl wait --for=jsonpath='{.status.phase}'=Ready tenant/payments
kubectl apply -f config/samples/platform_v1alpha1_serviceclaim.yaml

kubectl get tenant,serviceclaim
kubectl get ns,rolebinding,resourcequota -n team-payments
```

The full "claim to running workload" path, including the ArgoCD sync, is walked
through in `docs/verification.md`.

## Development

```bash
make test         # envtest: controllers, webhook, and the platform ceiling
make run          # run the controller from the host (webhooks off; no local cert)
make lint
```

`--workloads-repo-url` is required and has no default: a default would make a fork
silently sync workloads from the original repo. `config/manager/manager.yaml` sets
it for the in-cluster deployment.

## Layout

```
api/v1alpha1/          Tenant and ServiceClaim types
internal/controller/    the two reconcilers, field index, finalizers
internal/webhook/       the validating webhook (quota ceiling, terminating check)
internal/platform/      the quota ceiling (platform config, from flags)
workloads/<team>/<svc>/ the workload base manifests ArgoCD syncs
config/                 CRDs, RBAC, webhook + cert-manager wiring, samples
docs/adr/               the decision record (read these for the reasoning)
docs/                   concepts.md, verification.md
```

## Design reasoning

The ADRs are the point of this repo, more than the code. Two chains are worth
reading in order: ADR-004 to ADR-005 (why the reconciler is hand-written, not an
Argo Workflow), and ADR-000 to ADR-007 (why the identity is an extensible IDP, not
an Argo ecosystem demo). ADR-015 places the whole thing against the off-the-shelf
tools.
