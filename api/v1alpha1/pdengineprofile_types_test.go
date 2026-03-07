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

// TestPDEngineProfileSpec_Images_Required verifies that a PDEngineProfileSpec with
// a non-empty Images field serializes correctly and the images key is present.
func TestPDEngineProfileSpec_Images_Required(t *testing.T) {
	spec := PDEngineProfileSpec{
		Images: RoleImages{
			Scheduler: "sgl-router:v0.3.1",
			Prefill:   "sglang:v0.5.8",
			Decode:    "sglang:v0.5.8",
		},
		EngineConfig: EngineConfig{},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map failed: %v", err)
	}

	if _, ok := m["images"]; !ok {
		t.Error("images field must be present in serialized PDEngineProfileSpec")
	}

	imagesMap, ok := m["images"].(map[string]interface{})
	if !ok {
		t.Fatal("images must be an object")
	}
	if imagesMap["scheduler"] != "sgl-router:v0.3.1" {
		t.Errorf("images.scheduler mismatch: %v", imagesMap["scheduler"])
	}
}

// TestApplicabilitySpec_Optional verifies that a PDEngineProfileSpec without
// Applicability can be serialized without error.
func TestApplicabilitySpec_Optional(t *testing.T) {
	spec := PDEngineProfileSpec{
		Images: RoleImages{
			Scheduler: "sgl-router:latest",
			Prefill:   "sglang:latest",
			Decode:    "sglang:latest",
		},
		EngineConfig: EngineConfig{},
		// Applicability is nil
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal with nil Applicability failed: %v", err)
	}

	var decoded PDEngineProfileSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Applicability != nil {
		t.Error("Applicability should remain nil after round-trip")
	}
}

// TestEngineConfig_ExtraArgs_AllRoles verifies that EngineConfig.ExtraArgs supports
// independent per-role string slices.
func TestEngineConfig_ExtraArgs_AllRoles(t *testing.T) {
	cfg := EngineConfig{
		ExtraArgs: &RoleExtraArgs{
			Prefill:   []string{"--mem-fraction-static=0.8", "--tp-size=2"},
			Decode:    []string{"--decode-log-interval=100"},
			Scheduler: []string{"--policy=cache-aware"},
		},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded EngineConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.ExtraArgs == nil {
		t.Fatal("ExtraArgs should not be nil")
	}
	if len(decoded.ExtraArgs.Prefill) != 2 {
		t.Errorf("Prefill extra args: want 2, got %d", len(decoded.ExtraArgs.Prefill))
	}
	if decoded.ExtraArgs.Prefill[0] != "--mem-fraction-static=0.8" {
		t.Errorf("Prefill[0] mismatch: %v", decoded.ExtraArgs.Prefill[0])
	}
	if len(decoded.ExtraArgs.Decode) != 1 {
		t.Errorf("Decode extra args: want 1, got %d", len(decoded.ExtraArgs.Decode))
	}
	if len(decoded.ExtraArgs.Scheduler) != 1 {
		t.Errorf("Scheduler extra args: want 1, got %d", len(decoded.ExtraArgs.Scheduler))
	}
}

// TestRoleEngineRuntimes_JSON verifies that RoleEngineRuntimes and its nested
// EngineRuntime containers can serialize/deserialize args with special characters.
func TestRoleEngineRuntimes_JSON(t *testing.T) {
	runtimes := RoleEngineRuntimes{
		Prefill: []EngineRuntime{
			{
				ProfileName: "a30-profile",
				Containers: []RuntimeContainer{
					{
						Name: "sglang",
						Args: []string{
							`--kv-transfer-config={"kv_connector":"MooncakeConnector"}`,
							"--tp-size=4",
						},
					},
				},
			},
		},
		Decode: []EngineRuntime{
			{
				ProfileName: "a30-decode-profile",
			},
		},
	}

	data, err := json.Marshal(runtimes)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded RoleEngineRuntimes
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(decoded.Prefill) != 1 {
		t.Fatalf("want 1 prefill runtime, got %d", len(decoded.Prefill))
	}
	if decoded.Prefill[0].ProfileName != "a30-profile" {
		t.Errorf("ProfileName mismatch: %v", decoded.Prefill[0].ProfileName)
	}
	if len(decoded.Prefill[0].Containers) != 1 {
		t.Fatalf("want 1 container, got %d", len(decoded.Prefill[0].Containers))
	}
	if decoded.Prefill[0].Containers[0].Args[0] != `--kv-transfer-config={"kv_connector":"MooncakeConnector"}` {
		t.Errorf("Args[0] mismatch: %v", decoded.Prefill[0].Containers[0].Args[0])
	}
	if len(decoded.Decode) != 1 {
		t.Fatalf("want 1 decode runtime, got %d", len(decoded.Decode))
	}
}
