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

package sglang

import (
	"github.com/pd-ai/pd-manager/api/v1alpha1"
	"github.com/pd-ai/pd-manager/internal/config"
)

// RoleType identifies which inference role is being configured.
type RoleType string

const (
	RolePrefill   RoleType = "prefill"
	RoleDecode    RoleType = "decode"
	RoleScheduler RoleType = "scheduler"
)

// ArgsBuilder constructs the SGLang startup arguments for a specific role.
// It separates pd-manager-owned (auto-injected) args from user-provided extraArgs.
type ArgsBuilder struct{}

// BuildArgs returns the complete list of arguments for the given role.
// Structure:
//  1. pd-manager auto-injected args (highest priority, always first)
//  2. User extraArgs from MergedConfig (Profile + inline, concatenated)
func (b *ArgsBuilder) BuildArgs(
	role RoleType,
	pdis *v1alpha1.PDInferenceService,
	cfg *config.MergedConfig,
) []string {
	var args []string

	// --- Auto-injected args (pd-manager managed, not user-overridable) ---

	mountPath := "/models"
	if pdis.Spec.ModelStorage.MountPath != "" {
		mountPath = pdis.Spec.ModelStorage.MountPath
	}
	args = append(args,
		"--model-path", mountPath,
		"--served-model-name", pdis.Spec.Model,
		"--host", "$(POD_IP)",
		"--port", "8000",
	)

	// Disaggregation mode is only injected for prefill and decode.
	switch role {
	case RolePrefill:
		args = append(args, "--disaggregation-mode", "prefill")
	case RoleDecode:
		args = append(args, "--disaggregation-mode", "decode")
	}

	// KV transfer backend is only relevant for prefill and decode.
	if cfg.KVTransfer != nil && role != RoleScheduler {
		args = append(args, "--disaggregation-transfer-backend", string(cfg.KVTransfer.Backend))
	}

	// --- User extraArgs (transparent passthrough, appended after auto-inject) ---
	switch role {
	case RolePrefill:
		args = append(args, cfg.ExtraArgs.Prefill...)
	case RoleDecode:
		args = append(args, cfg.ExtraArgs.Decode...)
	case RoleScheduler:
		args = append(args, cfg.ExtraArgs.Scheduler...)
	}

	return args
}
