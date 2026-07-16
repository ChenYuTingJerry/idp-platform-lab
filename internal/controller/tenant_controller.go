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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	// roleBindingName is the fixed name of the per-namespace RoleBinding that
	// grants the team group edit access.
	roleBindingName = "team-edit"
	// resourceQuotaName is the fixed name of the per-namespace ResourceQuota.
	resourceQuotaName = "team-quota"
	// teamRole is the built-in ClusterRole the team group is bound to. A custom
	// idp role can replace this later (see ADR-008) without changing the loop.
	teamRole = "edit"
)

// TenantReconciler reconciles a Tenant object. A Tenant is the team-level
// provisioning resource: it owns the team namespace, its RoleBinding, and its
// ResourceQuota (see ADR-010). Services are separate ServiceClaims that
// reference the Tenant by name.
type TenantReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// TeamReader lists ServiceClaims by the spec.team field index so the finalizer
	// can block teardown while any claim still references this Tenant. The index is
	// cache-served, so this must be a cache-backed reader (the manager client). In
	// production it is the same object as Client; the field lets tests keep Client
	// strongly consistent while the indexed list goes through the cache.
	TeamReader client.Reader
}

// +kubebuilder:rbac:groups=platform.idp.io,resources=tenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.idp.io,resources=tenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.idp.io,resources=tenants/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=bind,resourceNames=edit

// Reconcile moves the cluster toward the state declared in a Tenant. It makes
// sure the team namespace, its RoleBinding, and its ResourceQuota all exist and
// are owned by the Tenant, then writes per-step conditions and an aggregate
// Ready back onto the Tenant's status.
func (r *TenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var tenant platformv1alpha1.Tenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !tenant.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &tenant)
	}

	// Add the finalizer before creating the namespace, so teardown ordering is
	// guaranteed for every Tenant that ever provisioned anything.
	if err := ensureFinalizer(ctx, r.Client, &tenant, platformv1alpha1.TenantFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	// statusBase is the snapshot we diff the status against. A merge patch
	// carries no resourceVersion precondition, so this status write does not
	// conflict with the second reconcile that the Owns(...) watches queue when
	// we create an owned object. status is written only by this controller, so
	// last-writer-wins is safe here.
	statusBase := client.MergeFrom(tenant.DeepCopy())

	reconcileErr := r.reconcileResources(ctx, &tenant)
	r.aggregateReady(&tenant)

	if err := r.Status().Patch(ctx, &tenant, statusBase); err != nil {
		if reconcileErr != nil {
			return ctrl.Result{}, reconcileErr
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, reconcileErr
}

// reconcileDelete blocks the Tenant's teardown while any ServiceClaim still
// references it, then deletes the team namespace and releases the finalizer.
// Blocking is deliberate: cascade-deleting the claims would let
// `kubectl delete tenant` silently destroy every running service the team has.
//
// This is the top half of the ordered teardown. The namespace survives (this
// finalizer keeps the Tenant, and its owner reference keeps the namespace) long
// enough for each claim's own finalizer to let ArgoCD prune its workload into the
// namespace before the namespace goes away.
func (r *TenantReconciler) reconcileDelete(ctx context.Context, tenant *platformv1alpha1.Tenant) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(tenant, platformv1alpha1.TenantFinalizer) {
		return ctrl.Result{}, nil
	}

	statusBase := client.MergeFrom(tenant.DeepCopy())
	tenant.Status.Phase = platformv1alpha1.PhaseTerminating

	var claims platformv1alpha1.ServiceClaimList
	if err := r.TeamReader.List(ctx, &claims, client.MatchingFields{ClaimTeamIndexKey: tenant.Name}); err != nil {
		// A failed List must NEVER be read as "no claims remain": that would
		// release the finalizer and delete the namespace out from under a running
		// service. Return the error and retry.
		return ctrl.Result{}, err
	}

	if len(claims.Items) > 0 {
		remaining := make([]string, 0, len(claims.Items))
		for _, c := range claims.Items {
			state := "not deleted"
			if !c.DeletionTimestamp.IsZero() {
				state = "terminating"
			}
			remaining = append(remaining, fmt.Sprintf("%s (%s)", c.Name, state))
		}
		msg := fmt.Sprintf("%d ServiceClaim(s) still reference this tenant: %s",
			len(remaining), strings.Join(remaining, ", "))
		r.setCondition(tenant, platformv1alpha1.ConditionClaimsDrained, metav1.ConditionFalse, "ClaimsRemain", msg)
		r.setCondition(tenant, platformv1alpha1.ConditionReady, metav1.ConditionFalse, "TerminationBlocked", msg)
		if err := r.Status().Patch(ctx, tenant, statusBase); err != nil {
			return ctrl.Result{}, err
		}
		// The ServiceClaim watch re-queues us when the last claim disappears; the
		// requeue is a safety net and keeps the message fresh.
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	r.setCondition(tenant, platformv1alpha1.ConditionClaimsDrained, metav1.ConditionTrue,
		"AllClaimsDeleted", "no ServiceClaim references this tenant")
	if err := r.Status().Patch(ctx, tenant, statusBase); err != nil {
		return ctrl.Result{}, err
	}

	// Delete the namespace explicitly (owner-ref GC is the safety net). This makes
	// the last teardown step assertable even in envtest, which has no GC, and stops
	// the ordering from depending on which cascade policy the caller happened to use.
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("team-%s", tenant.Name)}}
	if err := r.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, releaseFinalizer(ctx, r.Client, tenant, platformv1alpha1.TenantFinalizer)
}

