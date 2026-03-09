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

// ─── Volume types ─────────────────────────────────────────────────────────────

// HostPathVolume defines a hostPath volume source.
type HostPathVolume struct {
	// Path is the node-local directory path.
	Path string `json:"path"`
	// Type is the hostPath type (e.g. "Directory", "File"). Optional.
	Type string `json:"type,omitempty"`
}

// EmptyDirVolume defines an emptyDir volume source.
type EmptyDirVolume struct {
	// Medium is the storage medium (e.g. "Memory"). Empty string means default (disk).
	Medium string `json:"medium,omitempty"`
	// SizeLimit is the maximum size of the volume (e.g. "20Gi"). Optional.
	SizeLimit string `json:"sizeLimit,omitempty"`
}

// PVCVolume defines a PersistentVolumeClaim volume source.
type PVCVolume struct {
	// ClaimName is the name of the PersistentVolumeClaim.
	ClaimName string `json:"claimName"`
}

// VolumeSpec defines a volume that can be shared across roles.
// Exactly one of hostPath, emptyDir, or pvc must be set.
type VolumeSpec struct {
	// Name is the volume name referenced by volumeMounts.
	Name string `json:"name"`
	// HostPath mounts a host directory.
	HostPath *HostPathVolume `json:"hostPath,omitempty"`
	// EmptyDir creates an ephemeral directory (optionally in-memory).
	EmptyDir *EmptyDirVolume `json:"emptyDir,omitempty"`
	// PVC mounts a PersistentVolumeClaim.
	PVC *PVCVolume `json:"pvc,omitempty"`
}

// VolumeMountSpec references a named volume from spec.volumes and specifies where to mount it.
type VolumeMountSpec struct {
	// Name must match a volume defined in spec.volumes.
	Name string `json:"name"`
	// MountPath is the path inside the container where the volume is mounted.
	MountPath string `json:"mountPath"`
	// ReadOnly mounts the volume as read-only. Defaults to false.
	ReadOnly bool `json:"readOnly,omitempty"`
}

// ─── Resource types ───────────────────────────────────────────────────────────

// RoleResources defines CPU and memory requests and limits for a role.
// Keys follow Kubernetes resource conventions (e.g. "cpu", "memory").
type RoleResources struct {
	// Requests is the minimum resources required.
	Requests map[string]string `json:"requests,omitempty"`
	// Limits is the maximum resources allowed.
	Limits map[string]string `json:"limits,omitempty"`
}

// ─── Health check types ───────────────────────────────────────────────────────

