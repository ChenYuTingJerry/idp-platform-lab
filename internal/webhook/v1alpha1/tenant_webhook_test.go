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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
	"github.com/ChenYuTingJerry/idp-platform-lab/internal/platform"
)

// testLimits is the ceiling the webhook suite runs with: 8 CPU / 16Gi / 50 pods.
var testLimits = platform.Limits{
	MaxCPU:    resource.MustParse("8"),
	MaxMemory: resource.MustParse("16Gi"),
	MaxPods:   50,
}

func resources(cpu, mem string, pods int32) *platformv1alpha1.ResourceRequests {
	return &platformv1alpha1.ResourceRequests{
		CPU:    resource.MustParse(cpu),
		Memory: resource.MustParse(mem),
		Pods:   pods,
	}
}

var _ = Describe("Tenant validating webhook", func() {
	validator := TenantCustomValidator{Limits: testLimits}

	tenant := func(name string, r *platformv1alpha1.ResourceRequests) *platformv1alpha1.Tenant {
		return &platformv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       platformv1alpha1.TenantSpec{Resources: r},
		}
	}

	Context("the validator logic (called directly)", func() {
		It("denies a create over the ceiling and admits one at the ceiling", func() {
			_, err := validator.ValidateCreate(ctx, tenant("over", resources("9", "8Gi", 10)))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ceiling of 8"))

			_, err = validator.ValidateCreate(ctx, tenant("at", resources("8", "16Gi", 50)))
			Expect(err).NotTo(HaveOccurred())
		})

		It("allows an over-ceiling Tenant to be edited down, but not further up", func() {
			over := resources("10", "8Gi", 10)
			By("raising it further is denied")
			_, err := validator.ValidateUpdate(ctx, tenant("t", over), tenant("t", resources("12", "8Gi", 10)))
			Expect(err).To(HaveOccurred())

			By("lowering it (still over) is allowed, with a warning")
			warns, err := validator.ValidateUpdate(ctx, tenant("t", over), tenant("t", resources("9", "8Gi", 10)))
			Expect(err).NotTo(HaveOccurred())
			Expect(warns).NotTo(BeEmpty())

			By("an unchanged re-apply is allowed")
			_, err = validator.ValidateUpdate(ctx, tenant("t", over), tenant("t", resources("10", "8Gi", 10)))
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("the wiring (through the API server, proving the webhook is installed)", func() {
		It("rejects an over-ceiling Tenant at create time", func() {
			err := k8sClient.Create(ctx, tenant("wired-over", resources("100", "8Gi", 10)))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ceiling"))
		})

		It("admits a Tenant within the ceiling", func() {
			Expect(k8sClient.Create(ctx, tenant(fmt.Sprintf("wired-ok-%d", GinkgoRandomSeed()), resources("4", "8Gi", 10)))).To(Succeed())
		})
	})
})