// reconcileResources runs the three creation steps in order and stops at the
// first failure. Each step records its own condition so the status reflects how
// far the reconcile got.
func (r *TenantReconciler) reconcileResources(ctx context.Context, tenant *platformv1alpha1.Tenant) error {
	nsName := fmt.Sprintf("team-%s", tenant.Name)

	if err := r.ensureNamespace(ctx, tenant, nsName); err != nil {
		return err
	}
	if err := r.ensureRoleBinding(ctx, tenant, nsName); err != nil {
		return err
	}
	return r.ensureQuota(ctx, tenant, nsName)
}

func (r *TenantReconciler) ensureNamespace(ctx context.Context, tenant *platformv1alpha1.Tenant, nsName string) error {
	log := logf.FromContext(ctx)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, ns, func() error {
		return controllerutil.SetControllerReference(tenant, ns, r.Scheme)
	})
	if err != nil {
		r.setCondition(tenant, platformv1alpha1.ConditionNamespaceReady, metav1.ConditionFalse,
			"NamespaceError", err.Error())
		return err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled team namespace", "namespace", nsName, "operation", op)
	}
	r.setCondition(tenant, platformv1alpha1.ConditionNamespaceReady, metav1.ConditionTrue,
		"NamespaceCreated", fmt.Sprintf("namespace %s is ready", nsName))
	return nil
}

// ensureRoleBinding binds the team group (team-<team>) to the built-in edit
// ClusterRole inside the team namespace. We bind a group, not a ServiceAccount:
// the subject is human team members (mapped from an OIDC group in production),
// not in-cluster workloads.
func (r *TenantReconciler) ensureRoleBinding(ctx context.Context, tenant *platformv1alpha1.Tenant, nsName string) error {
	log := logf.FromContext(ctx)
	group := fmt.Sprintf("team-%s", tenant.Name)
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: roleBindingName, Namespace: nsName}}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     teamRole,
		}
		rb.Subjects = []rbacv1.Subject{{
			Kind:     rbacv1.GroupKind,
			APIGroup: rbacv1.GroupName,
			Name:     group,
		}}
		return controllerutil.SetControllerReference(tenant, rb, r.Scheme)
	})
	if err != nil {
		r.setCondition(tenant, platformv1alpha1.ConditionRBACReady, metav1.ConditionFalse,
			"RBACError", err.Error())
		return err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled team RoleBinding", "namespace", nsName, "group", group, "operation", op)
	}
	r.setCondition(tenant, platformv1alpha1.ConditionRBACReady, metav1.ConditionTrue,
		"RoleBindingCreated", fmt.Sprintf("group %s bound to %s in %s", group, teamRole, nsName))
	return nil
}