// ProbeSpec defines an HTTP GET health check probe.
type ProbeSpec struct {
	// HTTPPath is the HTTP endpoint to probe. Defaults to "/health".
	HTTPPath string `json:"httpPath,omitempty"`
	// Port is the port to probe. Defaults to 8000.
	Port int32 `json:"port,omitempty"`
	// InitialDelaySeconds is the number of seconds after the container starts before probing begins.
	InitialDelaySeconds int32 `json:"initialDelaySeconds,omitempty"`
	// PeriodSeconds is how often (in seconds) to perform the probe.
	PeriodSeconds int32 `json:"periodSeconds,omitempty"`
	// TimeoutSeconds is the number of seconds after which the probe times out.
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`
	// FailureThreshold is the minimum consecutive failures for the probe to be considered failed.
	FailureThreshold int32 `json:"failureThreshold,omitempty"`
}

// ─── Role spec types ──────────────────────────────────────────────────────────

// RouterRoleSpec defines the complete configuration for the router role.
type RouterRoleSpec struct {
	// Image is the container image for the router.
	// Required when engineProfileRef is not set.
	Image string `json:"image,omitempty"`

	// Replicas is the number of router instances. Defaults to 1.
	// +kubebuilder:validation:Minimum=1
	Replicas int32 `json:"replicas,omitempty"`

	// Resources defines CPU and memory requests and limits for the router container.
	Resources *RoleResources `json:"resources,omitempty"`

	// VolumeMounts lists volume mounts referencing volumes defined in spec.volumes.
	VolumeMounts []VolumeMountSpec `json:"volumeMounts,omitempty"`

	// Command overrides the container entrypoint (corresponds to Kubernetes container.command).
	// Optional. When set together with Args, Command replaces the image ENTRYPOINT.
	Command []string `json:"command,omitempty"`

	// Args are the complete startup arguments passed to the router container.
	// Required (either here or via engineProfileRef).
	Args []string `json:"args,omitempty"`

	// ReadinessProbe configures the readiness health check for the router.
	ReadinessProbe *ProbeSpec `json:"readinessProbe,omitempty"`

	// LivenessProbe configures the liveness health check for the router.
	LivenessProbe *ProbeSpec `json:"livenessProbe,omitempty"`
}

// InferenceRoleSpec defines the complete configuration for a prefill or decode role.
type InferenceRoleSpec struct {
	// Image is the container image for this role.
	// Required when engineProfileRef is not set.
	Image string `json:"image,omitempty"`

	// Replicas is the number of instances for this role.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	Replicas int32 `json:"replicas"`

	// GPU is the number of GPU devices to allocate per pod (e.g. "2").
	GPU string `json:"gpu,omitempty"`

	// GPUType constrains scheduling to nodes with the matching "accelerator" label
	// (e.g. "a30", "h100").
	GPUType string `json:"gpuType,omitempty"`

	// Resources defines CPU and memory requests and limits.
	// GPU resources are specified via the GPU field.
	Resources *RoleResources `json:"resources,omitempty"`

	// VolumeMounts lists volume mounts referencing volumes defined in spec.volumes.
	VolumeMounts []VolumeMountSpec `json:"volumeMounts,omitempty"`

	// Command overrides the container entrypoint (corresponds to Kubernetes container.command).
	// Optional. When set together with Args, Command replaces the image ENTRYPOINT.
	Command []string `json:"command,omitempty"`

	// Args are the complete startup arguments passed to the inference container.
	// pd-manager does not auto-inject engine args; all args must be specified here
	// or supplied by the referenced engineProfileRef.
	// The environment variable $(POD_IP) is available for use in args.
	Args []string `json:"args,omitempty"`

	// ReadinessProbe configures the readiness health check.
	ReadinessProbe *ProbeSpec `json:"readinessProbe,omitempty"`

	// LivenessProbe configures the liveness health check.
	LivenessProbe *ProbeSpec `json:"livenessProbe,omitempty"`

	// EngineRuntimes injects sidecar containers into this role's pods via RBG engineRuntimes.
	// Used for platform integrations such as Patio worker registration.
	EngineRuntimes []EngineRuntime `json:"engineRuntimes,omitempty"`
}

// ─── HPA types ────────────────────────────────────────────────────────────────

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

// ─── Status types ─────────────────────────────────────────────────────────────

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

// ─── Main spec / status ───────────────────────────────────────────────────────

// PDInferenceServiceSpec defines the desired state of PDInferenceService.
type PDInferenceServiceSpec struct {
	// Model is the model identifier (e.g. "Qwen/Qwen3-14B").
	// Used for labeling and as the served-model-name in inference APIs.
	Model string `json:"model"`

	// Engine selects the inference engine. Defaults to sglang.
	// +kubebuilder:validation:Enum=sglang
	// +kubebuilder:default=sglang
	Engine EngineType `json:"engine,omitempty"`

	// Volumes defines shared volumes available to all roles via volumeMounts.
	Volumes []VolumeSpec `json:"volumes,omitempty"`

	// Router configures the router role (sgl-router / sgl-model-gateway).
	Router RouterRoleSpec `json:"router"`

	// Prefill configures the prefill role.
	Prefill InferenceRoleSpec `json:"prefill"`

	// Decode configures the decode role.
	Decode InferenceRoleSpec `json:"decode"`

	// PDRatio is the desired prefill:decode replica ratio (e.g. "1:2").
	// Mutually exclusive with scaling.prefill.
	PDRatio string `json:"pdRatio,omitempty"`

	// EngineProfileRef names a PDEngineProfile in the same namespace
	// to use as the base configuration template (images and per-role args).
	EngineProfileRef string `json:"engineProfileRef,omitempty"`

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
