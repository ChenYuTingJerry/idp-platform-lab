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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

// These prove the CRD's own CEL rules reject bad input at the API server, with
// no webhook and no controller running. Only k8sClient.Create is exercised.
var _ = Describe("CRD schema validation (CEL)", func() {
	newTenant := func(name string, res *platformv1alpha1.ResourceRequests) *platformv1alpha1.Tenant {
		return &platformv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       platformv1alpha1.TenantSpec{Resources: res},
		}
	}

	It("rejects a name that would make an invalid namespace", func() {
		// "my.team" is a valid DNS subdomain (so the name check passes) but
		// "team-my.team" is not a valid DNS label, which is what the namespace is.
		err := k8sClient.Create(ctx, newTenant("my.team", sampleResources()))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("team-<name>"))
	})

	It("rejects a name too long to fit team-<name> in 63 characters", func() {
		long := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 63 a's
		err := k8sClient.Create(ctx, newTenant(long, sampleResources()))
		Expect(err).To(HaveOccurred())
	})

	It("rejects a negative resource quantity", func() {
		nameCounter++
		res := &platformv1alpha1.ResourceRequests{
			CPU:    resource.MustParse("-1"),
			Memory: resource.MustParse("4Gi"),
		}
		err := k8sClient.Create(ctx, newTenant(fmt.Sprintf("neg%d", nameCounter), res))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must not be negative"))
	})

	It("admits a valid Tenant", func() {
		nameCounter++
		Expect(k8sClient.Create(ctx, newTenant(fmt.Sprintf("ok%d", nameCounter), sampleResources()))).To(Succeed())
	})
})
