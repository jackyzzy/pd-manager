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

// ModelSizeRange defines an inclusive range for model parameter counts (in billions).
// Values are strings to avoid float precision issues across languages (e.g. "7", "70.6").
type ModelSizeRange struct {
	// MinB is the minimum model size in billions of parameters.
	MinB string `json:"minB,omitempty"`
	// MaxB is the maximum model size in billions of parameters.
	MaxB string `json:"maxB,omitempty"`
}

// ApplicabilitySpec describes the hardware and model conditions under which
// this profile is applicable. Fields are informational; pd-manager does not
// enforce matching automatically.
type ApplicabilitySpec struct {
	// GPUTypes lists compatible GPU model names (e.g. ["A30", "A100"]).
	GPUTypes []string `json:"gpuTypes,omitempty"`
	// MinGPUMemoryGiB is the minimum GPU memory required in GiB.
	MinGPUMemoryGiB *int32 `json:"minGpuMemoryGiB,omitempty"`
	// TensorParallelSize is the tensor parallel degree this profile targets.
	TensorParallelSize *int32 `json:"tensorParallelSize,omitempty"`
	// ModelSizeRange is the model size range (in billions) this profile targets.
	ModelSizeRange *ModelSizeRange `json:"modelSizeRange,omitempty"`
	// OptimizedFor describes the workload pattern (e.g. "throughput", "latency").
	OptimizedFor string `json:"optimizedFor,omitempty"`
	// SGLangVersionRequired is the minimum SGLang version required.
	SGLangVersionRequired string `json:"sglangVersionRequired,omitempty"`
}

// RuntimeContainer describes a single container override in an EngineRuntime.
// The args are passed through to the RBG engineRuntimes field without parsing.
type RuntimeContainer struct {
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}

// EngineRuntime maps to an RBG engineRuntime entry, enabling platform-specific
// container arg overrides. pd-manager passes this through without interpreting it.
type EngineRuntime struct {
	ProfileName string             `json:"profileName"`
	Containers  []RuntimeContainer `json:"containers,omitempty"`
}

// RoleEngineRuntimes holds per-role lists of engine runtimes to be forwarded to RBG.
type RoleEngineRuntimes struct {
	Prefill   []EngineRuntime `json:"prefill,omitempty"`
	Decode    []EngineRuntime `json:"decode,omitempty"`
	Scheduler []EngineRuntime `json:"scheduler,omitempty"`
}

// PDEngineProfileSpec defines the desired state of PDEngineProfile.
type PDEngineProfileSpec struct {
	// Description is a human-readable description of this profile.
	Description string `json:"description,omitempty"`

	// Applicability describes the hardware/model conditions this profile targets.
	Applicability *ApplicabilitySpec `json:"applicability,omitempty"`

	// Images specifies the container images for each role.
	Images RoleImages `json:"images"`

	// EngineRuntimes are forwarded verbatim to the RBG roleSpec.engineRuntimes field.
	EngineRuntimes *RoleEngineRuntimes `json:"engineRuntimes,omitempty"`

	// EngineConfig provides the base engine configuration for this profile.
	EngineConfig EngineConfig `json:"engineConfig"`
}

// PDEngineProfileStatus defines the observed state of PDEngineProfile.
type PDEngineProfileStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=pdep

// PDEngineProfile is the Schema for the pdengineprofiles API.
type PDEngineProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PDEngineProfileSpec   `json:"spec,omitempty"`
	Status PDEngineProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PDEngineProfileList contains a list of PDEngineProfile.
type PDEngineProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PDEngineProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PDEngineProfile{}, &PDEngineProfileList{})
}
