# ADR 002: Use k3d-Managed Local Registry

- **Status:** Accepted
- **Date:** 2026-04-30
- **Implemented:** yes
- **Deciders:** Yu Ting
- **Related:** ADR-001 (k3d), ADR-000 (Argo ecosystem)

## Context

idp-platform-lab's Spec Processor includes an image build step: when a team
submits or updates a ServiceClaim, the platform builds a container image,
tags it deterministically (e.g., commit SHA), and deploys it via ArgoCD.

For the demo to be production-realistic, the build → push → pull flow
must mirror what happens with ECR/GCR/Harbor in production. The cluster
also needs to scale from single-node to multi-node later (ADR-001)
without breaking image distribution.

## Decision

Use a **k3d-managed local Docker registry** (`k3d registry create`),
wired into the cluster at creation time, exposed on host port `5050`.

Image flow:
```
docker build -t k3d-idp-registry:5050/<team>/<svc>:<sha> .
docker push k3d-idp-registry:5050/<team>/<svc>:<sha>
# Spec Processor renders manifests pointing at the same image ref
# ArgoCD syncs; cluster pulls from the local registry
```

## Alternatives Considered

### `k3d image import` (or `kind load docker-image`)

Both kind and k3d offer commands to push a local Docker image directly
into the cluster's containerd, bypassing any registry.

**Why not chosen:**
- **Tool-specific hack.** Production has no `image import` command.
  The Spec Processor's build step would need a "if local then import,
  else push" branch — a divergence between local and production code paths.
- **Whole-image transfer.** Every change re-uploads the entire image
  (no layer caching).
- **Breaks under multi-node.** With multiple agent nodes, every agent
  needs the image imported separately. Trivial in single-node, painful
  at scale.

### Push directly to a public registry (Docker Hub, GHCR)

**Why not chosen:** requires credentials in the demo, hits rate limits,
adds a network dependency for `task up` to work. The whole point of
local development is to be self-contained.

### Run a registry as a Kubernetes Deployment inside the cluster

A `Deployment` running `registry:2` plus a `Service`, accessed via
ingress or NodePort.

**Why not chosen:** chicken-and-egg problem — to push images for
idp-platform-lab itself you'd need the cluster up and the registry deployed
first. k3d's external registry container is available the moment the
cluster starts, with zero in-cluster dependencies.

### Harbor (full-featured registry)

The production-grade choice with vulnerability scanning, replication,
RBAC.

**Why not chosen:** massive overkill for a 1–2 week MVP. Worth noting
as the "production migration target": the ServiceClaim image reference
doesn't care which registry serves it.

## Consequences

### Positive

- **Identical workflow to production.** `docker push` works the same
  locally as it does pushing to ECR. The Spec Processor build step is
  one code path, not two.

- **Layer caching.** Subsequent builds only push changed layers — fast
  iteration during development.

- **Multi-node ready.** When we scale to 1 server + N agents (ADR-001),
  every agent pulls from the same registry. No image distribution refactor.

- **Self-contained demo.** No external network or credentials needed
  for `task up` to produce a working cluster with images.

- **Reinforces SDD story.** ServiceClaim references images by name and
  tag; teams never specify which registry. The platform decides — locally
  it's `k3d-idp-registry:5050`, in production it would be
  `prod.registry.example.com`. Spec stays unchanged.

### Negative

- **One more long-running container.** k3d's registry runs as a separate
  Docker container alongside the cluster. Adds maybe 50 MB of memory.

- **Insecure registry by default.** No TLS, no auth. Acceptable for local
  dev — explicitly out of scope per CLAUDE.md. Production migration
  would point at a real registry; the Spec Processor doesn't need
  to change.

- **Registry hostname leaks into image refs.** `k3d-idp-registry:5050`
  is a k3d-specific hostname. Mitigated by the Spec Processor templating
  the registry hostname from a config value, not hardcoding it.
