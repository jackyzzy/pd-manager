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

package config

import (
	"context"
	"fmt"

	"github.com/pd-ai/pd-manager/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MergedConfig is the result of merging a PDEngineProfile (if any) with the
// inline engineConfig of a PDInferenceService. The priority order is:
//
//	pd-manager auto-inject (not here) > inline engineConfig > PDEngineProfile
//
// ExtraArgs are concatenated: Profile args first, inline args last. This allows
// SGLang to use the last occurrence of a flag as the effective value.
type MergedConfig struct {
	Images             v1alpha1.RoleImages
	TensorParallelSize *int32
	KVTransfer         *v1alpha1.KVTransfer
	ExtraArgs          v1alpha1.RoleExtraArgs
	EngineRuntimes     *v1alpha1.RoleEngineRuntimes
}

// Merger resolves the final engine configuration by merging a PDEngineProfile
// (if referenced) with the inline engineConfig on a PDInferenceService.
type Merger struct {
	client client.Client
}

// NewMerger creates a Merger backed by the given Kubernetes client.
func NewMerger(cl client.Client) *Merger {
	return &Merger{client: cl}
}

// Resolve performs the three-tier merge for the given PDInferenceService:
//  1. Load the referenced PDEngineProfile from pd-system namespace (if any).
//  2. Apply inline structured fields (TensorParallelSize, KVTransfer) on top.
//  3. Merge images (non-empty inline fields override profile fields).
//  4. Concatenate extraArgs (profile first, inline last).
func (m *Merger) Resolve(ctx context.Context, pdis *v1alpha1.PDInferenceService) (*MergedConfig, error) {
	merged := &MergedConfig{}

	// Step 1: Load PDEngineProfile if referenced.
	if pdis.Spec.EngineProfileRef != "" {
		profile := &v1alpha1.PDEngineProfile{}
		if err := m.client.Get(ctx, types.NamespacedName{
			Namespace: "pd-system",
			Name:      pdis.Spec.EngineProfileRef,
		}, profile); err != nil {
			return nil, fmt.Errorf("get profile %q: %w", pdis.Spec.EngineProfileRef, err)
		}
		merged.applyProfile(profile)
	}

	// Step 2: Inline images — partial override (only non-empty fields win).
	if img := pdis.Spec.Images; img != nil {
		if img.Scheduler != "" {
			merged.Images.Scheduler = img.Scheduler
		}
		if img.Prefill != "" {
			merged.Images.Prefill = img.Prefill
		}
		if img.Decode != "" {
			merged.Images.Decode = img.Decode
		}
	}

	// Step 3: Inline structured fields override profile (pointer non-nil = override).
	if ec := pdis.Spec.EngineConfig; ec != nil {
		if ec.TensorParallelSize != nil {
			merged.TensorParallelSize = ec.TensorParallelSize
		}
		if ec.KVTransfer != nil {
			merged.KVTransfer = ec.KVTransfer
		}
		// Step 4: Append inline extraArgs after profile extraArgs.
		if ec.ExtraArgs != nil {
			merged.ExtraArgs.Prefill = append(merged.ExtraArgs.Prefill, ec.ExtraArgs.Prefill...)
			merged.ExtraArgs.Decode = append(merged.ExtraArgs.Decode, ec.ExtraArgs.Decode...)
			merged.ExtraArgs.Scheduler = append(merged.ExtraArgs.Scheduler, ec.ExtraArgs.Scheduler...)
		}
	}

	return merged, nil
}

// applyProfile loads the base configuration from a PDEngineProfile.
func (m *MergedConfig) applyProfile(profile *v1alpha1.PDEngineProfile) {
	m.Images = profile.Spec.Images

	ec := profile.Spec.EngineConfig
	if ec.TensorParallelSize != nil {
		m.TensorParallelSize = ec.TensorParallelSize
	}
	if ec.KVTransfer != nil {
		m.KVTransfer = ec.KVTransfer
	}
	if ec.ExtraArgs != nil {
		m.ExtraArgs.Prefill = append(m.ExtraArgs.Prefill, ec.ExtraArgs.Prefill...)
		m.ExtraArgs.Decode = append(m.ExtraArgs.Decode, ec.ExtraArgs.Decode...)
		m.ExtraArgs.Scheduler = append(m.ExtraArgs.Scheduler, ec.ExtraArgs.Scheduler...)
	}
	m.EngineRuntimes = profile.Spec.EngineRuntimes
}
