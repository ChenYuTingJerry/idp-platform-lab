# ADR 016: Install ArgoCD as a Helm release, not through Kustomize Helm inflation

- **Status:** Accepted
- **Date:** 2026-08-08
- **Implemented:** yes (`Taskfile.yml` task `up:argocd`, `infra/argocd/values.yaml`; `infra/argocd/kustomization.yaml` was removed)
- **Deciders:** Yu Ting
- **Supersedes:** the previous unrecorded practice of `kustomize build infra/argocd --enable-helm`
- **Related:** ADR-009 (Kustomize for workload rendering, which cited the old ArgoCD install as supporting evidence), `otel-platform-lab` ADR-004 (the same question, worked through first, with the reusable criteria)

## Context

ArgoCD is the one component ArgoCD cannot manage, so `task up` installs it by
hand. This repo used to do that with Kustomize's Helm chart inflator:

```sh
kustomize build infra/argocd --enable-helm | kubectl apply --server-side -f -
```

That choice was never written down. It came to light while auditing something
else, which is why this ADR exists at all.

A common misreading is that inflation does not use Helm. It does. Kustomize
shells out to `helm template`, so Helm renders the chart either way. The real
question is **which Helm subcommand runs**, and therefore whether a Helm release
exists in the cluster afterwards.

`helm upgrade --install` stores a release object, a Secret of type
`helm.sh/release.v1`, holding the rendered manifests, the values, the chart
version, and a revision number. `helm list`, `helm history` and `helm rollback`
all read it. `helm template` never creates it. Under inflation Helm is only a
renderer, not a release manager.

`otel-platform-lab` ADR-004 worked through this same choice and wrote down
criteria meant to be reused across repos. Measured against those criteria, this
repo's bootstrap matched the "use a real Helm release" profile on nearly every
axis, and the one axis pointing the other way turned out to be weaker than it
looked.

## Decision

Install ArgoCD with `helm upgrade --install` from `task up:argocd`, using
`infra/argocd/values.yaml` and the chart version pinned in `Taskfile.yml`. Delete
`infra/argocd/kustomization.yaml`.

### Why the Kustomize argument did not hold

The single argument for inflation was tool consistency: this repo's `config/`
tree is Kustomize, so keeping the bootstrap on Kustomize avoids a second tool.
ADR-009 leaned on that same "the whole project is Kustomize-native" claim.

It did not survive contact with what the setup actually did:

- **The patching ability was unused.** Inflation's real advantage over plain
  values is patching rendered output (strategic merge, JSON6902, blanket labels
  across subchart manifests). `infra/argocd/kustomization.yaml` had no patches at
  all. It set a namespace and inflated a chart, which is what `helm install`
  does on its own.
- **The reproducibility benefit was declined.** Vendoring the chart into git is
  what makes inflation deterministic, offline capable, and diffable in review.
  `.gitignore` excluded `infra/argocd/charts/`, so the chart was a local cache.
  The costs were paid, the upside was not collected.
- **Helm hooks were silently flattened.** The argo-cd 9.5.7 chart annotates four
  objects `helm.sh/hook: pre-install,pre-upgrade`, including the Job
  `argocd-redis-secret-init` that generates the Redis auth secret. Under
  inflation those annotations are inert. Install ordering was no longer
  guaranteed, and `task up` compensated with a blanket
  `kubectl wait --for=condition=available deployment --all --timeout=300s`.
- **Chart upgrades on a live cluster were set up to fail.**
  `helm.sh/hook-delete-policy: before-hook-creation` also did nothing, so nothing
  deleted the old hook Job before applying the new one. A Job's `spec.template`
  and `spec.selector` are immutable, and a new chart version carries a different
  image tag, so the apply would have been rejected. `--force-conflicts` does not
  help: it resolves server-side apply field ownership, not immutability
  validation.

So the trade was one unused benefit against four real costs.

## What this changes in practice

- `helm list -n argocd`, `helm history`, and `helm rollback` now work for the
  bootstrap.
- Hooks run as hooks. The Redis secret init Job completes before the rest of the
  release is installed, instead of racing it.
- `--wait` replaces the blanket `kubectl wait`. Helm blocks until the release's
  resources are ready, which is what the `kubectl wait` was approximating.
- Chart version bumps on a live cluster work, because Helm deletes the old hook
  Job first.
- `ARGOCD_CHART_VERSION` in `Taskfile.yml` is now actually used. It was a dead
  variable before, since the version was hardcoded in the deleted
  `kustomization.yaml`.

## Cost accepted

`helm` joins `kubectl`, `k3d`, `kustomize` and `task` as a prerequisite. That is
the whole cost, and it is smaller than it looked: `kustomize` stays for the
controller's own `config/`, so this is one more tool in the bootstrap step, not a
second rendering strategy competing with the first.

Kustomize and Helm now have clean, separate jobs in this repo:

| Layer | Tool | Why |
|---|---|---|
| Controller manifests (`config/`) | Kustomize | kubebuilder scaffold, patches and overlays are used |
| ArgoCD bootstrap | Helm | third-party chart with hooks, wants a release lifecycle |
| Team workloads | Kustomize (via ArgoCD) | plain bases, no chart boilerplate, see ADR-009 |

## Effect on ADR-009

ADR-009 chose Kustomize over Helm for rendering team workloads, and supported
that partly with "the ArgoCD install and the controller's own `config/` both use
it." Half of that clause is now false.

The conclusion still stands on its own: the workloads are plain bases with no
chart to write, and adding a chart per service is real cost for no gain. Only the
supporting evidence is weaker. ADR-009 carries a note pointing here.

## When to revisit

Move the bootstrap back to Kustomize inflation when there is something to patch
that the chart's values cannot reach, and confirm that first by rendering the
chart and checking the value surface. Reproducibility would be the other reason:
CI applying the bootstrap, more than one machine, or an air-gapped environment.
Both would also mean vendoring the chart into git, which is the part that was
missing before.

## Consequences

- The bootstrap needs network access to `argoproj.github.io/argo-helm` at install
  time. Unchanged: inflation fetched the chart from the same place.
- A Helm release for ArgoCD exists in the cluster. `task down` deletes the k3d
  cluster and the release with it.
- ArgoCD-managed resources are unaffected. How the bootstrap is installed is a
  separate concern from how ArgoCD syncs workloads.
- The two labs now agree. `otel-platform-lab` reached the same answer first, by
  the same criteria, for partly different reasons: there, Kustomize would have
  been the odd tool out among Helm-source Applications.
