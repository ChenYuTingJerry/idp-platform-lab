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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

// waitForCache blocks until an object created via the direct k8sClient shows up
// in the manager's cache (mgrReader). Objects reach the cache asynchronously, so
// any spec that then relies on the spec.team field index must wait first,
// otherwise the indexed List can miss a just-created claim and flake.
func waitForCache(ctx context.Context, key types.NamespacedName, obj client.Object) {
	GinkgoHelper()
	Eventually(func() error {
		return mgrReader.Get(ctx, key, obj)
	}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
}

// waitForCacheGone blocks until an object deleted via k8sClient has left the
// cache, so a Tenant's indexed claim count reflects the removal before the
// teardown reconcile reads it.
func waitForCacheGone(ctx context.Context, key types.NamespacedName, obj client.Object) {
	GinkgoHelper()
	Eventually(func() bool {
		return apierrors.IsNotFound(mgrReader.Get(ctx, key, obj))
	}, 5*time.Second, 50*time.Millisecond).Should(BeTrue())
}

// argoApplication fetches the generated ArgoCD Application for a claim.
func argoApplication(ctx context.Context, name string) *unstructured.Unstructured {
	GinkgoHelper()
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: argoCDNamespace}, app)).To(Succeed())
	return app
}

// simulateArgoPrune stands in for ArgoCD finishing its cascade: it strips the
// resources-finalizer so the (already deletion-timestamped) Application is
// actually removed from etcd. envtest has no ArgoCD, so nothing else would ever
// remove that finalizer, which makes this the "ArgoCD is down / now up" switch.
func simulateArgoPrune(ctx context.Context, name string) {
	GinkgoHelper()
	app := argoApplication(ctx, name)
	app.SetFinalizers(nil)
	Expect(k8sClient.Update(ctx, app)).To(Succeed())
}

// nameCounter gives each spec a fresh team name. envtest has no namespace
// controller, so a deleted namespace stays stuck in Terminating and blocks new
// content; a unique team per spec keeps them isolated without deleting anything.
var nameCounter int

// createReadyTenant creates a Tenant for team and reconciles it to Ready
// (namespace, RBAC, quota). ServiceClaim specs use this to satisfy the
// Tenant-Ready gate before a claim can create its Application.
func createReadyTenant(ctx context.Context, team string, res *platformv1alpha1.ResourceRequests) {
	GinkgoHelper()
	tenant := &platformv1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: team},
		Spec:       platformv1alpha1.TenantSpec{Resources: res},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	tr := &TenantReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TeamReader: mgrReader}
	_, err := tr.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: team}})
	Expect(err).NotTo(HaveOccurred())

	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: team}, tenant)).To(Succeed())
	Expect(tenant.Status.Phase).To(Equal(platformv1alpha1.PhaseReady))
}

// sampleResources is the resource declaration used across specs.
func sampleResources() *platformv1alpha1.ResourceRequests {
	return &platformv1alpha1.ResourceRequests{
		CPU:    resource.MustParse("2"),
		Memory: resource.MustParse("4Gi"),
		Pods:   10,
	}
}

// expectQuantity asserts a resource.Quantity equals want by value, using Cmp so
// internal representation differences (cached strings, formats) do not flake.
func expectQuantity(got resource.Quantity, want string) {
	GinkgoHelper()
	expected := resource.MustParse(want)
	Expect(got.Cmp(expected)).To(Equal(0),
		"expected quantity %s, got %s", expected.String(), got.String())
}
