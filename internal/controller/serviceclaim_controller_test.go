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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

// These specs cover the ServiceClaim's Tenant gate (ADR-010): a claim only
// creates its ArgoCD Application once the referenced Tenant is Ready, and stays
// pending (not rejected) otherwise. The Application's shape is covered in
// argoapplication_test.go; the team-level namespace/RBAC/quota are covered in
// tenant_controller_test.go.
var _ = Describe("ServiceClaim Controller Tenant gate", func() {
	ctx := context.Background()

	var (
		teamName string
		nsName   string
	)

	BeforeEach(func() {
		nameCounter++
		teamName = fmt.Sprintf("orders%d", nameCounter)
		nsName = "team-" + teamName
	})

	reconciler := func() *ServiceClaimReconciler {
		return &ServiceClaimReconciler{
			Client:                  k8sClient,
			Scheme:                  k8sClient.Scheme(),
			TeamReader:              mgrReader,
			WorkloadsRepoURL:        testWorkloadsRepoURL,
			WorkloadsTargetRevision: testWorkloadsTargetRevision,
		}
	}

	createClaim := func(name string) {
		claim := &platformv1alpha1.ServiceClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       platformv1alpha1.ServiceClaimSpec{Team: teamName},
		}
		Expect(k8sClient.Create(ctx, claim)).To(Succeed())
	}

	getClaim := func(name string) *platformv1alpha1.ServiceClaim {
		claim := &platformv1alpha1.ServiceClaim{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, claim)).To(Succeed())
		return claim
	}

	reconcileClaim := func(name string) {
		GinkgoHelper()
		_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
		Expect(err).NotTo(HaveOccurred())
	}

	appExists := func(name string) bool {
		app := &unstructured.Unstructured{}
		app.SetGroupVersionKind(applicationGVK)
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: argoCDNamespace}, app)
		if errors.IsNotFound(err) {
			return false
		}
		Expect(err).NotTo(HaveOccurred())
		return true
	}

	Context("when the referenced Tenant does not exist", func() {
		It("stays pending and creates no Application", func() {
			createClaim(teamName)
			reconcileClaim(teamName)

			claim := getClaim(teamName)
			cond := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionTenantReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("TenantNotFound"))
			Expect(claim.Status.Phase).To(Equal(platformv1alpha1.PhasePending))
			Expect(appExists(teamName)).To(BeFalse())
		})
	})

	Context("when the referenced Tenant exists but is not Ready", func() {
		It("stays pending and creates no Application", func() {
			// Create the Tenant but never reconcile it, so it has no Ready condition.
			Expect(k8sClient.Create(ctx, &platformv1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{Name: teamName},
			})).To(Succeed())
			createClaim(teamName)
			reconcileClaim(teamName)

			claim := getClaim(teamName)
			cond := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionTenantReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("TenantNotReady"))
			Expect(claim.Status.Phase).To(Equal(platformv1alpha1.PhasePending))
			Expect(appExists(teamName)).To(BeFalse())
		})
	})

	Context("when the referenced Tenant is Ready", func() {
		It("creates the Application and reaches Ready", func() {
			createReadyTenant(ctx, teamName, sampleResources())
			createClaim(teamName)
			reconcileClaim(teamName)

			claim := getClaim(teamName)
			Expect(meta.IsStatusConditionTrue(claim.Status.Conditions, platformv1alpha1.ConditionTenantReady)).To(BeTrue())
			Expect(meta.IsStatusConditionTrue(claim.Status.Conditions, platformv1alpha1.ConditionArgoAppCreated)).To(BeTrue())
			Expect(claim.Status.Phase).To(Equal(platformv1alpha1.PhaseReady))
			Expect(appExists(teamName)).To(BeTrue())
		})

		It("lets one team run many services: two claims on one Tenant both go Ready", func() {
			// This is the bug ADR-010 fixes. Before the split, a second claim hit
			// AlreadyOwnedError on the shared namespace/RBAC/quota. Now the Tenant
			// owns those, so two claims only ever add their own Applications.
			createReadyTenant(ctx, teamName, sampleResources())

			svcA := teamName + "-a"
			svcB := teamName + "-b"
			createClaim(svcA)
			createClaim(svcB)
			reconcileClaim(svcA)
			reconcileClaim(svcB)

			By("both claims reaching Ready with their own Application")
			for _, name := range []string{svcA, svcB} {
				claim := getClaim(name)
				Expect(claim.Status.Phase).To(Equal(platformv1alpha1.PhaseReady), "claim %s should be Ready", name)
				Expect(appExists(name)).To(BeTrue(), "application %s should exist", name)
			}

			By("sharing the one namespace their Tenant provisioned")
			ns := &corev1.Namespace{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nsName}, ns)).To(Succeed())
		})
	})

	Context("when the claim does not exist", func() {
		It("ignores a claim that was never created", func() {
			reconcileClaim("no-such-claim")
		})
	})
})
