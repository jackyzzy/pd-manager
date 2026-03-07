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

package config_test

import (
	"context"
	"testing"

	"github.com/pd-ai/pd-manager/api/v1alpha1"
	"github.com/pd-ai/pd-manager/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func ptr[T any](v T) *T { return &v }

// TestMerge_NoProfile verifies that when no engineProfileRef is set,
// MergedConfig comes directly from the inline engineConfig.
func TestMerge_NoProfile(t *testing.T) {
	pdis := &v1alpha1.PDInferenceService{
		Spec: v1alpha1.PDInferenceServiceSpec{
			Model: "qwen3-14b",
			Images: &v1alpha1.RoleImages{
				Scheduler: "sgl-router:latest",
				Prefill:   "sglang:latest",
				Decode:    "sglang:latest",
			},
			EngineConfig: &v1alpha1.EngineConfig{
				TensorParallelSize: ptr(int32(4)),
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	merger := config.NewMerger(cl)
	merged, err := merger.Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if merged.TensorParallelSize == nil || *merged.TensorParallelSize != 4 {
		t.Errorf("TensorParallelSize mismatch: %v", merged.TensorParallelSize)
	}
	if merged.Images.Prefill != "sglang:latest" {
		t.Errorf("Images.Prefill mismatch: %v", merged.Images.Prefill)
	}
}

// TestMerge_ProfileOnly verifies that when only a Profile is set and no inline config,
// MergedConfig equals the Profile configuration.
func TestMerge_ProfileOnly(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "a30-profile",
			Namespace: "pd-system",
		},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{
				Scheduler: "sgl-router:profile",
				Prefill:   "sglang:profile",
				Decode:    "sglang:profile",
			},
			EngineConfig: v1alpha1.EngineConfig{
				TensorParallelSize: ptr(int32(2)),
				KVTransfer:         &v1alpha1.KVTransfer{Backend: v1alpha1.KVBackendMooncake},
			},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		Spec: v1alpha1.PDInferenceServiceSpec{
			Model:            "qwen3-14b",
			EngineProfileRef: "a30-profile",
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merger := config.NewMerger(cl)
	merged, err := merger.Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if merged.TensorParallelSize == nil || *merged.TensorParallelSize != 2 {
		t.Errorf("TensorParallelSize mismatch: %v", merged.TensorParallelSize)
	}
	if merged.KVTransfer == nil || merged.KVTransfer.Backend != v1alpha1.KVBackendMooncake {
		t.Errorf("KVTransfer mismatch: %v", merged.KVTransfer)
	}
	if merged.Images.Prefill != "sglang:profile" {
		t.Errorf("Images.Prefill mismatch: %v", merged.Images.Prefill)
	}
}

// TestMerge_InlineOverridesStructuredFields verifies that inline engineConfig
// TensorParallelSize overrides the Profile value.
func TestMerge_InlineOverridesStructuredFields(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "base-profile", Namespace: "pd-system"},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{Scheduler: "s", Prefill: "p", Decode: "d"},
			EngineConfig: v1alpha1.EngineConfig{
				TensorParallelSize: ptr(int32(2)),
			},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		Spec: v1alpha1.PDInferenceServiceSpec{
			EngineProfileRef: "base-profile",
			EngineConfig: &v1alpha1.EngineConfig{
				TensorParallelSize: ptr(int32(8)), // override
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if merged.TensorParallelSize == nil || *merged.TensorParallelSize != 8 {
		t.Errorf("expected TensorParallelSize=8 (inline wins), got %v", merged.TensorParallelSize)
	}
}

// TestMerge_InlineOverridesKVTransfer verifies that inline kvTransfer.backend
// overrides the Profile's kvTransfer.backend.
func TestMerge_InlineOverridesKVTransfer(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "base-profile", Namespace: "pd-system"},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{Scheduler: "s", Prefill: "p", Decode: "d"},
			EngineConfig: v1alpha1.EngineConfig{
				KVTransfer: &v1alpha1.KVTransfer{Backend: v1alpha1.KVBackendMooncake},
			},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		Spec: v1alpha1.PDInferenceServiceSpec{
			EngineProfileRef: "base-profile",
			EngineConfig: &v1alpha1.EngineConfig{
				KVTransfer: &v1alpha1.KVTransfer{Backend: v1alpha1.KVBackendNixl}, // override
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if merged.KVTransfer == nil || merged.KVTransfer.Backend != v1alpha1.KVBackendNixl {
		t.Errorf("expected KVBackendNixl (inline wins), got %v", merged.KVTransfer)
	}
}

// TestMerge_ExtraArgs_Appended verifies that extraArgs from Profile and inline
// are concatenated in order: Profile first, inline last.
func TestMerge_ExtraArgs_Appended(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "base-profile", Namespace: "pd-system"},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{Scheduler: "s", Prefill: "p", Decode: "d"},
			EngineConfig: v1alpha1.EngineConfig{
				ExtraArgs: &v1alpha1.RoleExtraArgs{
					Prefill: []string{"--from-profile"},
				},
			},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		Spec: v1alpha1.PDInferenceServiceSpec{
			EngineProfileRef: "base-profile",
			EngineConfig: &v1alpha1.EngineConfig{
				ExtraArgs: &v1alpha1.RoleExtraArgs{
					Prefill: []string{"--from-inline"},
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if len(merged.ExtraArgs.Prefill) != 2 {
		t.Fatalf("expected 2 prefill extra args, got %d", len(merged.ExtraArgs.Prefill))
	}
	if merged.ExtraArgs.Prefill[0] != "--from-profile" {
		t.Errorf("first arg should be from profile, got %v", merged.ExtraArgs.Prefill[0])
	}
	if merged.ExtraArgs.Prefill[1] != "--from-inline" {
		t.Errorf("second arg should be from inline, got %v", merged.ExtraArgs.Prefill[1])
	}
}

// TestMerge_InlineImages_PartialOverride verifies that when inline spec.images
// only specifies decode, scheduler and prefill are inherited from the Profile.
func TestMerge_InlineImages_PartialOverride(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "base-profile", Namespace: "pd-system"},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{
				Scheduler: "sgl-router:profile",
				Prefill:   "sglang:profile-prefill",
				Decode:    "sglang:profile-decode",
			},
			EngineConfig: v1alpha1.EngineConfig{},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		Spec: v1alpha1.PDInferenceServiceSpec{
			EngineProfileRef: "base-profile",
			Images: &v1alpha1.RoleImages{
				// Only override decode; scheduler and prefill should come from profile.
				Decode: "sglang:custom-decode",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if merged.Images.Scheduler != "sgl-router:profile" {
		t.Errorf("Scheduler should inherit from profile, got %v", merged.Images.Scheduler)
	}
	if merged.Images.Prefill != "sglang:profile-prefill" {
		t.Errorf("Prefill should inherit from profile, got %v", merged.Images.Prefill)
	}
	if merged.Images.Decode != "sglang:custom-decode" {
		t.Errorf("Decode should be overridden by inline, got %v", merged.Images.Decode)
	}
}

// TestMerge_ExtraArgs_SameKey_InlineLast verifies that when both Profile and inline
// have the same flag key, both are present and inline appears last (SGLang uses last).
func TestMerge_ExtraArgs_SameKey_InlineLast(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "base-profile", Namespace: "pd-system"},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{Scheduler: "s", Prefill: "p", Decode: "d"},
			EngineConfig: v1alpha1.EngineConfig{
				ExtraArgs: &v1alpha1.RoleExtraArgs{
					Prefill: []string{"--mem-fraction-static=0.88"},
				},
			},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		Spec: v1alpha1.PDInferenceServiceSpec{
			EngineProfileRef: "base-profile",
			EngineConfig: &v1alpha1.EngineConfig{
				ExtraArgs: &v1alpha1.RoleExtraArgs{
					Prefill: []string{"--mem-fraction-static=0.95"},
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if len(merged.ExtraArgs.Prefill) != 2 {
		t.Fatalf("expected 2 args (both present), got %d", len(merged.ExtraArgs.Prefill))
	}
	// Profile first, inline last — SGLang uses the last occurrence
	if merged.ExtraArgs.Prefill[0] != "--mem-fraction-static=0.88" {
		t.Errorf("first should be profile value, got %v", merged.ExtraArgs.Prefill[0])
	}
	if merged.ExtraArgs.Prefill[1] != "--mem-fraction-static=0.95" {
		t.Errorf("second should be inline value, got %v", merged.ExtraArgs.Prefill[1])
	}
}
