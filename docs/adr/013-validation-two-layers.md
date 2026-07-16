# ADR 013: Validation in two layers, CEL in the CRD and a cert-manager webhook

- **Status:** Accepted
- **Date:** 2026-07-15
- **Implemented:** yes (CEL rules in `api/v1alpha1/tenant_types.go`; webhook in `internal/webhook/v1alpha1/`; `internal/platform/limits.go`)
- **Deciders:** Yu Ting
- **Related:** ADR-008 (quota ceiling, deferred there), ADR-010 Decision 5 (designed the webhook)

## Context

ADR-008 and ADR-010 promised a validating webhook to enforce a platform quota
ceiling and to reject a Tenant name that would make an invalid namespace. Neither
was built. Without them, a Tenant could ask for `cpu: "10000"` and the controller
would create that quota, and a Tenant named `my.team` produced the invalid
namespace `team-my.team`, which made the controller reconcile forever.

A validating webhook is not free: it adds an availability dependency on every
write, a serving certificate, and a `Service`. So the question is not only "what
to validate" but "at which layer".

## Decision

Put each rule at the cheapest layer that can enforce it.

**Static structural rules live in the CRD as CEL (`x-kubernetes-validations`). No
webhook, no cert-manager.** These hold even when the webhook is down.
- The Tenant name must produce a valid namespace: `('team-' + self.metadata.name)`
  must match the RFC 1123 label rules. The CRD's own name check is the looser DNS
  subdomain rule (dots allowed, up to 253 characters), which would let `my.team`
  or a long name through. The CEL rule rejects that at apply time.
- `cpu` and `memory` must not be negative. A negative quantity would otherwise
  produce a `ResourceQuota` the API server rejects, leaving the Tenant stuck
  Pending with no clear reason.

**Rules that need platform configuration or another object's state live in the
validating webhook.** A CRD schema cannot express either.
- The **quota ceiling** comes from operator flags (`--max-tenant-cpu`,
  `--max-tenant-memory`, `--max-tenant-pods`), never from a Tenant field, so a
  team cannot raise its own ceiling. The default is zero, which means "no ceiling"
  and disables the check. `ValidateUpdate` is **allow-if-not-worse**: it denies a
  change only when a resource is both over the ceiling and higher than before, so
  an over-ceiling Tenant can always be edited downward and a GitOps re-apply of an
  unchanged spec never starts failing. A Tenant that stays over the ceiling
  without getting worse is admitted with a warning, so a pre-existing violator is
  visible but never wedged un-editable.
- The **ServiceClaim** validator rejects a claim created against a *terminating*
  Tenant (pointing ArgoCD at a namespace that is about to disappear is never
  useful). It deliberately admits a claim whose Tenant merely does not exist yet:
  that is pending, not invalid (ADR-010 Decision 3), and a test defends that so no
  one "helpfully" tightens it later.

**The serving certificate is provisioned by cert-manager, not self-signed.** The
kubebuilder scaffold wires it: a `Certificate` and `Issuer` in `config/certmanager`,
and `cert-manager.io/inject-ca-from` injects the CA into the
`ValidatingWebhookConfiguration`. In envtest, `WebhookInstallOptions` stands in for
the whole plumbing with a throwaway CA. Both are the correct mechanism for their
context; neither is a self-signing shortcut.

`failurePolicy` is `Fail`. Failing open on a quota ceiling would let oversized
Tenants slip through exactly when the platform is unhealthy. The webhook matches
`create` and `update` only, never `delete`, so teardown never depends on webhook
availability.

## Consequences

- A bad Tenant name or a negative quantity is rejected by the API server with no
  webhook running, which is cheap and always available.
- The quota ceiling is enforced and configurable per environment, and the rollout
  is safe: ship with the ceiling disabled (zero), find violators, then set it.
- cert-manager becomes a deploy dependency (`task up:certmanager`). `make run` from
  a host sets `ENABLE_WEBHOOKS=false`, because a host run has no serving cert.
- The webhook never clamps a quota to the ceiling. Silently giving a team less than
  it declared would be the same class of hidden failure this project exists to
  remove.

## Deferred

The report-only condition for a *pre-existing* over-ceiling Tenant (one created
before the ceiling was set) is designed but not built. A validating webhook only
fires on write, so such a Tenant keeps running until its next edit; a
`WithinPlatformCeiling` status condition plus a metric would surface it. Recorded
here so the gap is documented, not hidden.

## Alternatives considered

- **Put everything in the webhook.** Simpler to read, but it makes static checks
  (name shape, non-negative) depend on webhook availability for no reason. CEL is
  the right layer for what the schema can express.
- **A `max-replicas` cap on the ServiceClaim.** Rejected: the `pods` quota is the
  real per-team enforcement, and a claim-level cap would duplicate it and could
  disagree with it. An over-quota Deployment is rejected by the ResourceQuota with
  its own message, which is more informative than an admission denial.
- **An image registry allowlist or a no-`:latest` rule.** That is policy, not
  schema, and belongs in a policy engine (Kyverno, Gatekeeper) or its own ADR, not
  welded into the platform's admission path.
