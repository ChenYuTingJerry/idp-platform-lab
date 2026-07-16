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

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

// ClaimTeamIndexKey indexes ServiceClaim objects by spec.team. Both the
// ServiceClaim watch map (a Tenant event enqueues its claims) and the Tenant
// finalizer (block teardown while any claim references the tenant) list claims
// for one team; the index makes that O(matching claims) instead of listing every
// claim in the cluster and filtering in Go.
//
// A custom field selector on a CRD is served by the cache's field indexer, never
// by the API server, so any List using this key MUST go through a cache-backed
// reader (the manager client), not a direct API client.
const ClaimTeamIndexKey = "spec.team"

// claimTeamIndexer extracts the index value(s) for a ServiceClaim: its spec.team,
// or nothing for a claim with no team (which no Tenant could ever match) or a
// non-claim object. Kept as a named function so it can be unit-tested directly.
func claimTeamIndexer(o client.Object) []string {
	claim, ok := o.(*platformv1alpha1.ServiceClaim)
	if !ok || claim.Spec.Team == "" {
		return nil
	}
	return []string{claim.Spec.Team}
}

// SetupFieldIndexes registers the field indexes the controllers rely on. It must
// run once, against the manager, before either controller's SetupWithManager and
// before the cache starts. Registering the same object and field twice returns an
// indexer-conflict error, so this is the single registration point.
func SetupFieldIndexes(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &platformv1alpha1.ServiceClaim{}, ClaimTeamIndexKey, claimTeamIndexer)
}
