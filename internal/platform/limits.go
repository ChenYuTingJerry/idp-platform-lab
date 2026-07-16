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

// Package platform holds platform configuration that is not part of any
// team-facing API. The quota ceiling lives here: it comes from operator flags,
// never from a Tenant field, so a team can never raise its own ceiling.
package platform

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

// Limits is the platform-enforced ceiling on what a single Tenant may request.
// A zero value for any field means "no ceiling" for that resource, so the zero
// Limits{} disables the ceiling entirely. This is what makes the safe rollout
// possible: ship with the ceiling off, find the violators, then turn it on.
type Limits struct {
	MaxCPU    resource.Quantity
	MaxMemory resource.Quantity
	MaxPods   int32
}

// Violation is one resource that is over the ceiling.
type Violation struct {
	Field    string
	Declared string
	Ceiling  string
}

// Enabled reports whether any ceiling is set.
func (l Limits) Enabled() bool {
	return !l.MaxCPU.IsZero() || !l.MaxMemory.IsZero() || l.MaxPods > 0
}

// Exceeded returns one Violation per resource in res that is above the ceiling.
// A nil res or a disabled ceiling yields nothing.
func (l Limits) Exceeded(res *platformv1alpha1.ResourceRequests) []Violation {
	if res == nil {
		return nil
	}
	var v []Violation
	if !l.MaxCPU.IsZero() && res.CPU.Cmp(l.MaxCPU) > 0 {
		v = append(v, Violation{"spec.resources.cpu", res.CPU.String(), l.MaxCPU.String()})
	}
	if !l.MaxMemory.IsZero() && res.Memory.Cmp(l.MaxMemory) > 0 {
		v = append(v, Violation{"spec.resources.memory", res.Memory.String(), l.MaxMemory.String()})
	}
	if l.MaxPods > 0 && res.Pods > l.MaxPods {
		v = append(v, Violation{"spec.resources.pods", fmt.Sprintf("%d", res.Pods), fmt.Sprintf("%d", l.MaxPods)})
	}
	return v
}

// Worsened implements the allow-if-not-worse rule for updates: it returns only
// the over-ceiling resources in newR that were increased relative to oldR. So a
// Tenant that is already over the ceiling can always be edited downward, and a
// GitOps re-apply of an unchanged spec never starts failing. A resource that is
// over the ceiling but unchanged (or reduced) is not returned.
func (l Limits) Worsened(oldR, newR *platformv1alpha1.ResourceRequests) []Violation {
	if newR == nil {
		return nil
	}
	prev := oldR
	if prev == nil {
		prev = &platformv1alpha1.ResourceRequests{}
	}
	var worse []Violation
	if !l.MaxCPU.IsZero() && newR.CPU.Cmp(l.MaxCPU) > 0 && newR.CPU.Cmp(prev.CPU) > 0 {
		worse = append(worse, Violation{"spec.resources.cpu", newR.CPU.String(), l.MaxCPU.String()})
	}
	if !l.MaxMemory.IsZero() && newR.Memory.Cmp(l.MaxMemory) > 0 && newR.Memory.Cmp(prev.Memory) > 0 {
		worse = append(worse, Violation{"spec.resources.memory", newR.Memory.String(), l.MaxMemory.String()})
	}
	if l.MaxPods > 0 && newR.Pods > l.MaxPods && newR.Pods > prev.Pods {
		worse = append(worse, Violation{"spec.resources.pods", fmt.Sprintf("%d", newR.Pods), fmt.Sprintf("%d", l.MaxPods)})
	}
	return worse
}
