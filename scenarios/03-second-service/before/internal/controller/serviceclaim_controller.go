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

// Package controller holds the M3-era reconciler, reconstructed. One
// ServiceClaim creates all four objects and owns all four: the team namespace,
// the team RoleBinding, the team ResourceQuota, and its own ArgoCD
// Application. The first three are team-level, so the second claim for a team
// cannot own them. That is the failure this scenario shows. See the README for
// what is faithful here and what is a choice made today.
package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/scenarios/03-second-service/before/api/v1alpha1"
)

const (
	roleBindingName       = "team-edit"
	resourceQuotaName     = "team-quota"
	teamRole              = "edit"
	argoCDNamespace       = "argocd"
	argoAppProject        = "default"
	argoDestinationServer = "https://kubernetes.default.svc"
	argoImagePlaceholder  = "app"
	teamLabel             = "platform.idp.io/team"
	claimLabel            = "platform.idp.io/claim"
)

var applicationGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

// ServiceClaimReconciler reconciles a ServiceClaim into a namespace, a
// RoleBinding, a ResourceQuota and an ArgoCD Application.
type ServiceClaimReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	WorkloadsRepoURL        string
	WorkloadsTargetRevision string
}

// Reconcile runs the four steps in order and stops at the first failure. The
// status is written back whether the steps succeeded or not, so a failed claim
// still reports how far it got.
func (r *ServiceClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var claim platformv1alpha1.ServiceClaim
	if err := r.Get(ctx, req.NamespacedName, &claim); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	base := claim.DeepCopy()
	reconcileErr := r.reconcileResources(ctx, &claim)
	r.aggregateReady(&claim)

	if err := r.Status().Patch(ctx, &claim, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, reconcileErr
}

func (r *ServiceClaimReconciler) reconcileResources(ctx context.Context, claim *platformv1alpha1.ServiceClaim) error {
	nsName := fmt.Sprintf("team-%s", claim.Spec.Team)

	if err := r.ensureNamespace(ctx, claim, nsName); err != nil {
		return err
	}
	if err := r.ensureRoleBinding(ctx, claim, nsName); err != nil {
		return err
	}
	if err := r.ensureQuota(ctx, claim, nsName); err != nil {
		return err
	}
	return r.ensureArgoApplication(ctx, claim, nsName)
}

// ensureNamespace is where the second claim for a team fails. The namespace
// already has the first claim as its controller owner, so
// SetControllerReference returns AlreadyOwnedError, and it returns it from the
// mutate function, before anything is written.
func (r *ServiceClaimReconciler) ensureNamespace(ctx context.Context, claim *platformv1alpha1.ServiceClaim, nsName string) error {
	log := logf.FromContext(ctx)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, ns, func() error {
		return controllerutil.SetControllerReference(claim, ns, r.Scheme)
	})
	if err != nil {
		r.setCondition(claim, platformv1alpha1.ConditionNamespaceReady, metav1.ConditionFalse,
			"NamespaceError", err.Error())
		return err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled team namespace", "namespace", nsName, "operation", op)
	}
	r.setCondition(claim, platformv1alpha1.ConditionNamespaceReady, metav1.ConditionTrue,
		"NamespaceCreated", fmt.Sprintf("namespace %s is ready", nsName))
	return nil
}

func (r *ServiceClaimReconciler) ensureRoleBinding(ctx context.Context, claim *platformv1alpha1.ServiceClaim, nsName string) error {
	log := logf.FromContext(ctx)
	group := fmt.Sprintf("team-%s", claim.Spec.Team)
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
		return controllerutil.SetControllerReference(claim, rb, r.Scheme)
	})
	if err != nil {
		r.setCondition(claim, platformv1alpha1.ConditionRBACReady, metav1.ConditionFalse,
			"RBACError", err.Error())
		return err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled team RoleBinding", "namespace", nsName, "group", group, "operation", op)
	}
	r.setCondition(claim, platformv1alpha1.ConditionRBACReady, metav1.ConditionTrue,
		"RoleBindingCreated", fmt.Sprintf("group %s bound to %s in %s", group, teamRole, nsName))
	return nil
}

