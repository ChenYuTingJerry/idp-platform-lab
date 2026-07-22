# Verification runbook

How to build and verify each milestone, end to end. This is a living document:
each milestone has a **Build** section (commands to bring that state up) and a
**Verify** section (checks that prove it works). Milestones not yet implemented
are marked clearly and carry acceptance criteria only, no commands yet.

Conventions:

- All commands assume the `idp` cluster and the right kube context:
  `kubectl config use-context k3d-idp`.
- Images use the embedded registry and a pinned tag (never `:latest`):
  `k3d-idp-registry:5050/idp-controller:<version>`.
- Re-running a Build step should be safe (idempotent) unless noted.

Status at a glance:

| Milestone | State |
|-----------|-------|
| M0 — Local environment scaffold | Done |
| M1 — Controller scaffold + minimal reconciler | Done |
| M2 — RBAC, Quota, idempotency | Done |
| M3 — ArgoCD Application integration | Done (verified on k3d); Tenant-lifecycle e2e runs in CI |
| M4 — Finalizers and status polish | Done (two finalizers, ADR-012; validating webhook, ADR-013) |

---

## M0 — Local environment scaffold (Done)

### Build

```sh
task up        # create k3d cluster + registry, install ArgoCD, wait for ready
task status    # show all pods across all namespaces
```

`task up` is idempotent. Cluster creation is skipped if the cluster exists;
ArgoCD is re-applied with server-side apply.

### Verify

```sh
kubectl get nodes                          # 1 node, Ready
kubectl get pods -n argocd                 # all Running
kubectl get pods -n kube-system | grep traefik
kubectl get crd | grep argoproj            # 3 argoproj CRDs
docker ps | grep registry                  # registry on :5050
```

Registry round-trip (the embedded registry actually accepts pushes):

```sh
docker pull nginx:alpine
docker tag nginx:alpine k3d-idp-registry:5050/test:v1
docker push k3d-idp-registry:5050/test:v1
```

A successful `docker push` (it prints a `digest:` line) is already proof the
write path works. To confirm the registry actually stored the image, query its
HTTP API:

```sh
curl http://k3d-idp-registry:5050/v2/_catalog        # {"repositories":["test"]}
curl http://k3d-idp-registry:5050/v2/test/tags/list  # {"name":"test","tags":["v1"]}
```

Clean up the throwaway test image afterwards:

```sh
docker rmi k3d-idp-registry:5050/test:v1 nginx:alpine
```

Idempotency round-trip (second `up` is a no-op for the cluster):

```sh
task down && task up && task up
```

---

## M1 — Controller scaffold + minimal reconciler (Done)

The controller reconciles a `ServiceClaim` into a namespace `team-<team>`,
owned by the claim, and writes `Ready` status back.

### Build

```sh
task deploy     # build, push, and deploy the image (default tag 0.1.0-m1)
```

`task deploy` wraps three make calls: `docker-build`, `docker-push`, and
`deploy`. The `make deploy` step builds `config/default`, so it applies the CRD,
RBAC, and the `idp-controller-manager` Deployment into the `idp-system`
namespace in one step. Override the image tag with
`task deploy IMG=k3d-idp-registry:5050/idp-controller:<version>`.

Wait for the controller to be ready:

```sh
kubectl -n idp-system rollout status deploy/idp-controller-manager
```

### Verify

Apply the Tenant first, wait for it to be Ready, then apply the claim and
confirm the reconcile result. After the ADR-010 split the Tenant creates the
namespace, and a ServiceClaim stays `TenantReady=False` until its Tenant is
Ready:

```sh
kubectl apply -f config/samples/platform_v1alpha1_tenant.yaml
kubectl wait --for=jsonpath='{.status.phase}'=Ready tenant/payments --timeout=30s
kubectl apply -f config/samples/platform_v1alpha1_serviceclaim.yaml
kubectl wait --for=jsonpath='{.status.phase}'=Ready serviceclaim/payments --timeout=30s

kubectl get ns team-payments                                                     # Active
kubectl get ns team-payments -o jsonpath='{.metadata.ownerReferences[0].kind}'   # Tenant
kubectl get serviceclaim payments -o jsonpath='{.status.phase}'                  # Ready
```

Prove it is the in-cluster controller doing the work (delete the claim and the
Tenant, then a fresh recreate). The namespace is owned by the Tenant, so
deleting the Tenant is what removes it:

```sh
kubectl delete -f config/samples/platform_v1alpha1_serviceclaim.yaml
kubectl delete -f config/samples/platform_v1alpha1_tenant.yaml
kubectl wait --for=delete ns/team-payments --timeout=60s    # teardown removes the namespace
kubectl apply -f config/samples/platform_v1alpha1_tenant.yaml
kubectl -n idp-system logs deploy/idp-controller-manager | grep "reconciled team namespace"
```

Acceptance:

- [x] Namespace `team-payments` exists, owned by the Tenant (`controller=true`).
- [x] Claim status is `Ready` with `observedGeneration=1`.
- [x] Deleting the Tenant removes the namespace.
- [x] The running pod (not host `task run`) recreates the namespace on re-apply.

