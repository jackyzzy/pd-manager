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

package translator_test

import (
	"testing"

	"github.com/pd-ai/pd-manager/api/v1alpha1"
	"github.com/pd-ai/pd-manager/internal/config"
	"github.com/pd-ai/pd-manager/internal/translator"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rbgv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
)

func ptr[T any](v T) *T { return &v }

func makePDIS(name string) *v1alpha1.PDInferenceService {
	return &v1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       "test-uid-1234",
		},
		Spec: v1alpha1.PDInferenceServiceSpec{
			Model: "qwen3-14b",
			Volumes: []v1alpha1.VolumeSpec{
				{
					Name:     "model-storage",
					HostPath: &v1alpha1.HostPathVolume{Path: "/data/models", Type: "Directory"},
				},
				{
					Name:     "dshm",
					EmptyDir: &v1alpha1.EmptyDirVolume{Medium: "Memory", SizeLimit: "20Gi"},
				},
			},
			Router: v1alpha1.RouterRoleSpec{
				Image:    "sgl-router:v0.3.1",
				Replicas: 1,
				Args:     []string{"--pd-disaggregation", "--host", "0.0.0.0", "--port", "8000"},
				VolumeMounts: []v1alpha1.VolumeMountSpec{
					{Name: "model-storage", MountPath: "/models"},
				},
			},
			Prefill: v1alpha1.InferenceRoleSpec{
				Image:    "sglang:v0.5.8",
				Replicas: 2,
				GPU:      "2",
				GPUType:  "a30",
				Args:     []string{"--host", "$(POD_IP)", "--port", "8000", "--disaggregation-mode", "prefill"},
				VolumeMounts: []v1alpha1.VolumeMountSpec{
					{Name: "model-storage", MountPath: "/models"},
					{Name: "dshm", MountPath: "/dev/shm"},
				},
				Resources: &v1alpha1.RoleResources{
					Requests: map[string]string{"cpu": "16", "memory": "96Gi"},
					Limits:   map[string]string{"cpu": "32", "memory": "128Gi"},
				},
			},
			Decode: v1alpha1.InferenceRoleSpec{
				Image:    "sglang:v0.5.8",
				Replicas: 4,
				GPU:      "2",
				GPUType:  "a30",
				Args:     []string{"--host", "$(POD_IP)", "--port", "8000", "--disaggregation-mode", "decode"},
				VolumeMounts: []v1alpha1.VolumeMountSpec{
					{Name: "model-storage", MountPath: "/models"},
					{Name: "dshm", MountPath: "/dev/shm"},
				},
			},
		},
	}
}

func makeMergedConfig() *config.MergedConfig {
	return &config.MergedConfig{
		Images: v1alpha1.RoleImages{
			Router:  "sgl-router:v0.3.1",
			Prefill: "sglang:v0.5.8",
			Decode:  "sglang:v0.5.8",
		},
		RouterArgs:  []string{"--pd-disaggregation", "--host", "0.0.0.0", "--port", "8000"},
		PrefillArgs: []string{"--host", "$(POD_IP)", "--port", "8000", "--disaggregation-mode", "prefill"},
		DecodeArgs:  []string{"--host", "$(POD_IP)", "--port", "8000", "--disaggregation-mode", "decode"},
	}
}

func findRole(rbg *rbgv1alpha1.RoleBasedGroup, name string) *rbgv1alpha1.RoleSpec {
	for i := range rbg.Spec.Roles {
		if rbg.Spec.Roles[i].Name == name {
			return &rbg.Spec.Roles[i]
		}
	}
	return nil
}

