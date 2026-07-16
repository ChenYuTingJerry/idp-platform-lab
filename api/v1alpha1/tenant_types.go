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

// TenantSpec defines the desired state of Tenant. A Tenant is the team-level
// provisioning resource: it owns the team namespace, its RoleBinding, and its
// ResourceQuota. The Tenant name is the team name, so there is exactly one
// Tenant per team (see ADR-010). Services are declared separately as
// ServiceClaims that reference this Tenant by name.
type TenantSpec struct {
	// resources is what the team needs for its namespace. The team declares the
	// totals (the "what"); the platform turns them into a ResourceQuota (the
	// "how"). The platform-enforced ceiling that rejects oversized asks is the
	// validating webhook (see ADR-008, realized in ADR-010's M4 work).
	// +optional
	Resources *ResourceRequests `json:"resources,omitempty"`
}

// ResourceRequests is the team-facing resource declaration. It is deliberately
// small and free-form (no fixed small/medium/large tiers) so a team that does
// not fit a tier never has to wait on the platform team for a tier edit.
//
// The mapping to the namespace ResourceQuota is opinionated and hidden from the
// team: CPU is compressible, so only requests.cpu is set and workloads may
// burst above it; memory is incompressible, so requests.memory and
// limits.memory are both set to the same value. This is also what lets a
// scheduler like Karpenter pack nodes correctly.
//
// A negative cpu or memory is caught here in the schema (CEL), not in the
// controller: a negative quantity would otherwise produce a ResourceQuota the
// API server rejects, leaving the Tenant stuck Pending with no clear reason.
// +kubebuilder:validation:XValidation:rule="(!has(self.cpu) || !self.cpu.startsWith('-')) && (!has(self.memory) || !self.memory.startsWith('-'))",message="cpu and memory must not be negative"
type ResourceRequests struct {
	// cpu is the total CPU the namespace may request, e.g. "2" or "500m".
	// It sets requests.cpu on the quota. No cpu limit is set, so workloads
	// can burst above their request.
	// +optional
	CPU resource.Quantity `json:"cpu,omitempty"`

	// memory is the total memory the namespace may use, e.g. "4Gi".
	// It sets both requests.memory and limits.memory to this value, because
	// memory cannot be reclaimed and its request and limit should match.
	// +optional
	Memory resource.Quantity `json:"memory,omitempty"`

	// pods caps the number of pods in the namespace.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Pods int32 `json:"pods,omitempty"`
}

// TenantStatus defines the observed state of Tenant.
type TenantStatus struct {
	// phase is a one-word summary of where the tenant is: Pending or Ready.
	// +optional
	Phase string `json:"phase,omitempty"`

	// observedGeneration is the .metadata.generation the controller last acted on.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the Tenant resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="CPU",type=string,JSONPath=`.spec.resources.cpu`
// +kubebuilder:printcolumn:name="Memory",type=string,JSONPath=`.spec.resources.memory`
// +kubebuilder:printcolumn:name="Pods",type=integer,JSONPath=`.spec.resources.pods`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// The Tenant name becomes the namespace name "team-<name>", so it must be a valid
// DNS label. The CRD name check only enforces the looser DNS subdomain rule
// (dots allowed, up to 253 chars), which would let "my.team" or a long name
// through and then make the controller reconcile forever against an invalid
// namespace. This CEL rule rejects that at apply time. No webhook needed.
// +kubebuilder:validation:XValidation:rule="('team-' + self.metadata.name).matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?$') && size('team-' + self.metadata.name) <= 63",message="tenant name must be a lowercase DNS label (letters, digits and '-') so that the namespace 'team-<name>' is valid and at most 63 characters"

// Tenant is the Schema for the tenants API
type Tenant struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Tenant
	// +required
	Spec TenantSpec `json:"spec"`

	// status defines the observed state of Tenant
	// +optional
	Status TenantStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TenantList contains a list of Tenant
type TenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Tenant `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Tenant{}, &TenantList{})
}
