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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

var _ = Describe("Tenant Controller", func() {
	ctx := context.Background()

	var (
		teamName string
		nsName   string
		group    string

		tenantKey types.NamespacedName
		nsKey     types.NamespacedName
		rbKey     types.NamespacedName
		rqKey     types.NamespacedName
	)

	BeforeEach(func() {
		nameCounter++
		teamName = fmt.Sprintf("payments%d", nameCounter)
		nsName = "team-" + teamName
		group = "team-" + teamName

		tenantKey = types.NamespacedName{Name: teamName}
		nsKey = types.NamespacedName{Name: nsName}
		rbKey = types.NamespacedName{Name: roleBindingName, Namespace: nsName}
		rqKey = types.NamespacedName{Name: resourceQuotaName, Namespace: nsName}
	})

	reconciler := func() *TenantReconciler {
		return &TenantReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TeamReader: mgrReader}
	}

	createTenant := func(res *platformv1alpha1.ResourceRequests) {
		tenant := &platformv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{Name: teamName},
			Spec:       platformv1alpha1.TenantSpec{Resources: res},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
	}

	getTenant := func() *platformv1alpha1.Tenant {
		tenant := &platformv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, tenantKey, tenant)).To(Succeed())
		return tenant
	}

	ownedByTenant := func(refs []metav1.OwnerReference, tenantUID types.UID) {
		GinkgoHelper()
		Expect(refs).To(HaveLen(1))
		owner := refs[0]
		Expect(owner.Kind).To(Equal("Tenant"))
		Expect(owner.Name).To(Equal(teamName))
		Expect(owner.UID).To(Equal(tenantUID))
		Expect(owner.Controller).NotTo(BeNil())
		Expect(*owner.Controller).To(BeTrue())
	}

	Context("when the tenant declares resources", func() {
		BeforeEach(func() {
			createTenant(sampleResources())
		})

		It("creates the namespace, RoleBinding, and ResourceQuota, all owned by the tenant", func() {
			By("reconciling the tenant")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: tenantKey})
			Expect(err).NotTo(HaveOccurred())

			tenant := getTenant()

			By("creating a namespace named team-<team> owned by the tenant")
			ns := &corev1.Namespace{}
			Expect(k8sClient.Get(ctx, nsKey, ns)).To(Succeed())
			ownedByTenant(ns.OwnerReferences, tenant.UID)

			By("binding the team group to the edit ClusterRole in the namespace")
			rb := &rbacv1.RoleBinding{}
			Expect(k8sClient.Get(ctx, rbKey, rb)).To(Succeed())
			ownedByTenant(rb.OwnerReferences, tenant.UID)
			Expect(rb.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(rb.RoleRef.Name).To(Equal("edit"))
			Expect(rb.Subjects).To(HaveLen(1))
			Expect(rb.Subjects[0].Kind).To(Equal(rbacv1.GroupKind))
			Expect(rb.Subjects[0].Name).To(Equal(group))

			By("applying a ResourceQuota that maps cpu/memory/pods to quota keys")
			rq := &corev1.ResourceQuota{}
			Expect(k8sClient.Get(ctx, rqKey, rq)).To(Succeed())
			ownedByTenant(rq.OwnerReferences, tenant.UID)
			expectQuantity(rq.Spec.Hard[corev1.ResourceRequestsCPU], "2")
			expectQuantity(rq.Spec.Hard[corev1.ResourceRequestsMemory], "4Gi")
			expectQuantity(rq.Spec.Hard[corev1.ResourceLimitsMemory], "4Gi")
			expectQuantity(rq.Spec.Hard[corev1.ResourcePods], "10")

			By("not capping cpu with a limit, so workloads can burst")
			_, hasCPULimit := rq.Spec.Hard[corev1.ResourceLimitsCPU]
			Expect(hasCPULimit).To(BeFalse())

			By("writing Ready phase and all four conditions as True")
			Expect(tenant.Status.Phase).To(Equal(platformv1alpha1.PhaseReady))
			Expect(tenant.Status.ObservedGeneration).To(Equal(tenant.Generation))
			for _, condType := range []string{
				platformv1alpha1.ConditionNamespaceReady,
				platformv1alpha1.ConditionRBACReady,
				platformv1alpha1.ConditionQuotaApplied,
				platformv1alpha1.ConditionReady,
			} {
				cond := meta.FindStatusCondition(tenant.Status.Conditions, condType)
				Expect(cond).NotTo(BeNil(), "condition %s should be set", condType)
				Expect(cond.Status).To(Equal(metav1.ConditionTrue), "condition %s should be True", condType)
			}
		})

		It("is idempotent: a second reconcile changes nothing on the owned objects", func() {
			By("reconciling once and recording the owned objects' resourceVersions")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: tenantKey})
			Expect(err).NotTo(HaveOccurred())

			rb := &rbacv1.RoleBinding{}
			Expect(k8sClient.Get(ctx, rbKey, rb)).To(Succeed())
			rq := &corev1.ResourceQuota{}
			Expect(k8sClient.Get(ctx, rqKey, rq)).To(Succeed())
			rbVersion := rb.ResourceVersion
			rqVersion := rq.ResourceVersion

			By("reconciling again")
			_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: tenantKey})
			Expect(err).NotTo(HaveOccurred())

			By("leaving the RoleBinding and ResourceQuota untouched (no diff)")
			Expect(k8sClient.Get(ctx, rbKey, rb)).To(Succeed())
			Expect(k8sClient.Get(ctx, rqKey, rq)).To(Succeed())
			Expect(rb.ResourceVersion).To(Equal(rbVersion))
			Expect(rq.ResourceVersion).To(Equal(rqVersion))
			Expect(rb.OwnerReferences).To(HaveLen(1))
			Expect(rq.OwnerReferences).To(HaveLen(1))
		})

		It("updates the ResourceQuota when the tenant's resources change", func() {
			By("reconciling the initial tenant")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: tenantKey})
			Expect(err).NotTo(HaveOccurred())

			By("raising the declared pod count")
			tenant := getTenant()
			tenant.Spec.Resources.Pods = 25
			Expect(k8sClient.Update(ctx, tenant)).To(Succeed())

			By("reconciling again")
			_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: tenantKey})
			Expect(err).NotTo(HaveOccurred())

			By("seeing the new pod cap on the ResourceQuota")
			rq := &corev1.ResourceQuota{}
			Expect(k8sClient.Get(ctx, rqKey, rq)).To(Succeed())
			expectQuantity(rq.Spec.Hard[corev1.ResourcePods], "25")
		})

		It("removes the ResourceQuota when the tenant drops its resources", func() {
			By("reconciling the tenant with resources so a quota is applied")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: tenantKey})
			Expect(err).NotTo(HaveOccurred())
			rq := &corev1.ResourceQuota{}
			Expect(k8sClient.Get(ctx, rqKey, rq)).To(Succeed())

			By("clearing the declared resources")
			tenant := getTenant()
			tenant.Spec.Resources = nil
			Expect(k8sClient.Update(ctx, tenant)).To(Succeed())

			By("reconciling again")
			_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: tenantKey})
			Expect(err).NotTo(HaveOccurred())

			By("deleting the now-stale ResourceQuota so the namespace is truly uncapped")
			err = k8sClient.Get(ctx, rqKey, rq)
			Expect(errors.IsNotFound(err)).To(BeTrue())

			By("reporting QuotaApplied=True with NoResourcesDeclared and staying Ready")
			tenant = getTenant()
			cond := meta.FindStatusCondition(tenant.Status.Conditions, platformv1alpha1.ConditionQuotaApplied)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("NoResourcesDeclared"))
			Expect(tenant.Status.Phase).To(Equal(platformv1alpha1.PhaseReady))
		})
	})

	Context("when the tenant declares no resources", func() {
		BeforeEach(func() {
			createTenant(nil)
		})

		It("applies no ResourceQuota but still reaches Ready", func() {
			By("reconciling the tenant")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: tenantKey})
			Expect(err).NotTo(HaveOccurred())

			By("not creating a ResourceQuota")
			rq := &corev1.ResourceQuota{}
			err = k8sClient.Get(ctx, rqKey, rq)
			Expect(errors.IsNotFound(err)).To(BeTrue())

			By("reporting QuotaApplied=True with NoResourcesDeclared and overall Ready")
			tenant := getTenant()
			cond := meta.FindStatusCondition(tenant.Status.Conditions, platformv1alpha1.ConditionQuotaApplied)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("NoResourcesDeclared"))
			Expect(tenant.Status.Phase).To(Equal(platformv1alpha1.PhaseReady))
		})
	})

	Context("when the tenant does not exist", func() {
		It("ignores a tenant that was never created", func() {
			By("reconciling a name that does not exist")
			_, err := reconciler().Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "ghost"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("not creating a namespace for it")
			ns := &corev1.Namespace{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "team-ghost"}, ns)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})
})
