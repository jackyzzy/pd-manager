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

package sglang_test

import (
	"slices"
	"testing"

	"github.com/pd-ai/pd-manager/api/v1alpha1"
	"github.com/pd-ai/pd-manager/internal/config"
	"github.com/pd-ai/pd-manager/internal/translator/sglang"
)

func ptr[T any](v T) *T { return &v }

func newPDIS(model, hostPath, mountPath string) *v1alpha1.PDInferenceService {
	return &v1alpha1.PDInferenceService{
		Spec: v1alpha1.PDInferenceServiceSpec{
			Model: model,
			ModelStorage: v1alpha1.ModelStorageSpec{
				Type:      v1alpha1.StorageTypeHostPath,
				HostPath:  hostPath,
				MountPath: mountPath,
			},
		},
	}
}

func newMergedConfig() *config.MergedConfig {
	return &config.MergedConfig{}
}

func containsConsecutive(args []string, key, val string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == val {
			return true
		}
	}
	return false
}

func containsArg(args []string, arg string) bool {
	return slices.Contains(args, arg)
}

// TestBuildArgs_Prefill_AutoInjected verifies that the prefill role receives
// all required auto-injected arguments.
func TestBuildArgs_Prefill_AutoInjected(t *testing.T) {
	pdis := newPDIS("qwen3-14b", "/data/models", "/models")
	cfg := newMergedConfig()

	b := &sglang.ArgsBuilder{}
	args := b.BuildArgs(sglang.RolePrefill, pdis, cfg)

	checks := []struct {
		key string
		val string
	}{
		{"--model-path", "/models"},
		{"--served-model-name", "qwen3-14b"},
		{"--host", "$(POD_IP)"},
		{"--port", "8000"},
		{"--disaggregation-mode", "prefill"},
	}
	for _, c := range checks {
		if !containsConsecutive(args, c.key, c.val) {
			t.Errorf("prefill args missing %s %s; got %v", c.key, c.val, args)
		}
	}
}

// TestBuildArgs_Decode_AutoInjected verifies that the decode role receives
// --disaggregation-mode decode and does NOT receive prefill's disaggregation mode.
func TestBuildArgs_Decode_AutoInjected(t *testing.T) {
	pdis := newPDIS("qwen3-14b", "/data/models", "/models")
	cfg := newMergedConfig()

	b := &sglang.ArgsBuilder{}
	args := b.BuildArgs(sglang.RoleDecode, pdis, cfg)

	if !containsConsecutive(args, "--disaggregation-mode", "decode") {
		t.Errorf("decode args missing --disaggregation-mode decode; got %v", args)
	}
	if containsConsecutive(args, "--disaggregation-mode", "prefill") {
		t.Errorf("decode args should NOT contain --disaggregation-mode prefill; got %v", args)
	}
}

// TestBuildArgs_Scheduler_NoDisaggregation verifies that the scheduler role
// does not receive disaggregation-mode or disaggregation-transfer-backend.
func TestBuildArgs_Scheduler_NoDisaggregation(t *testing.T) {
	pdis := newPDIS("qwen3-14b", "/data/models", "/models")
	cfg := &config.MergedConfig{
		KVTransfer: &v1alpha1.KVTransfer{Backend: v1alpha1.KVBackendNixl},
	}

	b := &sglang.ArgsBuilder{}
	args := b.BuildArgs(sglang.RoleScheduler, pdis, cfg)

	if containsArg(args, "--disaggregation-mode") {
		t.Errorf("scheduler args should NOT contain --disaggregation-mode; got %v", args)
	}
	if containsArg(args, "--disaggregation-transfer-backend") {
		t.Errorf("scheduler args should NOT contain --disaggregation-transfer-backend; got %v", args)
	}
}

// TestBuildArgs_KVTransfer_Backend verifies that when KVTransfer is configured,
// prefill and decode roles receive --disaggregation-transfer-backend <backend>.
func TestBuildArgs_KVTransfer_Backend(t *testing.T) {
	pdis := newPDIS("model", "/data", "/models")
	cfg := &config.MergedConfig{
		KVTransfer: &v1alpha1.KVTransfer{Backend: v1alpha1.KVBackendNixl},
	}

	b := &sglang.ArgsBuilder{}

	for _, role := range []sglang.RoleType{sglang.RolePrefill, sglang.RoleDecode} {
		args := b.BuildArgs(role, pdis, cfg)
		if !containsConsecutive(args, "--disaggregation-transfer-backend", "nixl") {
			t.Errorf("role %s: missing --disaggregation-transfer-backend nixl; got %v", role, args)
		}
	}
}

// TestBuildArgs_ExtraArgs_AppendedAfterAutoInject verifies that user extraArgs
// appear after the auto-injected arguments.
func TestBuildArgs_ExtraArgs_AppendedAfterAutoInject(t *testing.T) {
	pdis := newPDIS("model", "/data", "/models")
	cfg := &config.MergedConfig{
		ExtraArgs: v1alpha1.RoleExtraArgs{
			Prefill: []string{"--mem-fraction-static=0.8"},
		},
	}

	b := &sglang.ArgsBuilder{}
	args := b.BuildArgs(sglang.RolePrefill, pdis, cfg)

	// Find index of --model-path (auto-inject) and --mem-fraction-static (extra)
	autoIdx := -1
	extraIdx := -1
	for i, a := range args {
		if a == "--model-path" {
			autoIdx = i
		}
		if a == "--mem-fraction-static=0.8" {
			extraIdx = i
		}
	}

	if autoIdx == -1 {
		t.Fatal("--model-path not found")
	}
	if extraIdx == -1 {
		t.Fatal("--mem-fraction-static=0.8 not found")
	}
	if extraIdx <= autoIdx {
		t.Errorf("extraArgs should come after auto-inject: autoIdx=%d, extraIdx=%d", autoIdx, extraIdx)
	}
}

// TestBuildArgs_CustomMountPath verifies that a custom modelStorage.MountPath
// is used for --model-path instead of the default.
func TestBuildArgs_CustomMountPath(t *testing.T) {
	pdis := newPDIS("model", "/data", "/data/models")
	cfg := newMergedConfig()

	b := &sglang.ArgsBuilder{}
	args := b.BuildArgs(sglang.RolePrefill, pdis, cfg)

	if !containsConsecutive(args, "--model-path", "/data/models") {
		t.Errorf("expected --model-path /data/models; got %v", args)
	}
}

// TestBuildArgs_DefaultMountPath verifies that when modelStorage.MountPath is empty,
// the default /models is used for --model-path.
func TestBuildArgs_DefaultMountPath(t *testing.T) {
	pdis := newPDIS("model", "/data", "") // empty MountPath
	cfg := newMergedConfig()

	b := &sglang.ArgsBuilder{}
	args := b.BuildArgs(sglang.RolePrefill, pdis, cfg)

	if !containsConsecutive(args, "--model-path", "/models") {
		t.Errorf("expected default --model-path /models; got %v", args)
	}
}
