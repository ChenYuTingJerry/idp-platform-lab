# ADR 003: Keep Traefik (k3s Built-in) as Ingress Controller

- **Status:** Accepted
- **Date:** 2026-04-30
- **Implemented:** partial (Traefik is kept and is the only ingress controller; the team-facing "expose" field this ADR describes is NOT built)
- **Deciders:** Yu Ting
- **Related:** ADR-001 (k3d), ADR-005 (ServiceClaim design — TBD)

> **Update (2026-07-15):** Only the decision to keep Traefik is implemented. The team-facing "expose" field and controller-rendered Ingress described below do NOT exist. ServiceClaim carries only team, image and replicas. This ADR is kept as the design for a future exposure capability, not a description of current behavior.

## Context

idp-platform-lab needs an Ingress controller so teams can declare HTTP exposure
in ServiceClaim (`expose.enabled: true, expose.host: ...`) and have the
Spec Processor render standard `Ingress` resources that actually serve
traffic.

The Kubernetes ingress landscape in 2026 is in flux:
- **ingress-nginx** (community, kubernetes/ingress-nginx) reaches EOL
  March 2026. No further releases, bugfixes, or CVE patches.
- **NGINX Ingress Controller** (F5/NGINX Inc., nginxinc/kubernetes-ingress)
  remains actively maintained — distinct project, often confused.
- **Gateway API** is the official forward-looking standard but still
  maturing in implementations.
- k3s ships with Traefik built-in, fully functional.

## Decision

**Keep Traefik** as the ingress controller. Do not replace it. Do not
install an additional controller for the MVP.

Treat the ingress controller choice as a platform implementation detail.
**ServiceClaim must abstract it** — teams declare `expose: true` and a
hostname, never reference the controller.

## Alternatives Considered

### Replace Traefik with NGINX Ingress Controller (F5)

Actively maintained by F5, broad annotation compatibility with the
deprecated ingress-nginx, large ecosystem. Note: the March 2026 EOL
applies to the community ingress-nginx project, not F5's NGINX Ingress
Controller — the two are often confused.

**Why not chosen:**
- Requires uninstalling Traefik (k3s flag `--disable=traefik`) and
  installing NGINX separately — adds setup steps for zero MVP benefit.
- The platform's value is in the ServiceClaim abstraction, not the
  controller. Switching now would distract from the core demo.
- Worth reconsidering if a real organization standardizes on NGINX —
  but that's a config change for the Spec Processor, not a redesign.

### Adopt Gateway API directly (no Ingress)

The forward-looking direction — the Kubernetes community is steering
new deployments here.

**Why not chosen:**
- Argo Rollouts and most reference material still target `Ingress`.
- Gateway API in k3s requires installing an implementation (e.g.,
  Envoy Gateway, Istio) — the simplicity gain of k3d evaporates.
- ServiceClaim abstraction means we can migrate later: change the
  Spec Processor to render `HTTPRoute` instead of `Ingress`; team
  specs unchanged.
- This is exactly the kind of forward-migration story that becomes
  a future ADR.

### HAProxy / Envoy / Istio

Heavier, multi-feature options. Useful for service mesh, mTLS, advanced
traffic shaping.

**Why not chosen:** all three are over-scoped for an MVP that just
needs `Host:`-based routing to work.

### Skip ingress entirely; use port-forward

Simplest possible setup.

**Why not chosen:** breaks the demo's realism. Teams in production
don't `kubectl port-forward` — they hit hostnames. The whole `expose`
field in ServiceClaim needs ingress to be meaningful.

## Consequences

### Positive

- **Zero install cost.** Traefik is running the moment `k3d cluster
  create` finishes. No extra Helm chart, no reconciler-of-the-reconciler
  problem.

- **Production-grade, not a toy.** Traefik runs at scale at many
  organizations. Choosing it isn't a downgrade.

- **Reinforces the SDD abstraction.** ServiceClaim declares intent;
  the platform picks the implementation. Demonstrating that the same
  spec could run on Traefik today and Gateway API later is a stronger
  story than picking the "best" controller upfront.

- **Works seamlessly with k3d's loadbalancer container.** `Ingress`
  resources route traffic exactly the same way they would in a managed
  cloud cluster — no port-forward hacks during demos.

### Negative

- **Traefik annotations differ from ingress-nginx.** Anyone familiar
  with ingress-nginx annotations will need to translate. Mitigated:
  ServiceClaim doesn't expose annotations to teams. Platform handles
  the translation.

- **Some team members unfamiliar with Traefik internals.** Mitigated:
  for the MVP, no Traefik-specific tuning is needed. Defaults work.

- **Locking the demo to k3s's bundled version.** k3s upgrades will
  upgrade Traefik. For idp-platform-lab's local scope this is fine;
  for a real platform we'd manage Traefik separately.
