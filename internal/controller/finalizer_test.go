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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

// The trick that makes ordered teardown testable without ArgoCD: envtest never
// removes the Application's resources-finalizer, so the Application stays in
// Terminating until simulateArgoPrune strips it. That is a free "ArgoCD is
// down / now up" switch.
var _ = Describe("Ordered teardown via finalizers", func() {
	var team, svc string

	scReconciler := func() *ServiceClaimReconciler {
		return &ServiceClaimReconciler{
			Client:                  k8sClient,
			Scheme:                  k8sClient.Scheme(),
			TeamReader:              mgrReader,
			WorkloadsRepoURL:        testWorkloadsRepoURL,
			WorkloadsTargetRevision: testWorkloadsTargetRevision,
		}
	}
	tnReconciler := func() *TenantReconciler {
		return &TenantReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TeamReader: mgrReader}
	}
	reconcileClaim := func(name string) (reconcile.Result, error) {
		return scReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
	}
	reconcileTenant := func() (reconcile.Result, error) {
		return tnReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: team}})
	}
	createClaim := func(name string) {
		GinkgoHelper()
		claim := &platformv1alpha1.ServiceClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       platformv1alpha1.ServiceClaimSpec{Team: team, Image: "nginx:1.25"},
		}
		Expect(k8sClient.Create(ctx, claim)).To(Succeed())
		waitForCache(ctx, types.NamespacedName{Name: name}, &platformv1alpha1.ServiceClaim{})
	}
	getClaim := func(name string) *platformv1alpha1.ServiceClaim {
		GinkgoHelper()
		c := &platformv1alpha1.ServiceClaim{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, c)).To(Succeed())
		return c
	}
	getTenant := func() *platformv1alpha1.Tenant {
		GinkgoHelper()
		t := &platformv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: team}, t)).To(Succeed())
		return t
	}
	nsName := func() string { return "team-" + team }
	getNamespace := func() (*corev1.Namespace, error) {
		ns := &corev1.Namespace{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: nsName()}, ns)
		return ns, err
	}

	BeforeEach(func() {
		nameCounter++
		team = fmt.Sprintf("pay%d", nameCounter)
		svc = fmt.Sprintf("svc%d", nameCounter)
	})

	It("adds both finalizers on create, plus the ArgoCD finalizer and labels on the Application", func() {
		createReadyTenant(ctx, team, sampleResources())
		Expect(controllerutil.ContainsFinalizer(getTenant(), platformv1alpha1.TenantFinalizer)).To(BeTrue())

		createClaim(svc)
		_, err := reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())
		Expect(controllerutil.ContainsFinalizer(getClaim(svc), platformv1alpha1.ServiceClaimFinalizer)).To(BeTrue())

		app := argoApplication(ctx, svc)
		Expect(app.GetFinalizers()).To(ContainElement(argoResourcesFinalizer))
		Expect(app.GetLabels()).To(HaveKeyWithValue(teamLabel, team))
		Expect(app.GetLabels()).To(HaveKeyWithValue(claimLabel, svc))
	})

	It("keeps the namespace alive while the Application drains, then tears down in order", func() {
		createReadyTenant(ctx, team, sampleResources())
		createClaim(svc)
		_, err := reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())

		By("deleting the Tenant first (the worst order) and then the claim")
		Expect(k8sClient.Delete(ctx, getTenant())).To(Succeed())
		Expect(k8sClient.Delete(ctx, getClaim(svc))).To(Succeed())

		By("the Tenant blocking on its claim, not disappearing")
		_, err = reconcileTenant()
		Expect(err).NotTo(HaveOccurred())
		tenant := getTenant() // still present
		Expect(tenant.Status.Phase).To(Equal(platformv1alpha1.PhaseTerminating))
		cond := findCond(tenant.Status.Conditions, platformv1alpha1.ConditionClaimsDrained)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring(svc))

		By("the claim asking ArgoCD to prune, not disappearing")
		res, err := reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		Expect(argoApplication(ctx, svc).GetDeletionTimestamp()).NotTo(BeNil())

		By("THE anti-bug assertion: the team namespace is still alive while the drain runs")
		ns, err := getNamespace()
		Expect(err).NotTo(HaveOccurred())
		Expect(ns.DeletionTimestamp).To(BeNil())

		By("repeated Tenant reconciles staying blocked with no error")
		for range 3 {
			res, err := reconcileTenant()
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		}

		By("ArgoCD finishing the prune")
		simulateArgoPrune(ctx, svc)
		_, err = reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: svc}, &platformv1alpha1.ServiceClaim{}))).
			To(BeTrue(), "claim should be gone once its Application is pruned")

		By("the Tenant then finishing: namespace deleted, Tenant gone")
		waitForCacheGone(ctx, types.NamespacedName{Name: svc}, &platformv1alpha1.ServiceClaim{})
		_, err = reconcileTenant()
		Expect(err).NotTo(HaveOccurred())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: team}, &platformv1alpha1.Tenant{}))).
			To(BeTrue(), "tenant should be gone once its last claim drained")
		_, nsErr := getNamespace()
		Expect(nsErr == nil || apierrors.IsNotFound(nsErr)).To(BeTrue())
	})

	It("does not deadlock when the Tenant and its claims are deleted together", func() {
		createReadyTenant(ctx, team, sampleResources())
		createClaim(svc)
		_, err := reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Delete(ctx, getTenant())).To(Succeed())
		Expect(k8sClient.Delete(ctx, getClaim(svc))).To(Succeed())

		By("reconciling the Tenant ten times before ever touching the claim")
		for range 10 {
			res, err := reconcileTenant()
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeNumerically(">", 0)) // blocked, never hung, never errored
		}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: team}, &platformv1alpha1.Tenant{})).To(Succeed())

		By("the claim still being able to make progress afterwards")
		_, err = reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())
		Expect(argoApplication(ctx, svc).GetDeletionTimestamp()).NotTo(BeNil())
	})

	It("blocks on a stuck claim but the block is breakable", func() {
		createReadyTenant(ctx, team, sampleResources())
		createClaim(svc)
		_, err := reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Delete(ctx, getTenant())).To(Succeed())
		Expect(k8sClient.Delete(ctx, getClaim(svc))).To(Succeed())

		By("never calling simulateArgoPrune: both stay blocked, no hot loop, no error")
		for range 5 {
			tr, err := reconcileTenant()
			Expect(err).NotTo(HaveOccurred())
			Expect(tr.RequeueAfter).To(BeNumerically(">", 0))
			cr, err := reconcileClaim(svc)
			Expect(err).NotTo(HaveOccurred())
			Expect(cr.RequeueAfter).To(BeNumerically(">", 0))
		}
		Expect(findCond(getClaim(svc).Status.Conditions, platformv1alpha1.ConditionApplicationDrained).Status).
			To(Equal(metav1.ConditionFalse))

		By("the documented escape hatch (strip ArgoCD's finalizer) unwinding the whole chain")
		simulateArgoPrune(ctx, svc)
		_, err = reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())
		waitForCacheGone(ctx, types.NamespacedName{Name: svc}, &platformv1alpha1.ServiceClaim{})
		_, err = reconcileTenant()
		Expect(err).NotTo(HaveOccurred())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: team}, &platformv1alpha1.Tenant{}))).To(BeTrue())
	})

	It("lets a ServiceClaim complete deletion with no Tenant present (invariant D1)", func() {
		createReadyTenant(ctx, team, sampleResources())
		createClaim(svc)
		_, err := reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())

		By("removing the Tenant entirely, so the claim has nothing to wait on")
		tenant := getTenant()
		controllerutil.RemoveFinalizer(tenant, platformv1alpha1.TenantFinalizer)
		Expect(k8sClient.Update(ctx, tenant)).To(Succeed())
		Expect(k8sClient.Delete(ctx, getTenant())).To(Succeed())
		waitForCacheGone(ctx, types.NamespacedName{Name: team}, &platformv1alpha1.Tenant{})

		By("the claim still draining and disappearing on its own")
		Expect(k8sClient.Delete(ctx, getClaim(svc))).To(Succeed())
		_, err = reconcileClaim(svc) // asks ArgoCD to prune; never reads the (absent) Tenant
		Expect(err).NotTo(HaveOccurred())
		simulateArgoPrune(ctx, svc)
		_, err = reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: svc}, &platformv1alpha1.ServiceClaim{}))).To(BeTrue())
	})

	It("leaves the Tenant and namespace intact when only a ServiceClaim is deleted", func() {
		createReadyTenant(ctx, team, sampleResources())
		createClaim(svc)
		_, err := reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())

		By("deleting only the claim")
		Expect(k8sClient.Delete(ctx, getClaim(svc))).To(Succeed())
		_, err = reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())
		Expect(argoApplication(ctx, svc).GetDeletionTimestamp()).NotTo(BeNil()) // ArgoCD asked to prune the workload
		simulateArgoPrune(ctx, svc)
		_, err = reconcileClaim(svc)
		Expect(err).NotTo(HaveOccurred())

		By("the Tenant and its namespace surviving untouched")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: team}, &platformv1alpha1.Tenant{})).To(Succeed())
		ns, err := getNamespace()
		Expect(err).NotTo(HaveOccurred())
		Expect(ns.DeletionTimestamp).To(BeNil())
	})
})

// findCond is a small helper: the condition must exist.
func findCond(conds []metav1.Condition, condType string) *metav1.Condition {
	GinkgoHelper()
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	Fail(fmt.Sprintf("condition %q not found", condType))
	return nil
}