func (r *ServiceClaimReconciler) ensureQuota(ctx context.Context, claim *platformv1alpha1.ServiceClaim, nsName string) error {
	log := logf.FromContext(ctx)
	hard := quotaHard(claim.Spec.Resources)

	if len(hard) == 0 {
		r.setCondition(claim, platformv1alpha1.ConditionQuotaApplied, metav1.ConditionTrue,
			"NoResourcesDeclared", "claim declares no resources; namespace is uncapped")
		return nil
	}

	rq := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: resourceQuotaName, Namespace: nsName}}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, rq, func() error {
		rq.Spec.Hard = hard
		return controllerutil.SetControllerReference(claim, rq, r.Scheme)
	})
	if err != nil {
		r.setCondition(claim, platformv1alpha1.ConditionQuotaApplied, metav1.ConditionFalse,
			"QuotaError", err.Error())
		return err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconciled team ResourceQuota", "namespace", nsName, "operation", op)
	}
	r.setCondition(claim, platformv1alpha1.ConditionQuotaApplied, metav1.ConditionTrue,
		"QuotaApplied", fmt.Sprintf("resource quota applied to %s", nsName))
	return nil
}

// quotaHard maps the declared totals onto quota fields: cpu sets requests.cpu
// only (CPU is compressible, so workloads may burst); memory sets both request
// and limit; pods caps the count. See ADR-008.
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

func (r *ServiceClaimReconciler) ensureArgoApplication(ctx context.Context, claim *platformv1alpha1.ServiceClaim, nsName string) error {
	log := logf.FromContext(ctx)

	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)
	app.SetName(claim.Name)
	app.SetNamespace(argoCDNamespace)

	path := fmt.Sprintf("workloads/%s/%s", claim.Spec.Team, claim.Name)

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, app, func() error {
		app.SetLabels(map[string]string{
			teamLabel:  claim.Spec.Team,
			claimLabel: claim.Name,
		})

		source := map[string]any{
			"repoURL":        r.WorkloadsRepoURL,
			"path":           path,
			"targetRevision": r.WorkloadsTargetRevision,
		}
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

func (r *ServiceClaimReconciler) aggregateReady(claim *platformv1alpha1.ServiceClaim) {
	ready := meta.IsStatusConditionTrue(claim.Status.Conditions, platformv1alpha1.ConditionNamespaceReady) &&
		meta.IsStatusConditionTrue(claim.Status.Conditions, platformv1alpha1.ConditionRBACReady) &&
		meta.IsStatusConditionTrue(claim.Status.Conditions, platformv1alpha1.ConditionQuotaApplied) &&
		meta.IsStatusConditionTrue(claim.Status.Conditions, platformv1alpha1.ConditionArgoAppCreated)

	claim.Status.ObservedGeneration = claim.Generation
	if ready {
		claim.Status.Phase = platformv1alpha1.PhaseReady
		r.setCondition(claim, platformv1alpha1.ConditionReady, metav1.ConditionTrue,
			"AllResourcesReady", "namespace, RBAC, quota and the ArgoCD Application are in place")
		return
	}
	claim.Status.Phase = platformv1alpha1.PhasePending
	r.setCondition(claim, platformv1alpha1.ConditionReady, metav1.ConditionFalse,
		"ResourcesNotReady", "one of the claim's resources is not ready")
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

// SetupWithManager wires the reconciler. The Owns watches make drift on an
// owned object re-trigger the claim, the same way the real controller does.
func (r *ServiceClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)

	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.ServiceClaim{}).
		Owns(&corev1.Namespace{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&corev1.ResourceQuota{}).
		Owns(app).
		Named("serviceclaim").
		Complete(r)
}
