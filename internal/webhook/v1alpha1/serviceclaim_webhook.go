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
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

var serviceClaimGR = schema.GroupResource{Group: "platform.idp.io", Resource: "serviceclaims"}

// SetupServiceClaimWebhookWithManager registers the webhook for ServiceClaim. It
// reads the referenced Tenant through the manager's uncached API reader, so the
// terminating check is strongly consistent and does not depend on cache warmup.
func SetupServiceClaimWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &platformv1alpha1.ServiceClaim{}).
		WithValidator(&ServiceClaimCustomValidator{Reader: mgr.GetAPIReader()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-platform-idp-io-v1alpha1-serviceclaim,mutating=false,failurePolicy=fail,sideEffects=None,groups=platform.idp.io,resources=serviceclaims,verbs=create,versions=v1alpha1,name=vserviceclaim-v1alpha1.kb.io,admissionReviewVersions=v1

// ServiceClaimCustomValidator rejects a claim that references a Tenant which is
// being deleted. It deliberately does NOT reject a claim whose Tenant merely
// does not exist yet: that is pending, not invalid (ADR-010 §3), because a
// folder that applies a Tenant and its claims together has no ordering guarantee.
type ServiceClaimCustomValidator struct {
	Reader client.Reader
}

// ValidateCreate denies a claim pointed at a terminating Tenant, admits one
// whose Tenant is absent (not-yet) or Ready.
func (v *ServiceClaimCustomValidator) ValidateCreate(ctx context.Context, obj *platformv1alpha1.ServiceClaim) (admission.Warnings, error) {
	var tenant platformv1alpha1.Tenant
	err := v.Reader.Get(ctx, types.NamespacedName{Name: obj.Spec.Team}, &tenant)
	if apierrors.IsNotFound(err) {
		return nil, nil // not-yet: pending, not invalid
	}
	if err != nil {
		return nil, err
	}
	if !tenant.DeletionTimestamp.IsZero() {
		return nil, apierrors.NewForbidden(serviceClaimGR, obj.Name,
			fmt.Errorf("tenant %q is being deleted; a new service cannot be added to a terminating tenant", obj.Spec.Team))
	}
	return nil, nil
}

// ValidateUpdate does nothing: spec.team is the only structural concern and it is
// enforced by the CRD schema, not here.
func (v *ServiceClaimCustomValidator) ValidateUpdate(_ context.Context, _, _ *platformv1alpha1.ServiceClaim) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete does nothing: teardown must never depend on webhook logic.
func (v *ServiceClaimCustomValidator) ValidateDelete(_ context.Context, _ *platformv1alpha1.ServiceClaim) (admission.Warnings, error) {
	return nil, nil
}