Known limitation (tracked for M2): a single status-update conflict can appear in
the logs on first reconcile (optimistic-locking race between the claim event and
the namespace `Owns` event). It self-heals via requeue. See
`docs/journal/2026-06-01-m1-controller-receipts.md`.

---

## M2 — RBAC, Quota, idempotency (Done)

The reconciler turns one `Tenant` into three owned objects: the team
`Namespace`, a `RoleBinding` (team group -> built-in `edit` ClusterRole), and a
`ResourceQuota` built from the Tenant's `spec.resources`. It writes four status
conditions (`NamespaceReady`, `RBACReady`, `QuotaApplied`, and the aggregate
`Ready`). The M1 status-update conflict is fixed by writing status with a merge
patch instead of an update. See ADR-008 for the quota/RBAC design. After the
ADR-010 split `spec.resources` lives on the `Tenant`, not on the `ServiceClaim`.

The quota mapping is opinionated and hidden from the team:

- `spec.resources.cpu` (on the Tenant) -> `requests.cpu` only. No `limits.cpu`,
  so workloads can burst above their request.
- `spec.resources.memory` -> both `requests.memory` and `limits.memory` (same
  value). Memory is incompressible, so request and limit should match.
- `spec.resources.pods` -> `pods`.

### Build

```sh
task deploy IMG=k3d-idp-registry:5050/idp-controller:0.2.0-m2
kubectl -n idp-system rollout status deploy/idp-controller-manager
```

Leader election takes ~30s on a fresh pod, so wait for a reconcile before
checking results (the `kubectl wait` below handles this).

### Verify

The namespace, RoleBinding and ResourceQuota come from the Tenant, so this
block applies the Tenant:

```sh
kubectl apply -f config/samples/platform_v1alpha1_tenant.yaml
kubectl wait --for=jsonpath='{.status.phase}'=Ready tenant/payments --timeout=60s

# Four conditions on the Tenant, all True:
kubectl get tenant payments \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status} ({.reason}){"\n"}{end}'

# RoleBinding: team group bound to the edit ClusterRole, owned by the Tenant:
kubectl -n team-payments get rolebinding team-edit \
  -o jsonpath='{.roleRef.kind}/{.roleRef.name} {.subjects[0].kind}/{.subjects[0].name}{"\n"}'

# ResourceQuota: requests.cpu, requests/limits.memory, pods -- and NO limits.cpu:
kubectl -n team-payments get resourcequota team-quota -o jsonpath='{.spec.hard}{"\n"}'
```

Expected quota: `{"limits.memory":"4Gi","pods":"10","requests.cpu":"2","requests.memory":"4Gi"}`.

Idempotency (re-reconcile produces no diff). Capture the owned objects'
`resourceVersion`, force a reconcile, and confirm they did not change:

```sh
RB=$(kubectl -n team-payments get rolebinding team-edit -o jsonpath='{.metadata.resourceVersion}')
RQ=$(kubectl -n team-payments get resourcequota team-quota -o jsonpath='{.metadata.resourceVersion}')
kubectl annotate tenant payments idp.io/touch="$(date +%s)" --overwrite
sleep 3
[ "$RB" = "$(kubectl -n team-payments get rolebinding team-edit -o jsonpath='{.metadata.resourceVersion}')" ] && echo "RoleBinding unchanged"
[ "$RQ" = "$(kubectl -n team-payments get resourcequota team-quota -o jsonpath='{.metadata.resourceVersion}')" ] && echo "Quota unchanged"
kubectl annotate tenant payments idp.io/touch-      # clean up
```

The controller stays silent on no-op reconciles by design (it only logs when
`CreateOrUpdate` actually writes), so idempotency is checked from the outside via
`resourceVersion`, not the logs.

Conflict fix: the controller logs should be free of optimistic-lock errors:

```sh
kubectl -n idp-system logs deploy/idp-controller-manager \
  | grep -iE 'conflict|has been modified|reconciler error' || echo "clean"
```

Integration tests (envtest, no cluster needed):

```sh
task test        # 5 specs: create, idempotency, update, no-resources, not-found
```

Acceptance:

