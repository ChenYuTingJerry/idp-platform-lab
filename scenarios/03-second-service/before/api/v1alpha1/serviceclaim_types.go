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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceClaimSpec is the M3 shape: one claim carries both the team's
// allocation and one service's workload. The team-level fields (resources) and
// the service-level fields (image, replicas) sit on the same object, which is
// the conflation ADR-010 splits apart.
type ServiceClaimSpec struct {
	// team is the team this claim belongs to. It names the namespace
	// (team-<team>) the claim creates and owns.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Team string `json:"team"`

	// image is the container image to run. It becomes a kustomize image
	// override on the workload base (ADR-009).
	// +optional
	Image string `json:"image,omitempty"`

	// replicas is the desired replica count, as a kustomize replicas override.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// resources is the team's allocation for the namespace. It became the
	// Tenant's field in ADR-010; at M3 it lived here, on a per-service object.
	// +optional
	Resources *ResourceRequests `json:"resources,omitempty"`
}

// ResourceRequests is the team-facing resource declaration, mapped to a
// namespace ResourceQuota by the controller (ADR-008).
type ResourceRequests struct {
	// cpu is the total CPU the namespace may request. It sets requests.cpu.
	// +optional
	CPU resource.Quantity `json:"cpu,omitempty"`

	// memory is the total memory. It sets requests.memory and limits.memory.
	// +optional
	Memory resource.Quantity `json:"memory,omitempty"`

	// pods caps the number of pods in the namespace.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Pods int32 `json:"pods,omitempty"`
}

const (
	// PhasePending means the reconciler has not yet reached Ready.
	PhasePending = "Pending"
	// PhaseReady means every step succeeded.
	PhaseReady = "Ready"
)

const (
	// ConditionReady is the aggregate condition.
	ConditionReady = "Ready"
	// ConditionNamespaceReady reports whether the team namespace exists.
	ConditionNamespaceReady = "NamespaceReady"
	// ConditionRBACReady reports whether the team RoleBinding is in place.
	ConditionRBACReady = "RBACReady"
	// ConditionQuotaApplied reports whether the namespace ResourceQuota matches
	// the declared resources.
	ConditionQuotaApplied = "QuotaApplied"
	// ConditionArgoAppCreated reports whether the ArgoCD Application exists.
	ConditionArgoAppCreated = "ArgoAppCreated"
)

// ServiceClaimStatus defines the observed state of ServiceClaim.
type ServiceClaimStatus struct {
	// phase is a one-word summary: Pending or Ready.
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

// ServiceClaim is the Schema for the serviceclaims API, as it was at M3.
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
