# ADR 011: Workload manifests live in the platform repo

- **Status:** Accepted
- **Date:** 2026-07-15
- **Implemented:** yes (workloads live under `workloads/<team>/<svc>/` in this repo; ArgoCD reads them over anonymous HTTPS)
- **Deciders:** Yu Ting
- **Related:** ADR-009 (workload sync; this supersedes its "separate repo" sub-decision), ADR-007 (spec-driven abstraction)
- **Supersedes:** ADR-009 Decision 1 (manifests in a separate repo)

## Context

ADR-009 put the workload base manifests in a second Git repo,
and had the controller point an ArgoCD `Application` at `workloads/<team>/<svc>/`
there. The repo URL was passed to the controller as a flag (`--workloads-repo-url`),
never as a field on the `ServiceClaim`, so the team-facing abstraction stayed clean.

Two things pushed a rethink. First, the two repos added friction for a single
lab: a change to a base and the controller that renders into it lived in
different places. Second, and more important, the platform repo was made
**private** at one point, which broke the whole model: ArgoCD reads the workloads
repo over anonymous HTTPS, so a private repo would need a repository Secret (a
deploy key or token) just to pull manifests that carry no secrets.

## Decision

Keep the workload manifests in this repo, under `workloads/<team>/<svc>/`. The
controller's path template does not change (`fmt.Sprintf("workloads/%s/%s", team,
name)`); only the repo URL does. Because this repo is **public**, ArgoCD pulls it
over anonymous HTTPS with no credentials, the same way `otel-platform-lab` serves
its own manifests.

The flag stays, and it is now **required with no default**. A default would let a
fork of this repo silently sync workloads from the original author's repo. Failing
fast at startup on an empty value is safer than a controller that looks healthy
and syncs the wrong thing.

The design choice this vindicates is ADR-009's own: because the repo URL was
always a flag and never a spec field, "one repo or two" was a deployment-time
decision, not an architectural one. Merging the repos changes a flag value and a
directory location, nothing in the reconcile loop.

## Consequences

- One repo to clone, review, and reason about. The base a `ServiceClaim` renders
  into sits next to the controller that renders it.
- ArgoCD needs no git credentials, so `task up` stays credential-free.
- This repo must stay public for anonymous pull to work. If it ever goes private,
  ArgoCD needs a repository Secret (this is the exact trade recorded above).
- The "hidden contract" from ADR-009 still holds (a base `Deployment` must be
  named `<svc>` and use the image placeholder `app`), but the base is now under
  our own review, not an external team's, which lowers the risk of a silent
  mismatch. See ADR-014 for what is still deferred here.

## Alternatives considered

- **Keep two repos and add a repository Secret** when the platform repo is
  private. Correct for a real multi-tenant platform, but it adds a credential and
  a rotation story for no benefit in a single public lab.
- **Keep two repos, both public.** Works, but keeps the split-brain friction with
  no upside once the platform repo is public anyway.