- [x] Reconciler creates a `RoleBinding` (team group `team-<team>` -> `edit`).
- [x] Reconciler creates a `ResourceQuota` from `spec.resources`.
- [x] Re-reconcile produces no diff (owned objects' `resourceVersion` unchanged).
- [x] Owner references propagate (`controller=true` on all three owned objects).
- [x] Status conditions `NamespaceReady`, `RBACReady`, `QuotaApplied` plus
      aggregate `Ready` are set.
- [x] envtest covers create, update, no-resources, idempotency, and not-found.

Platform ceiling: M2 turns the declaration into a quota. The platform-enforced
ceiling that rejects oversized asks is now built as a validating webhook in M4
(ADR-013), so a team can no longer ask for any quota.

---

## M3 — ArgoCD Application integration

The reconciler now adds a fourth step: one ArgoCD `Application` per claim,
pointing ArgoCD at `workloads/<team>/<svc>/` in this repo, where `<svc>`
is the claim name. The team's `image` and `replicas` ride along as Kustomize
overrides. See ADR-009 for the design (workloads moved into this repo per
ADR-011).

### Integration tests (envtest, no cluster needed)

```sh
task test
```

This runs the M2 specs plus four new Application specs: the Application is created
with the right source, destination, project, and sync policy; the `image` and
`replicas` overrides are present; a re-reconcile is a no-op (idempotent); and
ArgoCD's live health/sync shows up in the `ArgoAppCreated` condition message.

### Prerequisite for the live demo: the in-repo workload path

The Application syncs from this repo. The `--workloads-repo-url` flag is required
and has no default; it points at `https://github.com/ChenYuTingJerry/idp-platform-lab`
(set in `config/manager/manager.yaml`). The workloads live under
`workloads/<team>/<svc>/` in this repo (ADR-011). One example service follows the
naming contract from ADR-009. The sample claim is `payments` (team `payments`), so
the path is `workloads/payments/payments`:

```
workloads/
  payments/
    payments/
      kustomization.yaml   # resources: [deployment.yaml, service.yaml]
      deployment.yaml      # metadata.name: payments; container image: app
      service.yaml         # selects the payments Deployment
```

The base `Deployment` must be named `payments` (the claim name) and use the
literal image `app`, so the controller's `kustomize.images` / `kustomize.replicas`
overrides match. Do **not** pin `images:`/`replicas:` in the base
`kustomization.yaml`; those come from the Application.

### Live end-to-end (k3d + ArgoCD)

```sh
task up                 # cluster + ArgoCD
make docker-build docker-push deploy

# Apply the Tenant first; the claim stays TenantReady=False until it is Ready:
kubectl apply -f config/samples/platform_v1alpha1_tenant.yaml
kubectl wait --for=jsonpath='{.status.phase}'=Ready tenant/payments --timeout=60s
kubectl apply -f config/samples/platform_v1alpha1_serviceclaim.yaml

# The claim reaches Ready once the Application is applied:
kubectl wait --for=jsonpath='{.status.phase}'=Ready serviceclaim/payments --timeout=60s

# Five conditions now, including ArgoAppCreated:
kubectl get serviceclaim payments \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status} ({.message}){"\n"}{end}'

# The Application exists in argocd, owned by the claim, and syncs the workload:
kubectl -n argocd get application payments \
  -o jsonpath='{.spec.source.path} -> {.spec.destination.namespace} | health={.status.health.status} sync={.status.sync.status}{"\n"}'

# The workload pods land in the team namespace:
kubectl -n team-payments get deploy,pods
```

Expect the Application `path` to be `workloads/payments/payments`, destination
`team-payments`, and ArgoCD to report `Healthy`/`Synced` once it has pulled the
manifests. The `ArgoAppCreated` condition message tracks that health.

Acceptance criteria (from ROADMAP):

- [x] Reconciler creates an ArgoCD `Application` pointing at the workload path.
- [x] Workload manifests live under `workloads/<team>/<svc>/` in this repo (ADR-011).
- [x] ArgoCD `selfHeal: true` so workload drift is handled by ArgoCD.
- [x] Status condition `ArgoAppCreated` reflects the Application health (created
      gates the condition; live health/sync ride in the message).
- [x] End-to-end: apply a `ServiceClaim`, watch namespace + workload appear.
      Verified on k3d (k3s v1.31.14): the `payments` claim reached `Ready` with all
      five conditions True, ArgoCD reported the `payments` Application `Synced` /
      `Healthy`, and two nginx pods came up in `team-payments`. The
      `ArgoAppCreated` message carried `health=Healthy sync=Synced`, so the live
      read-back works against a real ArgoCD.

CI: the Tenant-lifecycle e2e now runs in CI. The full claim -> workload path
stays a manual step, documented above. Ordered teardown on delete (two
finalizers) is built in M4 (ADR-012); M3 itself relied on garbage collection,
which gives no ordering.

---

## M4 — Finalizers and status polish (Done)

The two finalizers are in `internal/controller/finalizers.go` (ADR-012) and the
validating webhook is in place (ADR-013). Finalizer behavior is covered by
`internal/controller/finalizer_test.go`; run it with `task test`.

Acceptance criteria (from ROADMAP):

- [x] Two finalizers give ordered teardown: the ServiceClaim removes its ArgoCD
      `Application` and waits for the prune; the Tenant blocks until its last
      ServiceClaim is gone, then deletes the namespace (ADR-012).
- [x] Webhook validation rejects malformed specs at `kubectl apply` time (ADR-013);
      name and non-negative rules also run as CEL rules on the CRD.
- [x] Status subresource has a full condition set with a `Ready` aggregate.
- [x] Printer columns: `kubectl get serviceclaim` shows team, image, phase, age.
