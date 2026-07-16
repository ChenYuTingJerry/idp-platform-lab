/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

const (
	// argoCDNamespace is where ArgoCD watches for Application objects by default.
	// The generated Application lives here, not in the team namespace.
	argoCDNamespace = "argocd"
	// argoAppProject is the AppProject the generated Application belongs to.
	// "default" is permissive; a dedicated idp project is future hardening.
	argoAppProject = "default"
	// argoDestinationServer is the in-cluster API server ArgoCD syncs into.
	argoDestinationServer = "https://kubernetes.default.svc"
	// argoImagePlaceholder is the image name the workload base manifests must use
	// so the controller's kustomize image override can match and replace it.
	// See ADR-009 for this controller-to-workloads-repo contract.
	argoImagePlaceholder = "app"
	// argoResourcesFinalizer is ArgoCD's own cascade finalizer. We add it to the
	// generated Application so that deleting the Application makes ArgoCD prune the
	// workload it manages before the Application object is removed. Without it,
	// deleting a ServiceClaim would leave the workload running, unmanaged.
	argoResourcesFinalizer = "resources-finalizer.argocd.argoproj.io"
	// teamLabel and claimLabel tag the generated Application so a human (and any
	// future tooling) can find which claim and team own it.
	teamLabel  = "platform.idp.io/team"
	claimLabel = "platform.idp.io/claim"
)

// applicationGVK is the ArgoCD Application kind. We talk to it as an
// unstructured object so the controller does not pull in the heavy argo-cd Go
// module; the CRD is installed in the cluster, so the REST mapper resolves it
// (see ADR-009).
var applicationGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

// ServiceClaimReconciler reconciles a ServiceClaim object. A ServiceClaim is
// service-level: it deploys one workload into its Tenant's namespace. The
// team-level namespace, RBAC and quota belong to the Tenant (see ADR-010), so
// this controller only creates the ArgoCD Application, and only once the
// referenced Tenant is Ready.
type ServiceClaimReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// TeamReader lists ServiceClaims by the spec.team field index. That index is
	// served by the cache, not the API server, so this must be a cache-backed
	// reader (the manager client). In production it is the same object as Client;
	// the field exists so tests can keep Client strongly consistent (a direct
	// API client) while still exercising the indexed list through the cache.
	TeamReader client.Reader

	// WorkloadsRepoURL is the Git repo that holds the workload manifests ArgoCD
	// syncs. It is platform config, not part of the team-facing ServiceClaim, so
	// the spec-driven contract stays intact (see ADR-009).
	WorkloadsRepoURL string
	// WorkloadsTargetRevision is the Git revision (branch, tag, or HEAD) the
	// generated Application tracks.
	WorkloadsTargetRevision string
}

// +kubebuilder:rbac:groups=platform.idp.io,resources=serviceclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.idp.io,resources=serviceclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.idp.io,resources=serviceclaims/finalizers,verbs=update
// +kubebuilder:rbac:groups=platform.idp.io,resources=tenants,verbs=get;list;watch
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;create;update;patch;delete

