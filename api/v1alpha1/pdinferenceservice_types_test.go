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
	spec := PDInferenceServiceSpec{
		Model: "qwen3-14b",
		Router: RouterRoleSpec{
			Image:    "sgl-router:latest",
			Replicas: 1,
			Args:     []string{"--host", "0.0.0.0", "--port", "8000"},
		},
		Prefill: InferenceRoleSpec{
			Image:    "sglang:latest",
			Replicas: 1,
			GPU:      "2",
			GPUType:  "a30",
			Args:     []string{"--disaggregation-mode", "prefill"},
		},
		Decode: InferenceRoleSpec{
			Image:    "sglang:latest",
			Replicas: 1,
			GPU:      "2",
			GPUType:  "a30",
			Args:     []string{"--disaggregation-mode", "decode"},
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
	if decoded.Router.Image != "sgl-router:latest" {
		t.Errorf("Router.Image mismatch: got %q", decoded.Router.Image)
	}
	if decoded.Prefill.Replicas != 1 {
		t.Errorf("Prefill.Replicas mismatch: got %d", decoded.Prefill.Replicas)
	}
	if decoded.Decode.Replicas != 1 {
		t.Errorf("Decode.Replicas mismatch: got %d", decoded.Decode.Replicas)
	}
	if decoded.Prefill.GPU != "2" {
		t.Errorf("Prefill.GPU mismatch: got %q", decoded.Prefill.GPU)
	}
	if decoded.Prefill.GPUType != "a30" {
		t.Errorf("Prefill.GPUType mismatch: got %q", decoded.Prefill.GPUType)
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

// TestVolumeSpec_AllTypes verifies that all volume types can be serialized correctly.
func TestVolumeSpec_AllTypes(t *testing.T) {
	spec := PDInferenceServiceSpec{
		Model: "qwen3-14b",
		Volumes: []VolumeSpec{
			{
				Name:     "model-storage",
				HostPath: &HostPathVolume{Path: "/data/models", Type: "Directory"},
			},
			{
				Name:     "dshm",
				EmptyDir: &EmptyDirVolume{Medium: "Memory", SizeLimit: "20Gi"},
			},
			{
				Name: "shared-pvc",
				PVC:  &PVCVolume{ClaimName: "my-pvc"},
			},
		},
		Prefill: InferenceRoleSpec{Replicas: 1},
		Decode:  InferenceRoleSpec{Replicas: 1},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded PDInferenceServiceSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(decoded.Volumes) != 3 {
		t.Fatalf("expected 3 volumes, got %d", len(decoded.Volumes))
	}
	if decoded.Volumes[0].HostPath == nil || decoded.Volumes[0].HostPath.Path != "/data/models" {
		t.Errorf("hostPath volume mismatch")
	}
	if decoded.Volumes[1].EmptyDir == nil || decoded.Volumes[1].EmptyDir.Medium != "Memory" {
		t.Errorf("emptyDir volume mismatch")
	}
	if decoded.Volumes[2].PVC == nil || decoded.Volumes[2].PVC.ClaimName != "my-pvc" {
		t.Errorf("pvc volume mismatch")
	}
}

// TestProbeSpec_JSON verifies ProbeSpec serializes correctly.
func TestProbeSpec_JSON(t *testing.T) {
	probe := ProbeSpec{
		HTTPPath:            "/health",
		Port:                8000,
		InitialDelaySeconds: 30,
		PeriodSeconds:       10,
		FailureThreshold:    5,
	}

	data, err := json.Marshal(probe)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded ProbeSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.HTTPPath != "/health" {
		t.Errorf("HTTPPath mismatch: %v", decoded.HTTPPath)
	}
	if decoded.Port != 8000 {
		t.Errorf("Port mismatch: %v", decoded.Port)
	}
	if decoded.InitialDelaySeconds != 30 {
		t.Errorf("InitialDelaySeconds mismatch: %v", decoded.InitialDelaySeconds)
	}
}
