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
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
	"github.com/ChenYuTingJerry/idp-platform-lab/internal/platform"
)

var tenantGK = schema.GroupKind{Group: "platform.idp.io", Kind: "Tenant"}

// SetupTenantWebhookWithManager registers the webhook for Tenant in the manager.
// The ceiling comes from platform config (flags), never from the Tenant, so a
// team cannot raise its own ceiling.
func SetupTenantWebhookWithManager(mgr ctrl.Manager, limits platform.Limits) error {
	return ctrl.NewWebhookManagedBy(mgr, &platformv1alpha1.Tenant{}).
		WithValidator(&TenantCustomValidator{Limits: limits}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-platform-idp-io-v1alpha1-tenant,mutating=false,failurePolicy=fail,sideEffects=None,groups=platform.idp.io,resources=tenants,verbs=create;update,versions=v1alpha1,name=vtenant-v1alpha1.kb.io,admissionReviewVersions=v1

// TenantCustomValidator enforces the platform quota ceiling. Static structural
// rules (name shape, non-negative quantities) live in the CRD as CEL and are not
// repeated here; the webhook carries only what a CRD cannot express, which is a
// ceiling that depends on platform configuration.
type TenantCustomValidator struct {
	Limits platform.Limits
}

func deniedTenant(name string, violations []platform.Violation) error {
	errs := make(field.ErrorList, 0, len(violations))
	for _, vio := range violations {
		errs = append(errs, field.Invalid(field.NewPath(vio.Field), vio.Declared,
			fmt.Sprintf("exceeds the platform ceiling of %s", vio.Ceiling)))
	}
	return apierrors.NewInvalid(tenantGK, name, errs)
}

// ValidateCreate rejects a Tenant whose resources are above the ceiling.
func (v *TenantCustomValidator) ValidateCreate(_ context.Context, obj *platformv1alpha1.Tenant) (admission.Warnings, error) {
	if vs := v.Limits.Exceeded(obj.Spec.Resources); len(vs) > 0 {
		return nil, deniedTenant(obj.Name, vs)
	}
	return nil, nil
}

// ValidateUpdate applies the allow-if-not-worse rule: an over-ceiling Tenant can
// always be edited downward, and an unchanged re-apply always passes. Only a
// resource raised further above the ceiling is rejected. A Tenant that stays
// over the ceiling without getting worse is admitted with a warning, so a
// pre-existing violator is visible but never wedged un-editable.
func (v *TenantCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *platformv1alpha1.Tenant) (admission.Warnings, error) {
	if worse := v.Limits.Worsened(oldObj.Spec.Resources, newObj.Spec.Resources); len(worse) > 0 {
		return nil, deniedTenant(newObj.Name, worse)
	}
	if vs := v.Limits.Exceeded(newObj.Spec.Resources); len(vs) > 0 {
		return admission.Warnings{
			fmt.Sprintf("tenant %q is over the platform ceiling but was not made worse; tolerated", newObj.Name),
		}, nil
	}
	return nil, nil
}

// ValidateDelete does nothing: teardown must never depend on webhook logic.
func (v *TenantCustomValidator) ValidateDelete(_ context.Context, _ *platformv1alpha1.Tenant) (admission.Warnings, error) {
	return nil, nil
}
