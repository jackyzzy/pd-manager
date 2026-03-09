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

// MergedConfig holds the resolved configuration after merging a PDEngineProfile
// (if referenced) with the inline per-role configuration of a PDInferenceService.
//
// Priority: inline CR fields > PDEngineProfile defaults.
// For images and args: if the CR role field is non-empty, it takes full precedence
// over the profile (no concatenation – profiles provide defaults, not additions).
type MergedConfig struct {
	// Images holds the resolved container image for each role.
	Images v1alpha1.RoleImages

	// RouterArgs is the resolved startup args for the router role.
	RouterArgs []string
	// PrefillArgs is the resolved startup args for the prefill role.
	PrefillArgs []string
	// DecodeArgs is the resolved startup args for the decode role.
	DecodeArgs []string

	// EngineRuntimes holds per-role sidecar injection config from the profile.
	EngineRuntimes *v1alpha1.RoleEngineRuntimes
}

// Merger resolves the final configuration by merging a PDEngineProfile
// (if referenced) with the inline per-role fields on a PDInferenceService.
type Merger struct {
	client client.Client
}

// NewMerger creates a Merger backed by the given Kubernetes client.
func NewMerger(cl client.Client) *Merger {
	return &Merger{client: cl}
}

// Resolve performs the two-tier merge for the given PDInferenceService:
//  1. Load the referenced PDEngineProfile (if any) as defaults.
//  2. Override with inline per-role fields (non-empty values take precedence).
func (m *Merger) Resolve(ctx context.Context, pdis *v1alpha1.PDInferenceService) (*MergedConfig, error) {
	merged := &MergedConfig{}

	// Step 1: Load PDEngineProfile if referenced.
	if pdis.Spec.EngineProfileRef != "" {
		profile := &v1alpha1.PDEngineProfile{}
		if err := m.client.Get(ctx, types.NamespacedName{
			Namespace: pdis.Namespace,
			Name:      pdis.Spec.EngineProfileRef,
		}, profile); err != nil {
			return nil, fmt.Errorf("get profile %q: %w", pdis.Spec.EngineProfileRef, err)
		}
		merged.applyProfile(profile)
	}

	// Step 2: Inline per-role images override profile images (non-empty wins).
	if pdis.Spec.Router.Image != "" {
		merged.Images.Router = pdis.Spec.Router.Image
	}
	if pdis.Spec.Prefill.Image != "" {
		merged.Images.Prefill = pdis.Spec.Prefill.Image
	}
	if pdis.Spec.Decode.Image != "" {
		merged.Images.Decode = pdis.Spec.Decode.Image
	}

	// Step 3: Inline per-role args override profile args (non-empty wins).
	if len(pdis.Spec.Router.Args) > 0 {
		merged.RouterArgs = pdis.Spec.Router.Args
	}
	if len(pdis.Spec.Prefill.Args) > 0 {
		merged.PrefillArgs = pdis.Spec.Prefill.Args
	}
	if len(pdis.Spec.Decode.Args) > 0 {
		merged.DecodeArgs = pdis.Spec.Decode.Args
	}

	return merged, nil
}

// applyProfile loads the base configuration from a PDEngineProfile.
func (m *MergedConfig) applyProfile(profile *v1alpha1.PDEngineProfile) {
	m.Images = profile.Spec.Images
	if ra := profile.Spec.RoleArgs; ra != nil {
		m.RouterArgs = ra.Router
		m.PrefillArgs = ra.Prefill
		m.DecodeArgs = ra.Decode
	}
	m.EngineRuntimes = profile.Spec.EngineRuntimes
}
