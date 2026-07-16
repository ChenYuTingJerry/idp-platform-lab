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

package v1alpha1

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

// These go through the API server so they prove the webhook is installed and
// really reads the referenced Tenant. The suite runs no controllers, so a Tenant
// with a finalizer stays in Terminating after a delete, which is exactly the
// state we need to test.
var whCounter int

var _ = Describe("ServiceClaim validating webhook", func() {
	names := func() (team, svc string) {
		whCounter++
		return fmt.Sprintf("wteam%d", whCounter), fmt.Sprintf("wsvc%d", whCounter)
	}
	makeTenant := func(name string, finalizers ...string) *platformv1alpha1.Tenant {
		return &platformv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{Name: name, Finalizers: finalizers},
			Spec: platformv1alpha1.TenantSpec{
				Resources: resources("4", "8Gi", 10),
			},
		}
	}
	makeClaim := func(name, team string) *platformv1alpha1.ServiceClaim {
		return &platformv1alpha1.ServiceClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       platformv1alpha1.ServiceClaimSpec{Team: team, Image: "nginx:1.25"},
		}
	}

	It("admits a claim whose Tenant does not exist yet (pending, not invalid)", func() {
		_, svc := names()
		Expect(k8sClient.Create(ctx, makeClaim(svc, "no-such-team"))).To(Succeed())
	})

	It("admits a claim for an existing, live Tenant", func() {
		team, svc := names()
		Expect(k8sClient.Create(ctx, makeTenant(team))).To(Succeed())
		Expect(k8sClient.Create(ctx, makeClaim(svc, team))).To(Succeed())
	})

	It("rejects a claim for a terminating Tenant", func() {
		team, svc := names()
		By("creating a Tenant with a finalizer so it sticks in Terminating on delete")
		Expect(k8sClient.Create(ctx, makeTenant(team, "test/keep"))).To(Succeed())
		Expect(k8sClient.Delete(ctx, &platformv1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: team}})).To(Succeed())

		By("confirming it is terminating")
		var t platformv1alpha1.Tenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: team}, &t)).To(Succeed())
		Expect(t.DeletionTimestamp).NotTo(BeNil())

		By("the webhook rejecting a new claim for it")
		err := k8sClient.Create(ctx, makeClaim(svc, team))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("terminating"))
	})
})
