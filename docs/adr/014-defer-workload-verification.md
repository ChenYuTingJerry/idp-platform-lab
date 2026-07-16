# ADR 014: Defer read-back verification of the deployed workload

- **Status:** Accepted
- **Date:** 2026-07-15
- **Implemented:** no (deferred on purpose; this ADR records the gap and the two real fixes)
- **Deciders:** Yu Ting
- **Related:** ADR-009 (kustomize overrides, the source of the silent failure), ADR-011 (workloads in this repo, which lowers the risk)

## Context

The controller renders the team's declared `image` and `replicas` into kustomize
overrides on the ArgoCD `Application`: `images: ["app=<image>"]` and `replicas:
[{name: <svc>, count: N}]`. Kustomize's image and replica transformers are
**matchers**. `images: ["app=X"]` rewrites a container only if its image is the
literal string `app`. `replicas: [{name: svc, ...}]` rewrites a workload only if it
is named `svc`.

Miss either and the transformer is a silent no-op: the base's own values deploy,
ArgoCD reports `Synced` (the rendered and live objects agree, both wrong in the
same way), and the `ServiceClaim` reports `Ready`. The team's declared image is
discarded, and no object anywhere records that fact. This is exactly the class of
hidden failure this project exists to remove.

## Decision

Do **not** build the read-back verification now. Record the gap honestly instead.

The idea, if built: after ArgoCD syncs, read the actual `Deployment` named `<svc>`
in `team-<team>` and compare its real image and replicas against the claim. On a
mismatch, write a loud failing condition (`WorkloadVerified=False`) that names the
expected and actual values, so `Ready` means "what I declared is what is running",
not "I wrote an Application object".

Why deferred:

1. **It partly patches a problem our own design created.** The silent no-op exists
   because the controller renders an ArgoCD Application with kustomize overrides
   against a separately-authored base. A cleaner long-term answer is to reconsider
   how workloads are rendered (see the alternatives), not to bolt a read-back loop
   onto the current design.
2. **The risk is now lower.** Since ADR-011 moved the workloads into this repo, the
   base is under our own review, not an external team's. A contract violation is a
   self-review issue, not a trap sprung by someone else.
3. **It is the piece most likely to sprawl.** Distinguishing "not synced yet" from
   "the override no-op'd" needs ArgoCD's sync status, a grace period, a Deployment
   watch across namespaces we do not own, and a new `Ready` aggregation. That is a
   lot of surface for a lab whose story is already complete without it.

`Ready` therefore stays `TenantReady && ArgoAppCreated`. This ADR is the honest
record that `Ready` does not yet mean the workload is running.

## Consequences

- The silent kustomize no-op remains a known limitation, documented here and in the
  workloads base comments, mitigated by the base being in-repo.
- The full "claim to running workload" path is verified manually against a live
  ArgoCD (see `docs/verification.md`), not by the controller.

## Alternatives (the two real fixes, for whoever picks this up)

- **Read-back verification.** Build the loop described above. Turns `Ready` into a
  true statement about the running workload, and surfaces the quota-rejection case
  (a base without pod `resources` is rejected by the ResourceQuota) verbatim on the
  claim. Cost: the sprawl in point 3.
- **Direct workload rendering.** Have the controller render the `Deployment` itself
  (with the image and replicas filled in directly), instead of overriding a
  separate base through kustomize. The silent no-op then cannot happen, because
  there is no matcher to miss. This is closer to what Crossplane or kro would do,
  and it is the more principled fix. Cost: the controller takes on more of the
  workload's shape, which trades some of ArgoCD's ownership back.
