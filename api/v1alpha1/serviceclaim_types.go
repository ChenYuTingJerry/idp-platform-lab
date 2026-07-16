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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceClaimSpec defines the desired state of ServiceClaim. A ServiceClaim is
// service-level: it declares one workload for a team. The team-level resources
// (namespace, RBAC, quota) belong to the team's Tenant (see ADR-010).
type ServiceClaimSpec struct {
	// team references the Tenant (by name) this service belongs to. The
	// workload is deployed into that Tenant's namespace (team-<team>). The
	// Tenant must be Ready before this claim's Application is created.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Team string `json:"team"`

	// image is the container image to run. It becomes a kustomize image
	// override on the workload base (see ADR-009).
	// +optional
	Image string `json:"image,omitempty"`

	// replicas is the desired replica count. It becomes a kustomize replicas
	// override on the workload base.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`
}

// These phase and condition constants are shared by both CRDs in this package
// (ServiceClaim and Tenant). The per-step conditions belong to whichever
// reconciler sets them: NamespaceReady/RBACReady/QuotaApplied are the Tenant's
// steps; TenantReady/ArgoAppCreated are the ServiceClaim's; Ready is the
// aggregate on both.
const (
	// PhasePending means the reconciler has not yet reached Ready.
	PhasePending = "Pending"
	// PhaseReady means every step succeeded and status is up to date.
	PhaseReady = "Ready"
	// PhaseTerminating means the resource is being deleted and its finalizer is
	// draining owned resources in order.
	PhaseTerminating = "Terminating"
)

const (
	// ServiceClaimFinalizer guards a ServiceClaim so its ArgoCD Application is
	// deleted (and ArgoCD prunes the workload) before the claim leaves etcd.
	ServiceClaimFinalizer = "platform.idp.io/application-teardown"
	// TenantFinalizer guards a Tenant so its namespace, RBAC and quota survive
	// until the last ServiceClaim referencing it is gone (ordered teardown).
	TenantFinalizer = "platform.idp.io/tenant-teardown"
)

const (
	// ConditionReady is the aggregate condition: True only when every step
	// for that resource has succeeded.
	ConditionReady = "Ready"
	// ConditionNamespaceReady (Tenant) reports whether the team namespace exists.
	ConditionNamespaceReady = "NamespaceReady"
	// ConditionRBACReady (Tenant) reports whether the team RoleBinding is in place.
	ConditionRBACReady = "RBACReady"
	// ConditionQuotaApplied (Tenant) reports whether the namespace ResourceQuota
	// matches the Tenant's declared resources.
	ConditionQuotaApplied = "QuotaApplied"
	// ConditionTenantReady (ServiceClaim) reports whether the referenced Tenant
	// exists and is Ready. Until it is, the claim stays pending and no
	// Application is created.
	ConditionTenantReady = "TenantReady"
	// ConditionArgoAppCreated (ServiceClaim) reports whether the ArgoCD
	// Application that syncs the team's workload has been applied. Its message
	// carries ArgoCD's own health and sync status once ArgoCD reports them.
	ConditionArgoAppCreated = "ArgoAppCreated"
	// ConditionApplicationDrained (ServiceClaim) appears only during deletion. It
	// reports whether the ArgoCD Application has been removed so ArgoCD can prune
	// the workload before the claim's finalizer is released.
	ConditionApplicationDrained = "ApplicationDrained"
	// ConditionClaimsDrained (Tenant) appears only during deletion. It reports
	// whether every ServiceClaim referencing the Tenant is gone; the Tenant blocks
	// teardown while any remain.
	ConditionClaimsDrained = "ClaimsDrained"
)

// ServiceClaimStatus defines the observed state of ServiceClaim.
type ServiceClaimStatus struct {
	// phase is a one-word summary of where the claim is: Pending or Ready.
	// +optional
	Phase string `json:"phase,omitempty"`

	// observedGeneration is the .metadata.generation the controller last acted on.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the ServiceClaim resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Team",type=string,JSONPath=`.spec.team`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ServiceClaim is the Schema for the serviceclaims API
type ServiceClaim struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ServiceClaim
	// +required
	Spec ServiceClaimSpec `json:"spec"`

	// status defines the observed state of ServiceClaim
	// +optional
	Status ServiceClaimStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ServiceClaimList contains a list of ServiceClaim
type ServiceClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ServiceClaim `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceClaim{}, &ServiceClaimList{})
}
