# ADR 001: Use k3d for Local Kubernetes

- **Status:** Accepted
- **Date:** 2026-04-30
- **Implemented:** yes
- **Deciders:** Yu Ting
- **Related:** ADR-000 (Argo ecosystem), ADR-002 (local registry)

## Context

idp-platform-lab needs a local Kubernetes cluster on macOS (Apple Silicon) that can:

- Run three Argo controllers (ArgoCD, Workflows, Events) plus simulated
  team workloads on a developer laptop
- Be torn down and rebuilt frequently during MVP iteration
- Support the eventual story of "build image → push → deploy" without
  tool-specific hacks
- Scale from single-node (today) to multi-node (later) without rewriting
  manifests
- Run real, conformant Kubernetes — no toy distributions

## Decision

Use **k3d** (k3s in Docker) as the local Kubernetes runtime.

## Alternatives Considered

### kind (Kubernetes IN Docker)

The canonical choice for "real Kubernetes on a laptop". Runs upstream
Kubernetes inside Docker containers; maintained by Kubernetes SIG.

| Aspect | k3d | kind |
|---|---|---|
| Distribution | k3s (Rancher) — production distro for edge/IoT | Upstream Kubernetes |
| Memory footprint | Lower (~400-500 MB idle) | Higher (~600-700 MB idle) |
| Startup time | Seconds | Tens of seconds |
| Built-in registry | First-class (`k3d registry create`) | Requires manual config |
| Built-in ingress | Traefik (production-grade) | None by default |
| Built-in LoadBalancer | ServiceLB (Klipper) | None — requires MetalLB |
| Conformance with prod | k3s = real prod distro at edge | Closer to managed K8s (EKS/GKE/AKS) |

**Why not kind:** the registry story is the deciding factor. With kind,
every image change requires `kind load docker-image`, which is a
kind-specific hack that doesn't exist in production. With k3d's local
registry, the workflow is `docker push` — identical to ECR/GCR/Harbor.
This matters because the Spec Processor's image-build step should look
the same locally and in production, otherwise the demo's CI/CD story
breaks. (See ADR-002.)

The lower memory headroom on kind also matters when running three Argo
controllers plus team workloads on a laptop.

### minikube

The original local Kubernetes tool. Mature, feature-rich, supports
multiple drivers (Docker, hyperkit, qemu, podman).

**Why not chosen:** higher resource footprint than both k3d and kind
(~35% more memory than k3d). Slower startup. Single-node only by default.
The feature breadth doesn't pay off for our scope.

### Docker Desktop's built-in Kubernetes

Zero-install for users who already have Docker Desktop.

**Why not chosen:** version is fixed by Docker Desktop release cycle,
no multi-cluster support, no easy way to embed cluster config in the
repo for reproducibility. The whole `task up` / `task down` workflow
falls apart.

### Talos Linux + Docker driver

Strong production story (immutable, API-driven). Modern alternative.

**Why not chosen:** added complexity for an MVP without a clear payoff.
Worth revisiting if idp-platform-lab ever grows into a real platform.

## Consequences

### Positive

- **Production-realistic distribution.** k3s is what runs at retail
  edge, IoT gateways, and CI runners — not a development-only flavor.
  The story "this stack could deploy to edge clusters unchanged" is
  literally true.

- **Lower resource ceiling** leaves headroom for the three Argo
  controllers plus simulated team workloads.

- **Built-in registry support** unlocks ADR-002's "build once, deploy
  everywhere" pipeline without registry-specific hacks.

- **Built-in Traefik** means we don't need to install an ingress
  controller separately. (See ADR-003 for ingress decision.)

- **Single-node now, multi-node later** is a config change, not a
  refactor. Combined with the registry decision, this means scaling
  the cluster doesn't break image distribution.

- **Production-realistic ingress out of the box.** k3d's loadbalancer
  container plus k3s's built-in Traefik means `Ingress` resources work
  the same way locally as in production — no port-forward hacks, no
  manual MetalLB install. Combined with `*.localhost` auto-resolution
  on macOS, demos look identical to a real cluster.

### Negative

- **Slightly less upstream-conformant than kind.** k3s removes some
  in-tree cloud providers and legacy storage drivers. idp-platform-lab doesn't
  use any of them, but if a future reconciler did, this would be
  a constraint.

- **k3d wraps k3s, which wraps Docker** — three layers of indirection.
  Debugging container issues occasionally requires understanding which
  layer the problem is in. Mitigated by clear documentation.

- **Klipper (ServiceLB) is unique to k3s.** We disable it (we don't
  need LoadBalancer locally), so this is moot.
