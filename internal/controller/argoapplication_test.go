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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

const (
	testWorkloadsRepoURL        = "https://github.com/ChenYuTingJerry/idp-platform-lab"
	testWorkloadsTargetRevision = "HEAD"
)

var _ = Describe("ServiceClaim Controller ArgoCD Application", func() {
	ctx := context.Background()

	var (
		resourceName string
		teamName     string
		nsName       string
		claimKey     types.NamespacedName
		appKey       types.NamespacedName
	)

	BeforeEach(func() {
		nameCounter++
		teamName = fmt.Sprintf("checkout%d", nameCounter)
		resourceName = teamName
		nsName = "team-" + teamName
		claimKey = types.NamespacedName{Name: resourceName}
		appKey = types.NamespacedName{Name: resourceName, Namespace: argoCDNamespace}

		// The claim only creates its Application once its Tenant is Ready
		// (ADR-010), so provision the team first.
		createReadyTenant(ctx, teamName, nil)
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

	getApp := func() *unstructured.Unstructured {
		app := &unstructured.Unstructured{}
		app.SetGroupVersionKind(applicationGVK)
		Expect(k8sClient.Get(ctx, appKey, app)).To(Succeed())
		return app
	}

	createClaim := func(image string, replicas *int32) {
		claim := &platformv1alpha1.ServiceClaim{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName},
			Spec: platformv1alpha1.ServiceClaimSpec{
				Team:     teamName,
				Image:    image,
				Replicas: replicas,
			},
		}
		Expect(k8sClient.Create(ctx, claim)).To(Succeed())
	}

	It("creates an ArgoCD Application that points at the team's workload path", func() {
		replicas := int32(3)
		createClaim("ghcr.io/acme/checkout:1.4.2", &replicas)

		_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: claimKey})
		Expect(err).NotTo(HaveOccurred())

		app := getApp()

		By("owning the Application from the cluster-scoped claim")
		claim := &platformv1alpha1.ServiceClaim{}
		Expect(k8sClient.Get(ctx, claimKey, claim)).To(Succeed())
		refs := app.GetOwnerReferences()
		Expect(refs).To(HaveLen(1))
		Expect(refs[0].Kind).To(Equal("ServiceClaim"))
		Expect(refs[0].Name).To(Equal(resourceName))
		Expect(refs[0].UID).To(Equal(claim.UID))
		Expect(refs[0].Controller).NotTo(BeNil())
		Expect(*refs[0].Controller).To(BeTrue())

		By("pointing the source at workloads/<team>/<svc> in the workloads repo")
		repoURL, _, _ := unstructured.NestedString(app.Object, "spec", "source", "repoURL")
		Expect(repoURL).To(Equal(testWorkloadsRepoURL))
		path, _, _ := unstructured.NestedString(app.Object, "spec", "source", "path")
		Expect(path).To(Equal(fmt.Sprintf("workloads/%s/%s", teamName, resourceName)))
		rev, _, _ := unstructured.NestedString(app.Object, "spec", "source", "targetRevision")
		Expect(rev).To(Equal(testWorkloadsTargetRevision))

		By("syncing into the team namespace in-cluster")
		server, _, _ := unstructured.NestedString(app.Object, "spec", "destination", "server")
		Expect(server).To(Equal(argoDestinationServer))
		ns, _, _ := unstructured.NestedString(app.Object, "spec", "destination", "namespace")
		Expect(ns).To(Equal(nsName))

		By("belonging to the default AppProject")
		project, _, _ := unstructured.NestedString(app.Object, "spec", "project")
		Expect(project).To(Equal(argoAppProject))

		By("enabling automated self-heal and prune")
		selfHeal, _, _ := unstructured.NestedBool(app.Object, "spec", "syncPolicy", "automated", "selfHeal")
		Expect(selfHeal).To(BeTrue())
		prune, _, _ := unstructured.NestedBool(app.Object, "spec", "syncPolicy", "automated", "prune")
		Expect(prune).To(BeTrue())

		By("rendering the team's image and replicas as kustomize overrides")
		images, _, _ := unstructured.NestedStringSlice(app.Object, "spec", "source", "kustomize", "images")
		Expect(images).To(Equal([]string{"app=ghcr.io/acme/checkout:1.4.2"}))
		replicasOverride, _, _ := unstructured.NestedSlice(app.Object, "spec", "source", "kustomize", "replicas")
		Expect(replicasOverride).To(HaveLen(1))
		entry, ok := replicasOverride[0].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(entry["name"]).To(Equal(resourceName))
		Expect(entry["count"]).To(Equal(int64(3)))

		By("reporting ArgoAppCreated and an aggregate Ready")
		Expect(meta.IsStatusConditionTrue(claim.Status.Conditions, platformv1alpha1.ConditionArgoAppCreated)).To(BeTrue())
		Expect(meta.IsStatusConditionTrue(claim.Status.Conditions, platformv1alpha1.ConditionReady)).To(BeTrue())
		Expect(claim.Status.Phase).To(Equal(platformv1alpha1.PhaseReady))
	})

	It("is idempotent: a second reconcile leaves the Application untouched", func() {
		replicas := int32(2)
		createClaim("ghcr.io/acme/checkout:1.0.0", &replicas)

		_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: claimKey})
		Expect(err).NotTo(HaveOccurred())
		version := getApp().GetResourceVersion()

		_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: claimKey})
		Expect(err).NotTo(HaveOccurred())
		Expect(getApp().GetResourceVersion()).To(Equal(version))
	})

	It("omits kustomize overrides when image and replicas are unset", func() {
		// Build the claim in memory (not through the API) so the CRD default for
		// replicas does not fill it in. That is the only way to exercise the path
		// where both overrides are skipped.
		claim := &platformv1alpha1.ServiceClaim{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, UID: types.UID("fake-uid-" + resourceName)},
			Spec:       platformv1alpha1.ServiceClaimSpec{Team: teamName},
		}
		Expect(reconciler().ensureArgoApplication(ctx, claim, nsName)).To(Succeed())

		_, hasKustomize, err := unstructured.NestedMap(getApp().Object, "spec", "source", "kustomize")
		Expect(err).NotTo(HaveOccurred())
		Expect(hasKustomize).To(BeFalse())
	})

	It("surfaces ArgoCD health and sync status in the condition message", func() {
		replicas := int32(1)
		createClaim("ghcr.io/acme/checkout:1.0.0", &replicas)

		_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: claimKey})
		Expect(err).NotTo(HaveOccurred())

		By("simulating ArgoCD writing health and sync back onto the Application")
		app := getApp()
		Expect(unstructured.SetNestedField(app.Object, "Healthy", "status", "health", "status")).To(Succeed())
		Expect(unstructured.SetNestedField(app.Object, "Synced", "status", "sync", "status")).To(Succeed())
		Expect(k8sClient.Update(ctx, app)).To(Succeed())

		By("re-reconciling and seeing the live status in the condition message")
		_, err = reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: claimKey})
		Expect(err).NotTo(HaveOccurred())

		claim := &platformv1alpha1.ServiceClaim{}
		Expect(k8sClient.Get(ctx, claimKey, claim)).To(Succeed())
		cond := meta.FindStatusCondition(claim.Status.Conditions, platformv1alpha1.ConditionArgoAppCreated)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Message).To(ContainSubstring("health=Healthy"))
		Expect(cond.Message).To(ContainSubstring("sync=Synced"))
	})
})