// Reconcile moves the cluster toward the state declared in a ServiceClaim. It
// checks that the referenced Tenant is Ready, then makes sure the ArgoCD
// Application that syncs the workload exists, and writes per-step conditions and
// an aggregate Ready back onto the claim's status.
func (r *ServiceClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var claim platformv1alpha1.ServiceClaim
	if err := r.Get(ctx, req.NamespacedName, &claim); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Deletion is handled FIRST, before the Tenant gate or any other read.
	// Invariant D1 (see reconcileDelete): the claim's teardown must never wait on
	// its Tenant, or the two finalizers deadlock. Keeping this branch at the top,
	// above reconcileClaim's tenant Get, is what enforces that.
	if !claim.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &claim)
	}

	// Add the finalizer before any side effect, so we can never create an
	// Application we would fail to clean up.
	if err := ensureFinalizer(ctx, r.Client, &claim, platformv1alpha1.ServiceClaimFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	// statusBase is the snapshot we diff the status against. A merge patch
	// carries no resourceVersion precondition, so this status write does not
	// conflict with the second reconcile that the Owns(...) watch queues when we
	// create the Application. status is written only by this controller, so
	// last-writer-wins is safe here.
	statusBase := client.MergeFrom(claim.DeepCopy())

	reconcileErr := r.reconcileClaim(ctx, &claim)
	r.aggregateReady(&claim)

	if err := r.Status().Patch(ctx, &claim, statusBase); err != nil {
		if reconcileErr != nil {
			return ctrl.Result{}, reconcileErr
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, reconcileErr
}

// reconcileDelete drains the claim's ArgoCD Application, then releases the
// finalizer. It deletes the Application and waits until the object is actually
// gone (ArgoCD's own cascade finalizer keeps it around until the workload is
// pruned) before letting the claim leave etcd.
//
// Invariant D1: this path must NEVER read or wait on the claim's Tenant. The
// Tenant finalizer blocks on the claim; if the claim also blocked on the Tenant,
// the two would deadlock. The only object this path touches is the Application.
func (r *ServiceClaimReconciler) reconcileDelete(ctx context.Context, claim *platformv1alpha1.ServiceClaim) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(claim, platformv1alpha1.ServiceClaimFinalizer) {
		return ctrl.Result{}, nil
	}

	statusBase := client.MergeFrom(claim.DeepCopy())
	claim.Status.Phase = platformv1alpha1.PhaseTerminating

	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)
	err := r.Get(ctx, types.NamespacedName{Name: claim.Name, Namespace: argoCDNamespace}, app)

	switch {
	case apierrors.IsNotFound(err):
		// The Application is gone; ArgoCD has pruned the workload. Release.
		r.setCondition(claim, platformv1alpha1.ConditionApplicationDrained, metav1.ConditionTrue,
			"ApplicationDeleted", fmt.Sprintf("argocd Application %q is gone", claim.Name))
		if patchErr := r.Status().Patch(ctx, claim, statusBase); patchErr != nil {
			return ctrl.Result{}, client.IgnoreNotFound(patchErr)
		}
		if relErr := releaseFinalizer(ctx, r.Client, claim, platformv1alpha1.ServiceClaimFinalizer); relErr != nil {
			return ctrl.Result{}, relErr
		}
		return ctrl.Result{}, nil

	case err != nil:
		return ctrl.Result{}, err

	case app.GetDeletionTimestamp() == nil:
		// First pass: ask for deletion. The UID precondition avoids deleting an
		// Application that was recreated under the same name.
		uid := app.GetUID()
		if delErr := r.Delete(ctx, app, client.Preconditions{UID: &uid}); delErr != nil {
			return ctrl.Result{}, client.IgnoreNotFound(delErr)
		}
		r.setCondition(claim, platformv1alpha1.ConditionApplicationDrained, metav1.ConditionFalse,
			"DeletingApplication",
			fmt.Sprintf("requested deletion of Application %q; waiting for ArgoCD to prune the workload", claim.Name))
		if patchErr := r.Status().Patch(ctx, claim, statusBase); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil

	default:
		// Still terminating: ArgoCD has not finished pruning. Surface the
		// remaining finalizers so a human can see what the drain is blocked on.
		r.setCondition(claim, platformv1alpha1.ConditionApplicationDrained, metav1.ConditionFalse,
			"ApplicationDeletionBlocked",
			fmt.Sprintf("Application %q is terminating; remaining finalizers: %v", claim.Name, app.GetFinalizers()))
		if patchErr := r.Status().Patch(ctx, claim, statusBase); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
}

// reconcileClaim gates the workload on the referenced Tenant, then creates the
// Application. If the Tenant is missing or not yet Ready, the claim is pending,
// not rejected (ADR-010 §3): a bundle that applies a Tenant and its
// ServiceClaims together has no guaranteed order, so we wait and re-reconcile
// when the Tenant flips Ready (the Tenant watch in SetupWithManager re-queues
// us). We do not create the Application against a namespace that does not exist.
func (r *ServiceClaimReconciler) reconcileClaim(ctx context.Context, claim *platformv1alpha1.ServiceClaim) error {
	var tenant platformv1alpha1.Tenant
	err := r.Get(ctx, types.NamespacedName{Name: claim.Spec.Team}, &tenant)
	if apierrors.IsNotFound(err) {
		r.setCondition(claim, platformv1alpha1.ConditionTenantReady, metav1.ConditionFalse,
			"TenantNotFound", fmt.Sprintf("tenant %q does not exist yet", claim.Spec.Team))
		return nil
	}
	if err != nil {
		r.setCondition(claim, platformv1alpha1.ConditionTenantReady, metav1.ConditionFalse,
			"TenantError", err.Error())
		return err
	}
	if !meta.IsStatusConditionTrue(tenant.Status.Conditions, platformv1alpha1.ConditionReady) {
		r.setCondition(claim, platformv1alpha1.ConditionTenantReady, metav1.ConditionFalse,
			"TenantNotReady", fmt.Sprintf("tenant %q is not ready yet", claim.Spec.Team))
		return nil
	}
	r.setCondition(claim, platformv1alpha1.ConditionTenantReady, metav1.ConditionTrue,
		"TenantReady", fmt.Sprintf("tenant %q is ready", claim.Spec.Team))

	nsName := fmt.Sprintf("team-%s", claim.Spec.Team)
	return r.ensureArgoApplication(ctx, claim, nsName)
}

// ensureArgoApplication creates the ArgoCD Application that syncs the team's
// workload into its namespace. The controller owns one half of the deal (it
// declares where the manifests live and renders the team's image and replicas
// into kustomize overrides); ArgoCD owns the other half (it syncs and self-heals
// the workload). See ADR-005 for the division of labor and ADR-009 for the
// repo-and-rendering contract.
//
// The Application is created in the argocd namespace (where ArgoCD watches) but
// is owned by the cluster-scoped ServiceClaim, so deleting the claim garbage
// collects it. We talk to it as an unstructured object to avoid importing the
// argo-cd Go module; the CRD must be installed for the REST mapper to resolve
// the kind.
func (r *ServiceClaimReconciler) ensureArgoApplication(ctx context.Context, claim *platformv1alpha1.ServiceClaim, nsName string) error {
	log := logf.FromContext(ctx)

	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)
	app.SetName(claim.Name)
	app.SetNamespace(argoCDNamespace)

	// path is the per-service directory in the workloads repo. svc is the claim
	// name, which is globally unique because ServiceClaim is cluster-scoped.
	path := fmt.Sprintf("workloads/%s/%s", claim.Spec.Team, claim.Name)

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, app, func() error {
		app.SetLabels(map[string]string{
			teamLabel:  claim.Spec.Team,
			claimLabel: claim.Name,
		})
		// ArgoCD's cascade finalizer: deleting this Application makes ArgoCD prune
		// the workload first. This is what the ServiceClaim finalizer waits on
		// during teardown, so the drain actually drains.
		controllerutil.AddFinalizer(app, argoResourcesFinalizer)

		source := map[string]any{
			"repoURL":        r.WorkloadsRepoURL,
			"path":           path,
			"targetRevision": r.WorkloadsTargetRevision,
		}
		// The team's declared image and replicas become kustomize overrides on
		// the workload base. We only set an override when the field is present,
		// so the base manifest's own value wins otherwise.
		kustomize := map[string]any{}
		if claim.Spec.Image != "" {
			kustomize["images"] = []any{argoImagePlaceholder + "=" + claim.Spec.Image}
		}
		if claim.Spec.Replicas != nil {
			kustomize["replicas"] = []any{
				map[string]any{
					"name":  claim.Name,
					"count": int64(*claim.Spec.Replicas),
				},
			}
		}
		if len(kustomize) > 0 {
			source["kustomize"] = kustomize
		}
		if err := unstructured.SetNestedMap(app.Object, source, "spec", "source"); err != nil {
			return err
		}

		destination := map[string]any{
			"server":    argoDestinationServer,
			"namespace": nsName,
		}
		if err := unstructured.SetNestedMap(app.Object, destination, "spec", "destination"); err != nil {
			return err
		}

		if err := unstructured.SetNestedField(app.Object, argoAppProject, "spec", "project"); err != nil {
			return err
		}

		// selfHeal lets ArgoCD, not us, correct workload drift (ROADMAP M3).
		// prune removes resources the team dropped from Git.
		syncPolicy := map[string]any{
			"automated": map[string]any{
				"selfHeal": true,
				"prune":    true,
			},
		}
		if err := unstructured.SetNestedMap(app.Object, syncPolicy, "spec", "syncPolicy"); err != nil {
			return err
		}

		return controllerutil.SetControllerReference(claim, app, r.Scheme)
	})
	if err != nil {
		r.setCondition(claim, platformv1alpha1.ConditionArgoAppCreated, metav1.ConditionFalse,
			"ArgoAppError", err.Error())
		return err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled ArgoCD Application", "application", claim.Name, "namespace", argoCDNamespace, "operation", op)
	}

	// Surface ArgoCD's own view once it reports back. The Owns watch re-queues
	// the claim on every Application status write, so this message tracks the
	// live health and sync state. In envtest there is no ArgoCD, so the fields
	// stay empty and we report the application as applied and awaiting ArgoCD.
	health, _, _ := unstructured.NestedString(app.Object, "status", "health", "status")
	sync, _, _ := unstructured.NestedString(app.Object, "status", "sync", "status")
	msg := fmt.Sprintf("application %s applied; awaiting ArgoCD", claim.Name)
	if health != "" || sync != "" {
		msg = fmt.Sprintf("application %s: health=%s sync=%s", claim.Name, health, sync)
	}
	r.setCondition(claim, platformv1alpha1.ConditionArgoAppCreated, metav1.ConditionTrue,
		"ApplicationApplied", msg)
	return nil
}

