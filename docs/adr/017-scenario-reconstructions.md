# ADR 017: Reconstructed before-states live in `scenarios/`

- **Status:** Accepted (2026-09-30). The pilot, `scenarios/ownership-conflict`, ran end to end on 2026-09-28, and its repro output is used in [A Team Asked for One More Service, and My Kubernetes Platform Quietly Refused](https://dev.to/yu_ting_chen/a-kubernetes-ownership-limit-i-wrote-down-before-i-hit-it-350c), published 2026-09-30.
- **Date:** 2026-09-16
- **Implemented:** partial (the pilot scenario and the `CODEGEN_PATHS` narrowing are built; no second scenario exists yet)
- **Deciders:** Yu Ting
- **Related:** ADR-010 (the split the pilot reproduces), ADR-012 (what the M3 `Application` did not carry), ADR-008 / ADR-009 (the M3 resource shapes a reconstruction copies), ROADMAP (feature freeze at M4)

## Context

Writing about this lab means describing failures the current code no longer has.
A reader can read the ADR, but cannot run the version that broke. The git
history starts after the fix, both here and in the predecessor repo, because
both were squashed. So the reasoning is on record and the failure is not.

Not every scenario has a broken predecessor. Roughly three kinds exist:

- A **runtime before-state**: an earlier or naive design that fails in a way you
  can watch, such as the pre-split `ServiceClaim` in ADR-010.
- A **config before-state**: the same thing, reachable by changing manifests or
  flags rather than code.
- A **decision** with nothing broken behind it, such as build vs adopt
  (ADR-015) or what the platform refuses to own. Staging a failure for these
  would be theatre.

This ADR records where a reconstruction lives, what it has to say about itself,
and how it stays out of the main build. It does not reopen the feature freeze:
a reconstruction adds no CRD, no controller capability, and no milestone.

## Decision

### 1. Only scenarios with a before-state get a directory

`scenarios/<slug>/`, named after the failure it shows (the pilot is
`scenarios/ownership-conflict`). It started as `NN-slug`, numbered after the
private scenario cards, but a reader only saw a lone `03` with no visible
sequence, so the number was dropped. Decision-only scenarios
stay where they are, in `docs/scenarios.md` and the ADRs. If a scenario has no
runnable failure, it gets prose, not a directory.

### 2. Each directory holds a README and a `before/`

The README covers: the situation in the words a team would use, how to run the
before-state, what to look for, where the after-state evidence lives in this
repo, and a section titled "Reconstructed, not recovered".

### 3. A reconstruction states that it is one

The code is written now. It is not recovered from history, and it must not
pretend to be. Two rules follow:

- Drop anything that belongs to a later milestone. The pilot drops both
  finalizers, the ArgoCD cascade finalizer (ADR-012), the Tenant gate and the
  webhook, because M3 had none of them.
- List what is faithful, what is a choice made today, and what the
  reconstruction cannot show at all. The pilot cannot show the shared-quota
  contention ADR-010 mentions, because the second claim stops at the first step.

### 4. It never touches the main cluster or the main build

- Its own `go.mod`, so the root module ignores it.
- Its own k3d cluster and its own kubeconfig file. Every task pins `KUBECONFIG`
  to that file. Tasks that talk to the cluster check the context name first and
  stop if it is anything else. Tasks that only create or delete the cluster call
  `k3d` with the scenario's own cluster name, so they need no check. The check
  compares names, not API server addresses: it catches mistakes, not a
  hand-edited kubeconfig.
- Root code generation is pinned to `CODEGEN_PATHS` (`./api/...`,
  `./internal/...`, `./cmd/...`). With `./...`, controller-gen walks into nested
  modules, and a reconstruction declares the same API group and kind as the real
  one.

### 5. The after-state is linked, not copied

A scenario points at the envtest spec or the runbook step that already proves
the current behavior. Nothing about the current design is duplicated into
`scenarios/`, so there is one source of truth for how the lab works today.

## Cost accepted

- A frozen lab grows an area that still needs care. Each `before/` pins its own
  Go dependencies, so changes to the root module do not break its build. Nothing
  in CI compiles it either, so it can rot quietly.
- A reconstruction may read a file from the main repo instead of copying it. The
  pilot reads the ArgoCD `Application` CRD from `test/testdata/crds/`, because a
  copy would duplicate about 390 KB. If that fixture moves or is renamed,
  `task install` breaks. The Taskfile comment on that path names the coupling.
- A reconstruction can be wrong in ways no one notices, because there is no
  original to compare against. The README's faithful-versus-not list is the only
  guard, and it is a claim, not a proof.
- The root Makefile carries one more variable, and a new package outside those
  three paths would silently miss code generation.

## When to revisit

- Accept this once the pilot runs end to end and an article uses its output.
- Reject it, and delete `scenarios/`, if the second reconstruction costs as much
  as the first. The point is a repeatable rule. One-off effort per scenario
  means the rule is not earning its place.

## Consequences

- A reader can run the failure instead of trusting the story.
- The scenario cards gain a runnable form, and the ADRs stay the reasoning.
- Names and step order copied into a reconstruction can drift from the real
  controller. The README says which ADR each copied shape comes from, so drift
  is checkable.
