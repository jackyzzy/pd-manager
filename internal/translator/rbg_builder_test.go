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
			ModelStorage: v1alpha1.ModelStorageSpec{
				Type:      v1alpha1.StorageTypeHostPath,
				HostPath:  "/data/models",
				MountPath: "/models",
			},
			Prefill: v1alpha1.RoleSpec{
				Replicas: 2,
				Resources: v1alpha1.ResourceSpec{GPU: "1"},
			},
			Decode: v1alpha1.RoleSpec{
				Replicas: 4,
				Resources: v1alpha1.ResourceSpec{GPU: "1"},
			},
			Router: &v1alpha1.RouterSpec{Strategy: v1alpha1.RouterStrategyRoundRobin},
		},
	}
}

func makeMergedConfig() *config.MergedConfig {
	return &config.MergedConfig{
		Images: v1alpha1.RoleImages{
			Scheduler: "lmsysorg/sgl-model-gateway:v0.3.1",
			Prefill:   "lmsysorg/sglang:v0.5.8",
			Decode:    "lmsysorg/sglang:v0.5.8",
		},
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

// TestBuild_ThreeRolesCreated verifies that the resulting RBG has exactly
// three roles: scheduler, prefill, and decode.
func TestBuild_ThreeRolesCreated(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if len(rbg.Spec.Roles) != 3 {
		t.Errorf("expected 3 roles, got %d", len(rbg.Spec.Roles))
	}

	for _, expected := range []string{"scheduler", "prefill", "decode"} {
		if findRole(rbg, expected) == nil {
			t.Errorf("role %q not found in RBG", expected)
		}
	}
}

// argsContains checks whether args contains the given flag (possibly with a value).
func argsContains(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// argValue returns the value following flag in args, or "".
func argValue(args []string, flag string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

// TestBuild_SchedulerRole verifies the scheduler role: replicas=1, correct image,
// and includes the --policy flag matching the router strategy.
func TestBuild_SchedulerRole(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	scheduler := findRole(rbg, "scheduler")
	if scheduler == nil {
		t.Fatal("scheduler role not found")
	}
	if scheduler.Replicas == nil || *scheduler.Replicas != 1 {
		t.Errorf("scheduler should have replicas=1, got %v", scheduler.Replicas)
	}
	if scheduler.Template == nil {
		t.Fatal("scheduler template is nil")
	}
	if len(scheduler.Template.Spec.Containers) == 0 {
		t.Fatal("scheduler has no containers")
	}
	c := scheduler.Template.Spec.Containers[0]
	if c.Image != "lmsysorg/sgl-model-gateway:v0.3.1" {
		t.Errorf("scheduler image mismatch: %v", c.Image)
	}

	args := c.Args
	if !argsContains(args, "--policy") {
		t.Errorf("scheduler args should contain --policy; got %v", args)
	}
	if argValue(args, "--policy") != "round-robin" {
		t.Errorf("--policy should be round-robin, got %v", argValue(args, "--policy"))
	}
}

// TestBuild_SchedulerRole_PDDisaggregation verifies scheduler args include
// --pd-disaggregation, --model-path, --host 0.0.0.0.
func TestBuild_SchedulerRole_PDDisaggregation(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	scheduler := findRole(rbg, "scheduler")
	if scheduler == nil || scheduler.Template == nil || len(scheduler.Template.Spec.Containers) == 0 {
		t.Fatal("scheduler missing")
	}
	args := scheduler.Template.Spec.Containers[0].Args

	if !argsContains(args, "--pd-disaggregation") {
		t.Errorf("scheduler args must contain --pd-disaggregation; got %v", args)
	}
	if argValue(args, "--model-path") != "/models" {
		t.Errorf("--model-path should be /models, got %q", argValue(args, "--model-path"))
	}
	if argValue(args, "--host") != "0.0.0.0" {
		t.Errorf("--host should be 0.0.0.0, got %q", argValue(args, "--host"))
	}
}

// TestBuild_SchedulerRole_WorkerURLs verifies that the scheduler args contain
// the correct --prefill and --decode worker URLs for each replica.
// PDInferenceService "svc1" in namespace "default" with 2 prefill and 4 decode replicas
// should produce:
//   --prefill http://svc1-prefill-0.s-svc1-prefill.default.svc.cluster.local:8000
//   --prefill http://svc1-prefill-1.s-svc1-prefill.default.svc.cluster.local:8000
//   --decode  http://svc1-decode-{0..3}.s-svc1-decode.default.svc.cluster.local:8000
func TestBuild_SchedulerRole_WorkerURLs(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	scheduler := findRole(rbg, "scheduler")
	args := scheduler.Template.Spec.Containers[0].Args

	// Collect all --prefill values
	var prefillURLs, decodeURLs []string
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--prefill":
			prefillURLs = append(prefillURLs, args[i+1])
		case "--decode":
			decodeURLs = append(decodeURLs, args[i+1])
		}
	}

	if len(prefillURLs) != 2 {
		t.Errorf("expected 2 prefill URLs (matching Prefill.Replicas=2), got %d: %v", len(prefillURLs), prefillURLs)
	}
	if len(decodeURLs) != 4 {
		t.Errorf("expected 4 decode URLs (matching Decode.Replicas=4), got %d: %v", len(decodeURLs), decodeURLs)
	}

	expected0 := "http://svc1-prefill-0.s-svc1-prefill.default.svc.cluster.local:8000"
	expected1 := "http://svc1-prefill-1.s-svc1-prefill.default.svc.cluster.local:8000"
	if len(prefillURLs) > 0 && prefillURLs[0] != expected0 {
		t.Errorf("prefill URL[0] mismatch:\n  got  %q\n  want %q", prefillURLs[0], expected0)
	}
	if len(prefillURLs) > 1 && prefillURLs[1] != expected1 {
		t.Errorf("prefill URL[1] mismatch:\n  got  %q\n  want %q", prefillURLs[1], expected1)
	}

	expectedDecode0 := "http://svc1-decode-0.s-svc1-decode.default.svc.cluster.local:8000"
	if len(decodeURLs) > 0 && decodeURLs[0] != expectedDecode0 {
		t.Errorf("decode URL[0] mismatch:\n  got  %q\n  want %q", decodeURLs[0], expectedDecode0)
	}
}

// TestBuild_SchedulerRole_ModelVolume verifies the scheduler pod has the model volume mounted.
func TestBuild_SchedulerRole_ModelVolume(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	scheduler := findRole(rbg, "scheduler")
	if scheduler == nil || scheduler.Template == nil {
		t.Fatal("scheduler template is nil")
	}

	foundVol := false
	for _, vol := range scheduler.Template.Spec.Volumes {
		if vol.HostPath != nil && vol.HostPath.Path == "/data/models" {
			foundVol = true
			break
		}
	}
	if !foundVol {
		t.Errorf("scheduler should have hostPath volume /data/models; got %v", scheduler.Template.Spec.Volumes)
	}

	foundMount := false
	for _, vm := range scheduler.Template.Spec.Containers[0].VolumeMounts {
		if vm.MountPath == "/models" {
			foundMount = true
			break
		}
	}
	if !foundMount {
		t.Errorf("scheduler container should have volumeMount /models; got %v", scheduler.Template.Spec.Containers[0].VolumeMounts)
	}
}

// TestBuild_PrefillRole verifies the prefill role has the correct replica count
// and includes GPU resource limits.
func TestBuild_PrefillRole(t *testing.T) {
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
	if c.Image != "lmsysorg/sglang:v0.5.8" {
		t.Errorf("prefill image mismatch: %v", c.Image)
	}

	gpuLimit := c.Resources.Limits[corev1.ResourceName("nvidia.com/gpu")]
	if gpuLimit.IsZero() {
		t.Error("prefill container should have nvidia.com/gpu resource limit")
	}
}

// TestBuild_DecodeRole verifies the decode role replica count matches spec.
func TestBuild_DecodeRole(t *testing.T) {
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

// TestBuild_ModelVolume verifies that prefill and decode roles have a hostPath
// volume and the corresponding volumeMount.
func TestBuild_ModelVolume(t *testing.T) {
	b := translator.NewRBGBuilder()
	rbg, err := b.Build(makePDIS("svc1"), makeMergedConfig())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	for _, roleName := range []string{"prefill", "decode"} {
		role := findRole(rbg, roleName)
		if role == nil {
			t.Fatalf("role %s not found", roleName)
		}
		if role.Template == nil {
			t.Fatalf("%s: template is nil", roleName)
		}
		foundVol := false
		for _, vol := range role.Template.Spec.Volumes {
			if vol.HostPath != nil && vol.HostPath.Path == "/data/models" {
				foundVol = true
				break
			}
		}
		if !foundVol {
			t.Errorf("%s: hostPath volume /data/models not found", roleName)
		}
		if len(role.Template.Spec.Containers) == 0 {
			t.Fatalf("%s: no containers", roleName)
		}
		foundMount := false
		for _, vm := range role.Template.Spec.Containers[0].VolumeMounts {
			if vm.MountPath == "/models" {
				foundMount = true
				break
			}
		}
		if !foundMount {
			t.Errorf("%s: volumeMount /models not found", roleName)
		}
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

// TestBuild_GPUNodeSelector verifies that when resources.GPUType is set,
// the role's pod spec includes a nodeSelector for the GPU type.
func TestBuild_GPUNodeSelector(t *testing.T) {
	pdis := makePDIS("svc1")
	pdis.Spec.Prefill.Resources.GPUType = "A30"
	pdis.Spec.Decode.Resources.GPUType = "A30"

	b := translator.NewRBGBuilder()
	rbg, err := b.Build(pdis, makeMergedConfig())
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
			if v == "A30" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: nodeSelector for A30 not found; got %v", roleName, ns)
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