// ensureQuota turns the team's declared resources into a namespace
// ResourceQuota. The mapping is opinionated and hidden from the team:
//   - cpu sets requests.cpu only. No limits.cpu, so workloads can burst.
//   - memory sets requests.memory and limits.memory to the same value, because
//     memory is incompressible and a scheduler should pack on a fixed figure.
//   - pods caps the pod count.
//
// When the tenant declares no resources, we apply no quota (the namespace is
// uncapped) and say so in the condition. The platform-enforced ceiling that
// rejects oversized asks is the validating webhook (see ADR-008, ADR-010 §5).
func (r *TenantReconciler) ensureQuota(ctx context.Context, tenant *platformv1alpha1.Tenant, nsName string) error {
	log := logf.FromContext(ctx)
	hard := quotaHard(tenant.Spec.Resources)

	if len(hard) == 0 {
		// The tenant declares no resources, so the namespace must be uncapped. If
		// an earlier reconcile applied a quota (the tenant used to declare
		// resources and they were removed), delete it. Otherwise it would keep
		// enforcing limits while the status claims the namespace is uncapped.
		rq := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: resourceQuotaName, Namespace: nsName}}
		if err := client.IgnoreNotFound(r.Delete(ctx, rq)); err != nil {
			r.setCondition(tenant, platformv1alpha1.ConditionQuotaApplied, metav1.ConditionFalse,
				"QuotaError", err.Error())
			return err
		}
		log.Info("ensured no team ResourceQuota; tenant declares no resources", "namespace", nsName)
		r.setCondition(tenant, platformv1alpha1.ConditionQuotaApplied, metav1.ConditionTrue,
			"NoResourcesDeclared", "tenant declares no resources; namespace is uncapped")
		return nil
	}

	rq := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: resourceQuotaName, Namespace: nsName}}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, rq, func() error {
		rq.Spec.Hard = hard
		return controllerutil.SetControllerReference(tenant, rq, r.Scheme)
	})
	if err != nil {
		r.setCondition(tenant, platformv1alpha1.ConditionQuotaApplied, metav1.ConditionFalse,
			"QuotaError", err.Error())
		return err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled team ResourceQuota", "namespace", nsName, "operation", op)
	}
	r.setCondition(tenant, platformv1alpha1.ConditionQuotaApplied, metav1.ConditionTrue,
		"QuotaApplied", fmt.Sprintf("resource quota applied to %s", nsName))
	return nil
}

// quotaHard builds the ResourceQuota hard limits from the team's declaration.
// Zero or unset fields are skipped so the quota only constrains what the team
// asked for.
func quotaHard(res *platformv1alpha1.ResourceRequests) corev1.ResourceList {
	hard := corev1.ResourceList{}
	if res == nil {
		return hard
	}
	if !res.CPU.IsZero() {
		hard[corev1.ResourceRequestsCPU] = res.CPU
	}
	if !res.Memory.IsZero() {
		hard[corev1.ResourceRequestsMemory] = res.Memory
		hard[corev1.ResourceLimitsMemory] = res.Memory
	}
	if res.Pods > 0 {
		hard[corev1.ResourcePods] = *resource.NewQuantity(int64(res.Pods), resource.DecimalSI)
	}
	return hard
}

// aggregateReady sets the top-level Ready condition and Phase from the per-step
// conditions. Ready is True only when every step succeeded.
func (r *TenantReconciler) aggregateReady(tenant *platformv1alpha1.Tenant) {
	ready := meta.IsStatusConditionTrue(tenant.Status.Conditions, platformv1alpha1.ConditionNamespaceReady) &&
		meta.IsStatusConditionTrue(tenant.Status.Conditions, platformv1alpha1.ConditionRBACReady) &&
		meta.IsStatusConditionTrue(tenant.Status.Conditions, platformv1alpha1.ConditionQuotaApplied)

	tenant.Status.ObservedGeneration = tenant.Generation
	if ready {
		tenant.Status.Phase = platformv1alpha1.PhaseReady
		r.setCondition(tenant, platformv1alpha1.ConditionReady, metav1.ConditionTrue,
			"AllResourcesReady", "namespace, RBAC and quota are ready")
		return
	}
	tenant.Status.Phase = platformv1alpha1.PhasePending
	r.setCondition(tenant, platformv1alpha1.ConditionReady, metav1.ConditionFalse,
		"ResourcesNotReady", "one or more resources are not ready")
}

func (r *TenantReconciler) setCondition(tenant *platformv1alpha1.Tenant, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&tenant.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: tenant.Generation,
		Reason:             reason,
		Message:            message,
	})
}

// tenantForClaim maps a ServiceClaim to the Tenant it references, so the last
// claim's disappearance unblocks a Tenant that is waiting to finish teardown
// (instead of waiting for the next periodic requeue).
func (r *TenantReconciler) tenantForClaim(_ context.Context, claim client.Object) []reconcile.Request {
	team := claim.(*platformv1alpha1.ServiceClaim).Spec.Team
	if team == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: team}}}
}

// SetupWithManager sets up the controller with the Manager. It watches the
// objects it owns so drift on any of them re-triggers a reconcile, and watches
// ServiceClaims so a terminating Tenant wakes up when its last claim is gone.
func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.Tenant{}).
		Owns(&corev1.Namespace{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&corev1.ResourceQuota{}).
		Watches(&platformv1alpha1.ServiceClaim{}, handler.EnqueueRequestsFromMapFunc(r.tenantForClaim)).
		Named("tenant").
		Complete(r)
}
