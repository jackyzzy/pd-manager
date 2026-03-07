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

// EngineType defines the inference engine to use.
type EngineType string

const (
	// EngineTypeSGLang uses SGLang as the inference engine.
	EngineTypeSGLang EngineType = "sglang"
)

// StorageType defines how the model is stored and mounted.
type StorageType string

const (
	StorageTypeHostPath StorageType = "hostPath"
	StorageTypePVC      StorageType = "pvc"
)

// KVBackend defines the KV cache transfer backend.
// +kubebuilder:validation:Enum=mooncake;nixl;nccl
type KVBackend string

const (
	KVBackendMooncake KVBackend = "mooncake"
	KVBackendNixl     KVBackend = "nixl"
	KVBackendNccl     KVBackend = "nccl"
)

// Phase represents the lifecycle phase of a PDInferenceService.
// +kubebuilder:validation:Enum=Pending;Initializing;Running;Failed;Terminating
type Phase string

const (
	PhasePending      Phase = "Pending"
	PhaseInitializing Phase = "Initializing"
	PhaseRunning      Phase = "Running"
	PhaseFailed       Phase = "Failed"
	PhaseTerminating  Phase = "Terminating"
)

// RouterStrategy defines the routing strategy for the scheduler.
type RouterStrategy string

const (
	RouterStrategyCacheAware   RouterStrategy = "cache-aware"
	RouterStrategyRandom       RouterStrategy = "random"
	RouterStrategyRoundRobin   RouterStrategy = "round-robin"
	RouterStrategyPowerOfTwo   RouterStrategy = "power-of-two"
)

// ModelStorageSpec describes how the model is stored and where to mount it.
type ModelStorageSpec struct {
	// Type is the storage type: hostPath or pvc.
	Type StorageType `json:"type"`
	// HostPath is the node-local path to the model directory (used when type=hostPath).
	HostPath string `json:"hostPath,omitempty"`
	// PVCName is the PVC name (used when type=pvc).
	PVCName string `json:"pvcName,omitempty"`
	// MountPath is where the model directory is mounted inside the container.
	// Defaults to /models.
	MountPath string `json:"mountPath,omitempty"`
}

// RoleImages defines the container images for each role.
type RoleImages struct {
	Scheduler string `json:"scheduler"`
	Prefill   string `json:"prefill"`
	Decode    string `json:"decode"`
}

// ResourceSpec defines compute resources for a role.
type ResourceSpec struct {
	// GPU is the number of GPU cards (e.g. "1", "4").
	GPU string `json:"gpu"`
	// GPUType optionally constrains the GPU model (e.g. "A30", "H100").
	GPUType string `json:"gpuType,omitempty"`
}

// RoleSpec describes a prefill or decode role.
type RoleSpec struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	Replicas  int32        `json:"replicas"`
	Resources ResourceSpec `json:"resources"`
}

// RouterSpec configures the sgl-router scheduler.
type RouterSpec struct {
	// Strategy is the routing strategy.
	Strategy RouterStrategy `json:"strategy,omitempty"`
}

// KVTransfer configures the KV cache transfer between prefill and decode.
type KVTransfer struct {
	// Backend is the KV transfer backend.
	// +kubebuilder:validation:Enum=mooncake;nixl;nccl
	Backend KVBackend `json:"backend"`
}

// RoleExtraArgs holds per-role extra arguments to pass to the inference engine.
type RoleExtraArgs struct {
	Prefill   []string `json:"prefill,omitempty"`
	Decode    []string `json:"decode,omitempty"`
	Scheduler []string `json:"scheduler,omitempty"`
}

// EngineConfig holds structured and unstructured engine configuration.
type EngineConfig struct {
	TensorParallelSize *int32         `json:"tensorParallelSize,omitempty"`
	KVTransfer         *KVTransfer    `json:"kvTransfer,omitempty"`
	ExtraArgs          *RoleExtraArgs `json:"extraArgs,omitempty"`
}

// HPASpec defines Horizontal Pod Autoscaler configuration for a role.
type HPASpec struct {
	MinReplicas *int32 `json:"minReplicas,omitempty"`
	MaxReplicas int32  `json:"maxReplicas"`
}

// ScalingSpec configures autoscaling for decode (and optionally prefill).
type ScalingSpec struct {
	Decode  *HPASpec `json:"decode,omitempty"`
	Prefill *HPASpec `json:"prefill,omitempty"`
}

// RoleStatus reports the observed state of a single role.
type RoleStatus struct {
	Name  string `json:"name"`
	Ready int32  `json:"ready"`
	Total int32  `json:"total"`
}

// EventRecord captures the most recent notable event.
type EventRecord struct {
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// PDInferenceServiceSpec defines the desired state of PDInferenceService.
type PDInferenceServiceSpec struct {
	// Model is the logical model name (e.g. "qwen3-14b").
	Model string `json:"model"`

	// Engine selects the inference engine. Defaults to sglang.
	// +kubebuilder:validation:Enum=sglang
	// +kubebuilder:default=sglang
	Engine EngineType `json:"engine,omitempty"`

	// ModelStorage describes where the model files are located.
	ModelStorage ModelStorageSpec `json:"modelStorage"`

	// Images specifies the container images for each role.
	// Required when engineProfileRef is not set.
	Images *RoleImages `json:"images,omitempty"`

	// Prefill configures the prefill role.
	Prefill RoleSpec `json:"prefill"`

	// Decode configures the decode role.
	Decode RoleSpec `json:"decode"`

	// Router configures the sgl-router scheduler.
	Router *RouterSpec `json:"router,omitempty"`

	// PDRatio is the desired prefill:decode ratio (e.g. "1:2").
	// Mutually exclusive with scaling.prefill.
	PDRatio string `json:"pdRatio,omitempty"`

	// EngineProfileRef names a PDEngineProfile in the pd-system namespace
	// to use as the base engine configuration template.
	EngineProfileRef string `json:"engineProfileRef,omitempty"`

	// EngineConfig provides inline engine configuration that overrides the profile.
	EngineConfig *EngineConfig `json:"engineConfig,omitempty"`

	// Scaling configures HPA-based autoscaling.
	Scaling *ScalingSpec `json:"scaling,omitempty"`
}

// PDInferenceServiceStatus defines the observed state of PDInferenceService.
type PDInferenceServiceStatus struct {
	// Phase is the current lifecycle phase.
	Phase Phase `json:"phase,omitempty"`

	// Endpoint is the address where inference requests can be sent.
	Endpoint string `json:"endpoint,omitempty"`

	// Conditions holds standard Kubernetes condition objects.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// RoleStatuses reports readiness per role.
	RoleStatuses []RoleStatus `json:"roleStatuses,omitempty"`

	// LastEvent records the most recent notable event.
	LastEvent *EventRecord `json:"lastEvent,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pdis
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".status.endpoint"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// PDInferenceService is the Schema for the pdinferenceservices API.
type PDInferenceService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PDInferenceServiceSpec   `json:"spec,omitempty"`
	Status PDInferenceServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PDInferenceServiceList contains a list of PDInferenceService.
type PDInferenceServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PDInferenceService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PDInferenceService{}, &PDInferenceServiceList{})
}
