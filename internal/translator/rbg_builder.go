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

package translator

import (
	"github.com/pd-ai/pd-manager/api/v1alpha1"
	"github.com/pd-ai/pd-manager/internal/config"
	"github.com/pd-ai/pd-manager/internal/translator/sglang"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rbgv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
)

const (
	modelVolumeName = "model-storage"
)

// RBGBuilder translates a PDInferenceService into a RoleBasedGroup.
type RBGBuilder struct {
	argsBuilder *sglang.ArgsBuilder
}

// NewRBGBuilder creates a new RBGBuilder.
func NewRBGBuilder() *RBGBuilder {
	return &RBGBuilder{argsBuilder: &sglang.ArgsBuilder{}}
}

// Build constructs the complete RoleBasedGroup for the given PDInferenceService
// using the pre-merged configuration. The returned RBG has:
//   - Three roles: scheduler (1 replica), prefill (N), decode (M)
//   - hostPath volumes and mounts for model storage
//   - POD_IP injected via Downward API on prefill/decode
//   - ownerReference pointing back to the PDInferenceService
func (b *RBGBuilder) Build(pdis *v1alpha1.PDInferenceService, cfg *config.MergedConfig) (*rbgv1alpha1.RoleBasedGroup, error) {
	schedulerRole := b.buildSchedulerRole(pdis, cfg)
	prefillRole := b.buildInferenceRole("prefill", sglang.RolePrefill, pdis, cfg)
	decodeRole := b.buildInferenceRole("decode", sglang.RoleDecode, pdis, cfg)

	rbg := &rbgv1alpha1.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pdis.Name,
			Namespace: pdis.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         v1alpha1.GroupVersion.String(),
					Kind:               "PDInferenceService",
					Name:               pdis.Name,
					UID:                pdis.UID,
					Controller:         ptr(true),
					BlockOwnerDeletion: ptr(true),
				},
			},
		},
		Spec: rbgv1alpha1.RoleBasedGroupSpec{
			Roles: []rbgv1alpha1.RoleSpec{schedulerRole, prefillRole, decodeRole},
		},
	}

	return rbg, nil
}

// buildSchedulerRole builds the sgl-router scheduler role (always 1 replica).
func (b *RBGBuilder) buildSchedulerRole(pdis *v1alpha1.PDInferenceService, cfg *config.MergedConfig) rbgv1alpha1.RoleSpec {
	args := []string{"--port", "8000"}

	// Inject routing policy from spec.router.strategy
	if pdis.Spec.Router != nil && pdis.Spec.Router.Strategy != "" {
		args = append(args, "--policy", string(pdis.Spec.Router.Strategy))
	}

	// Append scheduler extra args
	args = append(args, cfg.ExtraArgs.Scheduler...)

	return rbgv1alpha1.RoleSpec{
		Name:     "scheduler",
		Replicas: ptr(int32(1)),
		Workload: rbgv1alpha1.WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"},
		TemplateSource: rbgv1alpha1.TemplateSource{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "scheduler",
							Image: cfg.Images.Scheduler,
							Args:  args,
						},
					},
				},
			},
		},
	}
}

// buildInferenceRole builds a prefill or decode role with full SGLang args,
// model volume, Downward API POD_IP, and GPU resources.
func (b *RBGBuilder) buildInferenceRole(
	roleName string,
	roleType sglang.RoleType,
	pdis *v1alpha1.PDInferenceService,
	cfg *config.MergedConfig,
) rbgv1alpha1.RoleSpec {
	// Select spec for this role
	var roleSpec v1alpha1.RoleSpec
	var image string
	if roleType == sglang.RolePrefill {
		roleSpec = pdis.Spec.Prefill
		image = cfg.Images.Prefill
	} else {
		roleSpec = pdis.Spec.Decode
		image = cfg.Images.Decode
	}

	args := b.argsBuilder.BuildArgs(roleType, pdis, cfg)

	// Model volume
	hostPathType := corev1.HostPathDirectory
	volumes := []corev1.Volume{
		{
			Name: modelVolumeName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: pdis.Spec.ModelStorage.HostPath,
					Type: &hostPathType,
				},
			},
		},
	}

	mountPath := "/models"
	if pdis.Spec.ModelStorage.MountPath != "" {
		mountPath = pdis.Spec.ModelStorage.MountPath
	}
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      modelVolumeName,
			MountPath: mountPath,
		},
	}

	// POD_IP via Downward API
	env := []corev1.EnvVar{
		{
			Name: "POD_IP",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
	}

	// GPU resource limits
	gpuCount, _ := resource.ParseQuantity(roleSpec.Resources.GPU)
	resources := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceName("nvidia.com/gpu"): gpuCount,
		},
	}

	// Node selector for GPU type
	var nodeSelector map[string]string
	if roleSpec.Resources.GPUType != "" {
		nodeSelector = map[string]string{
			"accelerator": roleSpec.Resources.GPUType,
		}
	}

	podSpec := corev1.PodSpec{
		Volumes: volumes,
		Containers: []corev1.Container{
			{
				Name:         roleName,
				Image:        image,
				Args:         args,
				Env:          env,
				Resources:    resources,
				VolumeMounts: volumeMounts,
			},
		},
	}
	if nodeSelector != nil {
		podSpec.NodeSelector = nodeSelector
	}

	return rbgv1alpha1.RoleSpec{
		Name:     roleName,
		Replicas: ptr(roleSpec.Replicas),
		Workload: rbgv1alpha1.WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"},
		TemplateSource: rbgv1alpha1.TemplateSource{
			Template: &corev1.PodTemplateSpec{
				Spec: podSpec,
			},
		},
	}
}

func ptr[T any](v T) *T { return &v }
