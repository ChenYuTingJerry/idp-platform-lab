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

package platform

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/api/v1alpha1"
)

func res(cpu, mem string, pods int32) *platformv1alpha1.ResourceRequests {
	return &platformv1alpha1.ResourceRequests{
		CPU:    resource.MustParse(cpu),
		Memory: resource.MustParse(mem),
		Pods:   pods,
	}
}

var ceiling = Limits{
	MaxCPU:    resource.MustParse("8"),
	MaxMemory: resource.MustParse("16Gi"),
	MaxPods:   50,
}

func TestExceeded(t *testing.T) {
	cases := []struct {
		name  string
		res   *platformv1alpha1.ResourceRequests
		limit Limits
		want  int
	}{
		{"under ceiling", res("4", "8Gi", 20), ceiling, 0},
		{"exactly at ceiling", res("8", "16Gi", 50), ceiling, 0},
		{"cpu over", res("9", "8Gi", 20), ceiling, 1},
		{"all three over", res("9", "32Gi", 60), ceiling, 3},
		{"nil resources", nil, ceiling, 0},
		{"disabled ceiling admits anything", res("1000", "1000Gi", 9999), Limits{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := len(c.limit.Exceeded(c.res)); got != c.want {
				t.Errorf("Exceeded(%s) = %d violations, want %d", c.name, got, c.want)
			}
		})
	}
}

func TestWorsened(t *testing.T) {
	cases := []struct {
		name     string
		old, new *platformv1alpha1.ResourceRequests
		want     int
	}{
		{"raise cpu further over ceiling is worse", res("9", "8Gi", 20), res("10", "8Gi", 20), 1},
		{"lower an over-ceiling cpu is allowed", res("10", "8Gi", 20), res("9", "8Gi", 20), 0},
		{"unchanged over-ceiling re-apply is allowed", res("9", "8Gi", 20), res("9", "8Gi", 20), 0},
		{"raise up to exactly the ceiling is allowed", res("9", "8Gi", 20), res("8", "8Gi", 20), 0},
		{"a fresh over-ceiling create counts as worse (no old)", nil, res("9", "8Gi", 20), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := len(ceiling.Worsened(c.old, c.new)); got != c.want {
				t.Errorf("Worsened(%s) = %d, want %d", c.name, got, c.want)
			}
		})
	}
}