// aggregateReady sets the top-level Ready condition and Phase from the per-step
// conditions. Ready is True only when the Tenant is ready and the Application is
// applied.
func (r *ServiceClaimReconciler) aggregateReady(claim *platformv1alpha1.ServiceClaim) {
	ready := meta.IsStatusConditionTrue(claim.Status.Conditions, platformv1alpha1.ConditionTenantReady) &&
		meta.IsStatusConditionTrue(claim.Status.Conditions, platformv1alpha1.ConditionArgoAppCreated)

	claim.Status.ObservedGeneration = claim.Generation
	if ready {
		claim.Status.Phase = platformv1alpha1.PhaseReady
		r.setCondition(claim, platformv1alpha1.ConditionReady, metav1.ConditionTrue,
			"AllResourcesReady", "tenant is ready and the ArgoCD Application is applied")
		return
	}
	claim.Status.Phase = platformv1alpha1.PhasePending
	r.setCondition(claim, platformv1alpha1.ConditionReady, metav1.ConditionFalse,
		"ResourcesNotReady", "waiting on the tenant or the ArgoCD Application")
}

func (r *ServiceClaimReconciler) setCondition(claim *platformv1alpha1.ServiceClaim, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&claim.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: claim.Generation,
		Reason:             reason,
		Message:            message,
	})
}

