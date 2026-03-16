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

// TestMerge_NoProfile verifies that when no engineProfileRef is set,
// MergedConfig comes directly from inline role fields.
func TestMerge_NoProfile(t *testing.T) {
	pdis := &v1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: v1alpha1.PDInferenceServiceSpec{
			Model: "qwen3-14b",
			Router: v1alpha1.RouterRoleSpec{
				Image: "sgl-router:latest",
				Args:  []string{"--host", "0.0.0.0"},
			},
			Prefill: v1alpha1.InferenceRoleSpec{
				Image: "sglang:latest",
				Args:  []string{"--disaggregation-mode", "prefill"},
			},
			Decode: v1alpha1.InferenceRoleSpec{
				Image: "sglang:latest",
				Args:  []string{"--disaggregation-mode", "decode"},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if merged.Images.Router != "sgl-router:latest" {
		t.Errorf("Images.Router mismatch: %v", merged.Images.Router)
	}
	if merged.Images.Prefill != "sglang:latest" {
		t.Errorf("Images.Prefill mismatch: %v", merged.Images.Prefill)
	}
	if len(merged.RouterArgs) != 2 || merged.RouterArgs[0] != "--host" {
		t.Errorf("RouterArgs mismatch: %v", merged.RouterArgs)
	}
	if len(merged.PrefillArgs) != 2 {
		t.Errorf("PrefillArgs mismatch: %v", merged.PrefillArgs)
	}
}

// TestMerge_ProfileOnly verifies that when only a Profile is set and no inline config,
// MergedConfig equals the Profile configuration (images + args).
func TestMerge_ProfileOnly(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "a30-profile", Namespace: "default"},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{
				Router:  "sgl-router:profile",
				Prefill: "sglang:profile",
				Decode:  "sglang:profile",
			},
			RoleArgs: &v1alpha1.RoleArgs{
				Router:  []string{"--from-profile-router"},
				Prefill: []string{"--from-profile-prefill"},
				Decode:  []string{"--from-profile-decode"},
			},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: v1alpha1.PDInferenceServiceSpec{
			Model:            "qwen3-14b",
			EngineProfileRef: "a30-profile",
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if merged.Images.Prefill != "sglang:profile" {
		t.Errorf("Images.Prefill should come from profile, got %v", merged.Images.Prefill)
	}
	if len(merged.RouterArgs) != 1 || merged.RouterArgs[0] != "--from-profile-router" {
		t.Errorf("RouterArgs should come from profile, got %v", merged.RouterArgs)
	}
	if len(merged.PrefillArgs) != 1 || merged.PrefillArgs[0] != "--from-profile-prefill" {
		t.Errorf("PrefillArgs should come from profile, got %v", merged.PrefillArgs)
	}
	if len(merged.DecodeArgs) != 1 || merged.DecodeArgs[0] != "--from-profile-decode" {
		t.Errorf("DecodeArgs should come from profile, got %v", merged.DecodeArgs)
	}
}

// TestMerge_InlineArgsOverrideProfile verifies that non-empty inline args
// completely override profile args (no concatenation).
func TestMerge_InlineArgsOverrideProfile(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "base-profile", Namespace: "default"},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{Router: "s", Prefill: "p", Decode: "d"},
			RoleArgs: &v1alpha1.RoleArgs{
				Prefill: []string{"--from-profile"},
			},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: v1alpha1.PDInferenceServiceSpec{
			EngineProfileRef: "base-profile",
			Prefill: v1alpha1.InferenceRoleSpec{
				Args: []string{"--from-inline"},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if len(merged.PrefillArgs) != 1 || merged.PrefillArgs[0] != "--from-inline" {
		t.Errorf("expected inline args to win completely, got %v", merged.PrefillArgs)
	}
}

// TestMerge_InlineImageOverridesProfile verifies that inline image overrides profile
// for that role while other roles inherit from profile.
func TestMerge_InlineImageOverridesProfile(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "base-profile", Namespace: "default"},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{
				Router:  "sgl-router:profile",
				Prefill: "sglang:profile-prefill",
				Decode:  "sglang:profile-decode",
			},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: v1alpha1.PDInferenceServiceSpec{
			EngineProfileRef: "base-profile",
			Decode:           v1alpha1.InferenceRoleSpec{Image: "sglang:custom-decode"},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if merged.Images.Router != "sgl-router:profile" {
		t.Errorf("Router should inherit from profile, got %v", merged.Images.Router)
	}
	if merged.Images.Prefill != "sglang:profile-prefill" {
		t.Errorf("Prefill should inherit from profile, got %v", merged.Images.Prefill)
	}
	if merged.Images.Decode != "sglang:custom-decode" {
		t.Errorf("Decode should be overridden by inline, got %v", merged.Images.Decode)
	}
}

// TestMerge_CommandFromProfile verifies that when a profile specifies RoleCommands and
// the PDIS has no inline command, MergedConfig gets the command from the profile.
func TestMerge_CommandFromProfile(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cmd-profile", Namespace: "default"},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{Router: "s", Prefill: "p", Decode: "d"},
			RoleCommands: &v1alpha1.RoleCommands{
				Prefill: []string{"python3", "-m", "sglang.launch_server"},
				Decode:  []string{"python3", "-m", "sglang.launch_server"},
			},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: v1alpha1.PDInferenceServiceSpec{
			EngineProfileRef: "cmd-profile",
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if len(merged.PrefillCommand) != 3 || merged.PrefillCommand[0] != "python3" {
		t.Errorf("PrefillCommand should come from profile, got %v", merged.PrefillCommand)
	}
	if len(merged.DecodeCommand) != 3 || merged.DecodeCommand[0] != "python3" {
		t.Errorf("DecodeCommand should come from profile, got %v", merged.DecodeCommand)
	}
	if len(merged.RouterCommand) != 0 {
		t.Errorf("RouterCommand should be empty (not set in profile), got %v", merged.RouterCommand)
	}
}

// TestMerge_InlineCommandOverridesProfile verifies that non-empty inline command
// on the PDIS completely overrides the profile command.
func TestMerge_InlineCommandOverridesProfile(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cmd-profile", Namespace: "default"},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{Router: "s", Prefill: "p", Decode: "d"},
			RoleCommands: &v1alpha1.RoleCommands{
				Prefill: []string{"python3", "-m", "sglang.launch_server"},
			},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: v1alpha1.PDInferenceServiceSpec{
			EngineProfileRef: "cmd-profile",
			Prefill: v1alpha1.InferenceRoleSpec{
				Command: []string{"custom-entrypoint"},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if len(merged.PrefillCommand) != 1 || merged.PrefillCommand[0] != "custom-entrypoint" {
		t.Errorf("expected inline command to win, got %v", merged.PrefillCommand)
	}
}

// TestMerge_ProfileNotFound verifies that referencing a non-existent profile returns an error.
func TestMerge_ProfileNotFound(t *testing.T) {
	pdis := &v1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: v1alpha1.PDInferenceServiceSpec{
			EngineProfileRef: "nonexistent-profile",
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	_, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err == nil {
		t.Error("expected error for non-existent profile, got nil")
	}
}

// TestMerge_EngineRuntimes_FromProfile verifies that EngineRuntimes from the profile
// are propagated to the MergedConfig.
func TestMerge_EngineRuntimes_FromProfile(t *testing.T) {
	profile := &v1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "base-profile", Namespace: "default"},
		Spec: v1alpha1.PDEngineProfileSpec{
			Images: v1alpha1.RoleImages{Router: "s", Prefill: "p", Decode: "d"},
			EngineRuntimes: &v1alpha1.RoleEngineRuntimes{
				Prefill: []v1alpha1.EngineRuntime{{ProfileName: "a30-runtime"}},
			},
		},
	}

	pdis := &v1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: v1alpha1.PDInferenceServiceSpec{
			EngineProfileRef: "base-profile",
		},
	}

	cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(profile).Build()
	merged, err := config.NewMerger(cl).Resolve(context.Background(), pdis)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if merged.EngineRuntimes == nil {
		t.Fatal("EngineRuntimes should not be nil")
	}
	if len(merged.EngineRuntimes.Prefill) != 1 {
		t.Fatalf("expected 1 prefill runtime, got %d", len(merged.EngineRuntimes.Prefill))
	}
	if merged.EngineRuntimes.Prefill[0].ProfileName != "a30-runtime" {
		t.Errorf("runtime ProfileName mismatch: %v", merged.EngineRuntimes.Prefill[0].ProfileName)
	}
}
