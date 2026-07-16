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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

var _ = Describe("The spec.team field index", func() {
	It("extracts spec.team, and nothing for an empty team or a non-claim object", func() {
		Expect(claimTeamIndexer(&platformv1alpha1.ServiceClaim{
			Spec: platformv1alpha1.ServiceClaimSpec{Team: "payments"},
		})).To(Equal([]string{"payments"}))

		// A claim with no team could never match a Tenant; index nothing so it
		// is never enqueued or counted against a Tenant's teardown.
		Expect(claimTeamIndexer(&platformv1alpha1.ServiceClaim{})).To(BeNil())

		// A non-ServiceClaim handed to the indexer must not panic.
		Expect(claimTeamIndexer(&corev1.Namespace{})).To(BeNil())
	})

	It("serves a MatchingFields list scoped to one team", func() {
		nameCounter++
		teamA := fmt.Sprintf("teama%d", nameCounter)
		nameCounter++
		teamB := fmt.Sprintf("teamb%d", nameCounter)

		created := map[string]string{
			teamA + "-api": teamA,
			teamA + "-web": teamA,
			teamB + "-api": teamB,
		}
		for name, team := range created {
			claim := &platformv1alpha1.ServiceClaim{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec:       platformv1alpha1.ServiceClaimSpec{Team: team},
			}
			Expect(k8sClient.Create(ctx, claim)).To(Succeed())
			// The index is cache-served, so wait until each claim is visible in
			// the cache before listing through it.
			waitForCache(ctx, types.NamespacedName{Name: name}, &platformv1alpha1.ServiceClaim{})
		}

		var got platformv1alpha1.ServiceClaimList
		Expect(mgrReader.List(ctx, &got, client.MatchingFields{ClaimTeamIndexKey: teamA})).To(Succeed())

		names := make([]string, 0, len(got.Items))
		for _, c := range got.Items {
			names = append(names, c.Name)
			// Nothing from another team leaks in.
			Expect(c.Spec.Team).To(Equal(teamA))
		}
		Expect(names).To(ConsistOf(teamA+"-api", teamA+"-web"))
	})
})
