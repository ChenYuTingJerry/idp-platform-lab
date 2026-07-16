# idp-platform-lab Concepts

A walkthrough of how idp-platform-lab is used, from a team's perspective and
from the platform engineer's perspective. Read this before reading
the ADRs; the ADRs explain *why* the design is this way, this file
explains *what* the design looks like in practice.

## The actors

There are three roles to keep in mind.

- **Platform engineer** (you, me). Operates the idp-platform-lab installation.
  Deploys the controller, installs the CRD, owns the codebase, decides
  what the platform exposes.
- **Developer team** (alpha team, beta team, etc). Writes services
  they want to deploy. Has no Kubernetes expertise required beyond
  `kubectl apply`. Should never need to know about ArgoCD, Helm,
  RBAC, ingress controllers, or any other implementation detail.
- **idp-platform-lab controller** (the Go code we're building). Two reconcilers
  in one manager: a `Tenant` reconciler that provisions each team's
  namespace, RBAC, and quota, and a `ServiceClaim` reconciler that turns
  each service claim into an ArgoCD `Application`. See ADR-010 for why
  the two are split.

ArgoCD also runs alongside, but it's the platform's tool, not the
team's. Teams never see ArgoCD.

## The team-facing surface

A team declares two kinds of thing, both small and both spec-driven. A
`Tenant`, once, says how much the team gets:

```yaml
apiVersion: platform.idp.io/v1alpha1
kind: Tenant
metadata:
  name: alpha            # the Tenant name is the team name
spec:
  resources:
    cpu: "2"
    memory: 4Gi
    pods: 10
```

Then a `ServiceClaim` per service says what to run:

```yaml
apiVersion: platform.idp.io/v1alpha1
kind: ServiceClaim
metadata:
  name: api-gateway
spec:
  team: alpha            # references the Tenant by name
  image: nginx:1.25
  replicas: 2
```

The team runs:

```sh
kubectl apply -f alpha-tenant.yaml -f api-gateway-claim.yaml
```

That is the whole interaction. No Helm chart, no ArgoCD UI, no
Kustomize overlays, no ingress configuration, no RBAC manifest, no
ResourceQuota tuning. Two small files: one for the team's allocation,
one per service.

This used to be a single `ServiceClaim`, but that object did two jobs
at once, team-level provisioning *and* one service, and it broke as
soon as a team wanted a second service (see ADR-010). Splitting the
allocation (`Tenant`) from the service (`ServiceClaim`) is what lets one
team run many services.

If the platform needs more from the team than these declarative files,
the abstraction has leaked. Pushing back on those requests is part of
the design.

## What happens when the team applies

The sequence below is what ADR-005 calls the "reconcile loop." It's
event-driven, not polling-based, and the trigger is the Kubernetes
API server's built-in watch mechanism. There are two reconcilers, one
per CRD.

1. **Team submits the Tenant and its ServiceClaims.** `kubectl apply`
   sends the YAML to the API server, which validates each object against
   its CRD's OpenAPI schema and writes the accepted ones to etcd. Apply
   order inside a batch is not guaranteed, and that is fine (see step 4).

2. **API server pushes watch events.** Each controller has an open watch
   on its CRD. As soon as the objects are in etcd, the reconcilers
   receive events.

3. **The Tenant reconciler provisions the team.** For a `Tenant` it makes
   sure these exist, all owned by the Tenant:
   - a `Namespace` named `team-<tenant>`
   - a `RoleBinding` that binds the team group (`team-<tenant>`) to the
     built-in `edit` role (a group, not a service account, see ADR-008)
   - a `ResourceQuota` derived from the Tenant's `spec.resources`

   It then writes per-step conditions and an aggregate `Ready`.

4. **The ServiceClaim reconciler waits for the Tenant, then wires up
   ArgoCD.** A claim whose Tenant does not exist yet, or is not `Ready`,
   is *pending*, not rejected: it reports `TenantReady=False` and waits,
   re-reconciling when the Tenant flips `Ready` (ADR-010 §3). Once the
   Tenant is ready, the reconciler creates one ArgoCD `Application`
   pointing at the team's Git path for the workload.

5. **ArgoCD takes over for workloads.** ArgoCD watches that
   `Application`, pulls Deployment and Service YAML from Git, and syncs
   them into the team's namespace. `selfHeal: true` means ArgoCD, not the
   controller, corrects workload drift.

6. **Controllers write status back.** Both reconcilers update
   `status.conditions` on their objects. The team can
   `kubectl get tenant alpha` and `kubectl get serviceclaim api-gateway`
   to see whether the namespace, RBAC, quota, and ArgoCD app are ready.

On delete, ordered teardown is implemented with two finalizers
(ADR-012). Deleting a ServiceClaim removes its ArgoCD `Application` and
waits for ArgoCD to prune the workload before the finalizer clears.
Deleting a Tenant blocks until its last ServiceClaim is gone, then
deletes the namespace, RBAC and quota. This drains the workload through
ArgoCD before the namespace goes away.

The team sees a service appear in their namespace, traffic flowing,
status visible on their Tenant and claim. They never touch ArgoCD or any
of the lower-level resources.

## What lives where

| Layer | Resource | Source of truth |
|---|---|---|
| Team's intent | `Tenant` + `ServiceClaim` CRs | etcd (via the API server) |
| Platform glue | Namespace, RoleBinding, ResourceQuota, ArgoCD `Application` | etcd (created by the controllers) |
| Workload manifests | Deployment, Service | Git (synced by ArgoCD) |

A common confusion: people assume "GitOps" means everything lives in
Git. In idp-platform-lab, only the workload manifests live in Git. The `Tenant`
and `ServiceClaim` live in etcd as normal Kubernetes resources. This
is intentional: they are the team's runtime declarations, not files
under version control. The team can update their Tenant or a claim
mid-incident with `kubectl edit` if they need to. The workload
manifests live in Git because that's what ArgoCD syncs.

## Why this shape (briefly)

ADR-005 is the long answer. The short answer:

- Teams want a **small, declarative surface**, not many knobs. A
  Tenant for the allocation plus a ServiceClaim per service is that
  surface.
- The platform needs **dynamic dispatch**: each object turns into a
  different bundle of resources. A controller is the natural shape
  for that.
- We delegate workload sync to **ArgoCD** because that's the job
  ArgoCD was built for. We don't reinvent it.
- ArgoCD's `selfHeal: true` handles workload drift. The controller
  only needs to react to claim changes, not to drift in the
  generated resources.

## What this is not

- **Not a workflow engine.** Argo Workflows is a finite, DAG-based
  task runner. idp-platform-lab reconciles desired state continuously.
  Different problem, different tool.
- **Not a Backstage replacement.** Backstage is a portal layer
  (UI, catalog). idp-platform-lab is a control plane (CRD + controller).
  They could coexist: Backstage could be a friendlier interface for
  filing ServiceClaims, with the same backing infrastructure.
- **Not Crossplane.** Crossplane manages external cloud resources
  via the same CRD pattern. idp-platform-lab manages in-cluster Kubernetes
  resources. The pattern is the same, the scope is narrower.
- **Not multi-cluster.** All actions happen in one cluster. Adding
  multi-cluster would be a future milestone, not part of MVP.