// claimsForTenant enqueues every ServiceClaim that references the given Tenant,
// so a claim that was pending on a not-yet-Ready Tenant re-reconciles when the
// Tenant becomes Ready. The Tenant name is the team name (see ADR-010). The list
// is served by the spec.team field index (O(matching claims)), through the
// cache-backed TeamReader.
//
// On a list error we log and enqueue nothing. That is safe here: a missed
// enqueue is corrected by the periodic resync, and this path only wakes claims
// up, it never removes anything.
func (r *ServiceClaimReconciler) claimsForTenant(ctx context.Context, tenant client.Object) []reconcile.Request {
	var claims platformv1alpha1.ServiceClaimList
	if err := r.TeamReader.List(ctx, &claims, client.MatchingFields{ClaimTeamIndexKey: tenant.GetName()}); err != nil {
		logf.FromContext(ctx).Error(err, "listing claims for tenant", "tenant", tenant.GetName())
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(claims.Items))
	for _, c := range claims.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: c.Name}})
	}
	return reqs
}

// SetupWithManager sets up the controller with the Manager. It owns the ArgoCD
// Application so ArgoCD's status writes re-trigger a reconcile, and watches
// Tenants so a claim waiting on its Tenant re-reconciles when the Tenant is
// ready.
func (r *ServiceClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// appType is an unstructured stand-in for the ArgoCD Application kind so the
	// Owns watch re-queues the claim when ArgoCD writes the Application's health
	// or sync status back. The ArgoCD CRD must be installed before the manager
	// starts, otherwise the REST mapper cannot resolve the kind.
	appType := &unstructured.Unstructured{}
	appType.SetGroupVersionKind(applicationGVK)

	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.ServiceClaim{}).
		Owns(appType).
		Watches(&platformv1alpha1.Tenant{}, handler.EnqueueRequestsFromMapFunc(r.claimsForTenant)).
		Named("serviceclaim").
		Complete(r)
}
