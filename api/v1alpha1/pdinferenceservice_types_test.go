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
	"encoding/json"
	"testing"
)

// TestPDInferenceServiceSpec_RequiredFields verifies that the minimum required fields
// can be set, serialized and deserialized without data loss.
func TestPDInferenceServiceSpec_RequiredFields(t *testing.T) {
	tp := int32(2)
	spec := PDInferenceServiceSpec{
		Model: "qwen3-14b",
		ModelStorage: ModelStorageSpec{
			Type:     StorageTypeHostPath,
			HostPath: "/data/models",
		},
		Images: &RoleImages{
			Scheduler: "sgl-router:latest",
			Prefill:   "sglang:latest",
			Decode:    "sglang:latest",
		},
		Prefill: RoleSpec{
			Replicas: 1,
			Resources: ResourceSpec{
				GPU: "1",
			},
		},
		Decode: RoleSpec{
			Replicas: 1,
			Resources: ResourceSpec{
				GPU: "1",
			},
		},
		EngineConfig: &EngineConfig{
			TensorParallelSize: &tp,
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("failed to marshal spec: %v", err)
	}

	var decoded PDInferenceServiceSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal spec: %v", err)
	}

	if decoded.Model != spec.Model {
		t.Errorf("Model mismatch: got %q, want %q", decoded.Model, spec.Model)
	}
	if decoded.ModelStorage.HostPath != spec.ModelStorage.HostPath {
		t.Errorf("ModelStorage.HostPath mismatch")
	}
	if decoded.Prefill.Replicas != 1 {
		t.Errorf("Prefill.Replicas mismatch: got %d", decoded.Prefill.Replicas)
	}
	if decoded.Decode.Replicas != 1 {
		t.Errorf("Decode.Replicas mismatch: got %d", decoded.Decode.Replicas)
	}
	if decoded.EngineConfig == nil || decoded.EngineConfig.TensorParallelSize == nil {
		t.Fatal("EngineConfig.TensorParallelSize should not be nil")
	}
	if *decoded.EngineConfig.TensorParallelSize != 2 {
		t.Errorf("TensorParallelSize mismatch: got %d", *decoded.EngineConfig.TensorParallelSize)
	}
}

// TestPhase_Constants verifies the Phase enum constants have the correct string values.
func TestPhase_Constants(t *testing.T) {
	tests := []struct {
		phase Phase
		want  string
	}{
		{PhasePending, "Pending"},
		{PhaseInitializing, "Initializing"},
		{PhaseRunning, "Running"},
		{PhaseFailed, "Failed"},
		{PhaseTerminating, "Terminating"},
	}
	for _, tt := range tests {
		if string(tt.phase) != tt.want {
			t.Errorf("Phase constant wrong: got %q, want %q", tt.phase, tt.want)
		}
	}
}

// TestKVBackend_Constants verifies the KVBackend enum constants.
func TestKVBackend_Constants(t *testing.T) {
	tests := []struct {
		backend KVBackend
		want    string
	}{
		{KVBackendMooncake, "mooncake"},
		{KVBackendNixl, "nixl"},
		{KVBackendNccl, "nccl"},
	}
	for _, tt := range tests {
		if string(tt.backend) != tt.want {
			t.Errorf("KVBackend constant wrong: got %q, want %q", tt.backend, tt.want)
		}
	}
}

// TestModelStorageSpec_DefaultMountPath verifies that MountPath with omitempty
// is omitted from JSON when empty, allowing external defaulting logic to fill it.
func TestModelStorageSpec_DefaultMountPath(t *testing.T) {
	spec := ModelStorageSpec{
		Type:     StorageTypeHostPath,
		HostPath: "/data/models",
		// MountPath intentionally omitted
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map failed: %v", err)
	}

	if _, ok := m["mountPath"]; ok {
		t.Error("mountPath should be omitted from JSON when empty (omitempty)")
	}
}

// TestEngineConfig_NilSafe verifies that a PDInferenceServiceSpec with nil EngineConfig
// can be serialized without panics.
func TestEngineConfig_NilSafe(t *testing.T) {
	spec := PDInferenceServiceSpec{
		Model: "qwen3-14b",
		ModelStorage: ModelStorageSpec{
			Type:     StorageTypeHostPath,
			HostPath: "/data/models",
		},
		Prefill: RoleSpec{Replicas: 1, Resources: ResourceSpec{GPU: "1"}},
		Decode:  RoleSpec{Replicas: 1, Resources: ResourceSpec{GPU: "1"}},
		// EngineConfig is nil
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal with nil EngineConfig failed: %v", err)
	}

	var decoded PDInferenceServiceSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.EngineConfig != nil {
		t.Error("EngineConfig should remain nil after round-trip")
	}
}