// argsContains checks whether args contains the given flag.
func argsContains(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// TestBuild_ThreeRolesCreated verifies that the resulting RBG has exactly
// three roles: router, prefill, and decode.
func TestBuild_ThreeRolesCreated(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if len(rbg.Spec.Roles) != 3 {
		t.Errorf("expected 3 roles, got %d", len(rbg.Spec.Roles))
	}

	for _, expected := range []string{"router", "prefill", "decode"} {
		if findRole(rbg, expected) == nil {
			t.Errorf("role %q not found in RBG", expected)
		}
	}
}

// TestBuild_RouterRole_Image verifies the router role uses the correct image and replicas.
func TestBuild_RouterRole_Image(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	router := findRole(rbg, "router")
	if router == nil {
		t.Fatal("router role not found")
	}
	if router.Replicas == nil || *router.Replicas != 1 {
		t.Errorf("router should have replicas=1, got %v", router.Replicas)
	}
	if router.Template == nil || len(router.Template.Spec.Containers) == 0 {
		t.Fatal("router has no containers")
	}
	c := router.Template.Spec.Containers[0]
	if c.Image != "sgl-router:v0.3.1" {
		t.Errorf("router image mismatch: %v", c.Image)
	}
}

// TestBuild_RouterRole_Args verifies that router args are passed through directly.
func TestBuild_RouterRole_Args(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	router := findRole(rbg, "router")
	if router == nil || router.Template == nil || len(router.Template.Spec.Containers) == 0 {
		t.Fatal("router missing")
	}
	args := router.Template.Spec.Containers[0].Args
	if !argsContains(args, "--pd-disaggregation") {
		t.Errorf("router args should contain --pd-disaggregation; got %v", args)
	}
	if !argsContains(args, "--host") {
		t.Errorf("router args should contain --host; got %v", args)
	}
}

// TestBuild_RouterRole_Volumes verifies router pod has volumes from spec.volumes
// and the container has the correct volumeMounts.
func TestBuild_RouterRole_Volumes(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	router := findRole(rbg, "router")
	if router == nil || router.Template == nil {
		t.Fatal("router template is nil")
	}

	// Should have 2 volumes (model-storage hostPath + dshm emptyDir) from spec.volumes
	if len(router.Template.Spec.Volumes) != 2 {
		t.Errorf("expected 2 volumes in router pod, got %d", len(router.Template.Spec.Volumes))
	}

	foundVol := false
	for _, vol := range router.Template.Spec.Volumes {
		if vol.HostPath != nil && vol.HostPath.Path == "/data/models" {
			foundVol = true
			break
		}
	}
	if !foundVol {
		t.Errorf("router should have hostPath volume /data/models; got %v", router.Template.Spec.Volumes)
	}

	foundMount := false
	for _, vm := range router.Template.Spec.Containers[0].VolumeMounts {
		if vm.MountPath == "/models" {
			foundMount = true
			break
		}
	}
	if !foundMount {
		t.Errorf("router container should have volumeMount /models; got %v", router.Template.Spec.Containers[0].VolumeMounts)
	}
}

// TestBuild_PrefillRole_ReplicasAndImage verifies the prefill role replica count and image.
func TestBuild_PrefillRole_ReplicasAndImage(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	prefill := findRole(rbg, "prefill")
	if prefill == nil {
		t.Fatal("prefill role not found")
	}
	if prefill.Replicas == nil || *prefill.Replicas != 2 {
		t.Errorf("prefill replicas mismatch: %v", prefill.Replicas)
	}
	if prefill.Template == nil || len(prefill.Template.Spec.Containers) == 0 {
		t.Fatal("prefill has no containers")
	}
	c := prefill.Template.Spec.Containers[0]
	if c.Image != "sglang:v0.5.8" {
		t.Errorf("prefill image mismatch: %v", c.Image)
	}
}

// TestBuild_DecodeRole_Replicas verifies the decode role replica count.
func TestBuild_DecodeRole_Replicas(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	decode := findRole(rbg, "decode")
	if decode == nil {
		t.Fatal("decode role not found")
	}
	if decode.Replicas == nil || *decode.Replicas != 4 {
		t.Errorf("decode replicas mismatch: %v", decode.Replicas)
	}
}

// TestBuild_InferenceRole_GPU verifies that prefill/decode pods have nvidia.com/gpu resource
// in both requests and limits.
func TestBuild_InferenceRole_GPU(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	for _, roleName := range []string{"prefill", "decode"} {
		role := findRole(rbg, roleName)
		if role == nil || role.Template == nil || len(role.Template.Spec.Containers) == 0 {
			t.Fatalf("%s: no containers", roleName)
		}
		c := role.Template.Spec.Containers[0]
		gpuLimit := c.Resources.Limits[corev1.ResourceName("nvidia.com/gpu")]
		if gpuLimit.IsZero() {
			t.Errorf("%s: should have nvidia.com/gpu resource limit", roleName)
		}
		gpuReq := c.Resources.Requests[corev1.ResourceName("nvidia.com/gpu")]
		if gpuReq.IsZero() {
			t.Errorf("%s: should have nvidia.com/gpu resource request", roleName)
		}
	}
}

// TestBuild_InferenceRole_Resources verifies that cpu/memory requests and limits
// from spec.prefill.resources are set on the container.
func TestBuild_InferenceRole_Resources(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	prefill := findRole(rbg, "prefill")
	if prefill == nil || prefill.Template == nil || len(prefill.Template.Spec.Containers) == 0 {
		t.Fatal("prefill missing")
	}
	c := prefill.Template.Spec.Containers[0]
	if c.Resources.Requests.Cpu().IsZero() {
		t.Error("prefill: cpu request should be set")
	}
	if c.Resources.Requests.Memory().IsZero() {
		t.Error("prefill: memory request should be set")
	}
	if c.Resources.Limits.Cpu().IsZero() {
		t.Error("prefill: cpu limit should be set")
	}
	if c.Resources.Limits.Memory().IsZero() {
		t.Error("prefill: memory limit should be set")
	}
}

// TestBuild_GPUNodeSelector verifies that when gpuType is set,
// the role's pod spec includes a nodeSelector for the GPU type.
func TestBuild_GPUNodeSelector(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	for _, roleName := range []string{"prefill", "decode"} {
		role := findRole(rbg, roleName)
		if role == nil || role.Template == nil {
			t.Fatalf("%s: role or template nil", roleName)
		}
		ns := role.Template.Spec.NodeSelector
		found := false
		for _, v := range ns {
			if v == "a30" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: nodeSelector for a30 not found; got %v", roleName, ns)
		}
	}
}

// TestBuild_DownwardAPI_PodIP verifies that prefill and decode containers
// have a POD_IP environment variable sourced from the Downward API.
func TestBuild_DownwardAPI_PodIP(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	for _, roleName := range []string{"prefill", "decode"} {
		role := findRole(rbg, roleName)
		if role == nil || role.Template == nil || len(role.Template.Spec.Containers) == 0 {
			t.Fatalf("%s: no containers", roleName)
		}
		c := role.Template.Spec.Containers[0]
		foundPodIP := false
		for _, env := range c.Env {
			if env.Name == "POD_IP" &&
				env.ValueFrom != nil &&
				env.ValueFrom.FieldRef != nil &&
				env.ValueFrom.FieldRef.FieldPath == "status.podIP" {
				foundPodIP = true
				break
			}
		}
		if !foundPodIP {
			t.Errorf("%s: POD_IP env from Downward API not found", roleName)
		}
	}
}

// TestBuild_InferenceRole_Volumes verifies prefill/decode pods have both
// hostPath and emptyDir volumes, and the correct volumeMounts.
func TestBuild_InferenceRole_Volumes(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	for _, roleName := range []string{"prefill", "decode"} {
		role := findRole(rbg, roleName)
		if role == nil || role.Template == nil {
			t.Fatalf("%s: template is nil", roleName)
		}

		foundHostPath := false
		foundEmptyDir := false
		for _, vol := range role.Template.Spec.Volumes {
			if vol.HostPath != nil && vol.HostPath.Path == "/data/models" {
				foundHostPath = true
			}
			if vol.EmptyDir != nil {
				foundEmptyDir = true
			}
		}
		if !foundHostPath {
			t.Errorf("%s: hostPath volume /data/models not found", roleName)
		}
		if !foundEmptyDir {
			t.Errorf("%s: emptyDir volume not found", roleName)
		}

		if len(role.Template.Spec.Containers) == 0 {
			t.Fatalf("%s: no containers", roleName)
		}
		foundModelMount := false
		foundShmMount := false
		for _, vm := range role.Template.Spec.Containers[0].VolumeMounts {
			if vm.MountPath == "/models" {
				foundModelMount = true
			}
			if vm.MountPath == "/dev/shm" {
				foundShmMount = true
			}
		}
		if !foundModelMount {
			t.Errorf("%s: volumeMount /models not found", roleName)
		}
		if !foundShmMount {
			t.Errorf("%s: volumeMount /dev/shm not found", roleName)
		}
	}
}

// TestBuild_InferenceRole_Args verifies args are passed through directly from merged config.
func TestBuild_InferenceRole_Args(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	prefill := findRole(rbg, "prefill")
	if prefill == nil || prefill.Template == nil || len(prefill.Template.Spec.Containers) == 0 {
		t.Fatal("prefill missing")
	}
	args := prefill.Template.Spec.Containers[0].Args
	if !argsContains(args, "--disaggregation-mode") {
		t.Errorf("prefill args should contain --disaggregation-mode; got %v", args)
	}
	if !argsContains(args, "prefill") {
		t.Errorf("prefill args should contain 'prefill'; got %v", args)
	}
}

// TestBuild_OwnerReference verifies that the generated RBG has an ownerReference
// pointing to the PDInferenceService.
func TestBuild_OwnerReference(t *testing.T) {
	pdis := makePDIS("svc1")
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(pdis, makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if len(rbg.OwnerReferences) == 0 {
		t.Fatal("RBG should have ownerReferences")
	}
	owner := rbg.OwnerReferences[0]
	if owner.Name != pdis.Name {
		t.Errorf("ownerReference.name mismatch: got %v, want %v", owner.Name, pdis.Name)
	}
	if owner.UID != pdis.UID {
		t.Errorf("ownerReference.uid mismatch")
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Error("ownerReference.controller should be true")
	}
}

// TestBuild_EmptyDirVolume verifies that an emptyDir volume is correctly translated
// with Memory medium and SizeLimit.
func TestBuild_EmptyDirVolume(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	prefill := findRole(rbg, "prefill")
	if prefill == nil || prefill.Template == nil {
		t.Fatal("prefill template is nil")
	}

	for _, vol := range prefill.Template.Spec.Volumes {
		if vol.Name == "dshm" {
			if vol.EmptyDir == nil {
				t.Fatal("dshm volume should be emptyDir")
			}
			if vol.EmptyDir.Medium != corev1.StorageMediumMemory {
				t.Errorf("dshm medium should be Memory, got %v", vol.EmptyDir.Medium)
			}
			if vol.EmptyDir.SizeLimit == nil || vol.EmptyDir.SizeLimit.IsZero() {
				t.Error("dshm SizeLimit should be set")
			}
			return
		}
	}
	t.Error("dshm volume not found in prefill pod")
}

// TestBuild_CommandFromMergedConfig verifies that when MergedConfig carries a PrefillCommand,
// the prefill container's Command field is set correctly (not from roleSpec.Command).
func TestBuild_CommandFromMergedConfig(t *testing.T) {
	pdis := makePDIS("svc1")
	// PDIS has no inline command on prefill
	pdis.Spec.Prefill.Command = nil

	cfg := makeMergedConfig()
	cfg.PrefillCommand = []string{"python3", "-m", "sglang.launch_server"}

	b := translator.NewRBGBuilder()
	rbg, err := b.Build(pdis, cfg)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	prefill := findRole(rbg, "prefill")
	if prefill == nil || prefill.Template == nil || len(prefill.Template.Spec.Containers) == 0 {
		t.Fatal("prefill missing")
	}
	cmd := prefill.Template.Spec.Containers[0].Command
	if len(cmd) != 3 || cmd[0] != "python3" || cmd[1] != "-m" || cmd[2] != "sglang.launch_server" {
		t.Errorf("prefill Command should come from MergedConfig, got %v", cmd)
	}
}

// TestBuild_InlineEngineRuntimesOverrideProfile verifies that when both MergedConfig.EngineRuntimes
// (from profile) and pdis.Spec.Prefill.EngineRuntimes (inline) are non-empty,
// the inline runtime wins for the prefill role.
func TestBuild_InlineEngineRuntimesOverrideProfile(t *testing.T) {
	pdis := makePDIS("svc1")
	pdis.Spec.Prefill.EngineRuntimes = []v1alpha1.EngineRuntime{
		{ProfileName: "inline-runtime", Containers: []v1alpha1.RuntimeContainer{{Name: "inline-ct"}}},
	}

	cfg := makeMergedConfig()
	cfg.EngineRuntimes = &v1alpha1.RoleEngineRuntimes{
		Prefill: []v1alpha1.EngineRuntime{
			{ProfileName: "profile-runtime", Containers: []v1alpha1.RuntimeContainer{{Name: "profile-ct"}}},
		},
	}

	b := translator.NewRBGBuilder()
	rbg, err := b.Build(pdis, cfg)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	prefill := findRole(rbg, "prefill")
	if prefill == nil {
		t.Fatal("prefill role not found")
	}
	if len(prefill.EngineRuntimes) != 1 {
		t.Fatalf("expected 1 engine runtime on prefill, got %d", len(prefill.EngineRuntimes))
	}
	if prefill.EngineRuntimes[0].ProfileName != "inline-runtime" {
		t.Errorf("inline engineRuntime should override profile runtime, got %v", prefill.EngineRuntimes[0].ProfileName)
	}
}

// TestBuild_WorkloadSpec verifies each role has the required WorkloadSpec.
func TestBuild_WorkloadSpec(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	for _, role := range rbg.Spec.Roles {
		if role.Workload.APIVersion != "apps/v1" || role.Workload.Kind != "StatefulSet" {
			t.Errorf("role %q: workload should be apps/v1/StatefulSet, got %v/%v",
				role.Name, role.Workload.APIVersion, role.Workload.Kind)
		}
	}
}
