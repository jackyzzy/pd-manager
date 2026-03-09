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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	rbgv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
)

// RBGBuilder translates a PDInferenceService into a RoleBasedGroup.
type RBGBuilder struct{}

// NewRBGBuilder creates a new RBGBuilder.
func NewRBGBuilder() *RBGBuilder {
	return &RBGBuilder{}
}

// Build constructs the complete RoleBasedGroup for the given PDInferenceService
// using the pre-merged configuration. The returned RBG has:
//   - Three roles: router (1 replica), prefill (N), decode (M)
//   - Volumes resolved from spec.volumes (hostPath / emptyDir / pvc)
//   - POD_IP injected via Downward API on prefill/decode
//   - ownerReference pointing back to the PDInferenceService
func (b *RBGBuilder) Build(pdis *v1alpha1.PDInferenceService, cfg *config.MergedConfig) (*rbgv1alpha1.RoleBasedGroup, error) {
	routerRole := b.buildRouterRole(pdis, cfg)
	prefillRole := b.buildInferenceRole("prefill", pdis, &pdis.Spec.Prefill, cfg.Images.Prefill, cfg.PrefillArgs, resolveEngineRuntimes(pdis.Spec.Prefill.EngineRuntimes, cfg))
	decodeRole := b.buildInferenceRole("decode", pdis, &pdis.Spec.Decode, cfg.Images.Decode, cfg.DecodeArgs, resolveEngineRuntimes(pdis.Spec.Decode.EngineRuntimes, cfg))

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
			Roles: []rbgv1alpha1.RoleSpec{routerRole, prefillRole, decodeRole},
		},
	}

	return rbg, nil
}

// buildRouterRole builds the router role using args and config directly from the merged config.
// No arguments are auto-injected; all args come from spec.router.args or the engine profile.
func (b *RBGBuilder) buildRouterRole(pdis *v1alpha1.PDInferenceService, cfg *config.MergedConfig) rbgv1alpha1.RoleSpec {
	replicas := pdis.Spec.Router.Replicas
	if replicas < 1 {
		replicas = 1
	}

	volumes := buildVolumes(pdis.Spec.Volumes)
	volumeMounts := buildVolumeMounts(pdis.Spec.Router.VolumeMounts)
	resources := buildResources(pdis.Spec.Router.Resources, "")

	var runtimeEngines []rbgv1alpha1.EngineRuntime
	if cfg.EngineRuntimes != nil {
		runtimeEngines = convertEngineRuntimes(cfg.EngineRuntimes.Router)
	}

	roleSpec := rbgv1alpha1.RoleSpec{
		Name:           "router",
		Replicas:       ptr(replicas),
		Workload:       rbgv1alpha1.WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"},
		EngineRuntimes: runtimeEngines,
		TemplateSource: rbgv1alpha1.TemplateSource{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Volumes: volumes,
					Containers: []corev1.Container{
						{
							Name:            "router",
							Image:           cfg.Images.Router,
							Args:            cfg.RouterArgs,
							VolumeMounts:    volumeMounts,
							Resources:       resources,
							ReadinessProbe:  buildProbe(pdis.Spec.Router.ReadinessProbe),
							LivenessProbe:   buildProbe(pdis.Spec.Router.LivenessProbe),
						},
					},
				},
			},
		},
	}
	return roleSpec
}

// buildInferenceRole builds a prefill or decode role. Args are taken directly from
// the merged config (no auto-injection). POD_IP is injected via Downward API.
func (b *RBGBuilder) buildInferenceRole(
	roleName string,
	pdis *v1alpha1.PDInferenceService,
	roleSpec *v1alpha1.InferenceRoleSpec,
	image string,
	args []string,
	engineRuntimes []rbgv1alpha1.EngineRuntime,
) rbgv1alpha1.RoleSpec {
	volumes := buildVolumes(pdis.Spec.Volumes)
	volumeMounts := buildVolumeMounts(roleSpec.VolumeMounts)
	resources := buildResources(roleSpec.Resources, roleSpec.GPU)

	// POD_IP via Downward API — referenced as $(POD_IP) in user-supplied args.
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

	podSpec := corev1.PodSpec{
		Volumes: volumes,
		Containers: []corev1.Container{
			{
				Name:           roleName,
				Image:          image,
				Args:           args,
				Env:            env,
				Resources:      resources,
				VolumeMounts:   volumeMounts,
				ReadinessProbe: buildProbe(roleSpec.ReadinessProbe),
				LivenessProbe:  buildProbe(roleSpec.LivenessProbe),
			},
		},
	}

	// Node selector for GPU type constraint.
	if roleSpec.GPUType != "" {
		podSpec.NodeSelector = map[string]string{
			"accelerator": roleSpec.GPUType,
		}
	}

	return rbgv1alpha1.RoleSpec{
		Name:           roleName,
		Replicas:       ptr(roleSpec.Replicas),
		Workload:       rbgv1alpha1.WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"},
		EngineRuntimes: engineRuntimes,
		TemplateSource: rbgv1alpha1.TemplateSource{
			Template: &corev1.PodTemplateSpec{
				Spec: podSpec,
			},
		},
	}
}

// buildVolumes converts the top-level spec.volumes into Kubernetes Volume objects.
func buildVolumes(specVolumes []v1alpha1.VolumeSpec) []corev1.Volume {
	volumes := make([]corev1.Volume, 0, len(specVolumes))
	for _, v := range specVolumes {
		vol := corev1.Volume{Name: v.Name}
		switch {
		case v.HostPath != nil:
			hostPathType := corev1.HostPathDirectory
			if v.HostPath.Type != "" {
				hostPathType = corev1.HostPathType(v.HostPath.Type)
			}
			vol.VolumeSource = corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: v.HostPath.Path,
					Type: &hostPathType,
				},
			}
		case v.EmptyDir != nil:
			emptyDir := &corev1.EmptyDirVolumeSource{}
			if v.EmptyDir.Medium != "" {
				emptyDir.Medium = corev1.StorageMedium(v.EmptyDir.Medium)
			}
			if v.EmptyDir.SizeLimit != "" {
				q := resource.MustParse(v.EmptyDir.SizeLimit)
				emptyDir.SizeLimit = &q
			}
			vol.VolumeSource = corev1.VolumeSource{EmptyDir: emptyDir}
		case v.PVC != nil:
			vol.VolumeSource = corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: v.PVC.ClaimName,
				},
			}
		}
		volumes = append(volumes, vol)
	}
	return volumes
}

// buildVolumeMounts converts per-role volume mount specs into Kubernetes VolumeMount objects.
func buildVolumeMounts(mounts []v1alpha1.VolumeMountSpec) []corev1.VolumeMount {
	result := make([]corev1.VolumeMount, 0, len(mounts))
	for _, m := range mounts {
		result = append(result, corev1.VolumeMount{
			Name:      m.Name,
			MountPath: m.MountPath,
			ReadOnly:  m.ReadOnly,
		})
	}
	return result
}

// buildResources constructs a Kubernetes ResourceRequirements from the role's resource spec.
// GPU resources are added to both Requests and Limits as "nvidia.com/gpu".
func buildResources(res *v1alpha1.RoleResources, gpu string) corev1.ResourceRequirements {
	req := corev1.ResourceList{}
	lim := corev1.ResourceList{}

	if res != nil {
		for k, v := range res.Requests {
			req[corev1.ResourceName(k)] = resource.MustParse(v)
		}
		for k, v := range res.Limits {
			lim[corev1.ResourceName(k)] = resource.MustParse(v)
		}
	}

	if gpu != "" {
		gpuQty := resource.MustParse(gpu)
		req[corev1.ResourceName("nvidia.com/gpu")] = gpuQty
		lim[corev1.ResourceName("nvidia.com/gpu")] = gpuQty
	}

	result := corev1.ResourceRequirements{}
	if len(req) > 0 {
		result.Requests = req
	}
	if len(lim) > 0 {
		result.Limits = lim
	}
	return result
}

// buildProbe converts a ProbeSpec into a Kubernetes Probe.
// Returns nil when spec is nil.
func buildProbe(spec *v1alpha1.ProbeSpec) *corev1.Probe {
	if spec == nil {
		return nil
	}
	path := spec.HTTPPath
	if path == "" {
		path = "/health"
	}
	port := spec.Port
	if port == 0 {
		port = 8000
	}
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: path,
				Port: portIntOrString(port),
			},
		},
		InitialDelaySeconds: spec.InitialDelaySeconds,
		PeriodSeconds:       spec.PeriodSeconds,
		TimeoutSeconds:      spec.TimeoutSeconds,
		FailureThreshold:    spec.FailureThreshold,
	}
}

// convertEngineRuntimes maps pd-manager EngineRuntime to RBG EngineRuntime.
func convertEngineRuntimes(runtimes []v1alpha1.EngineRuntime) []rbgv1alpha1.EngineRuntime {
	if len(runtimes) == 0 {
		return nil
	}
	result := make([]rbgv1alpha1.EngineRuntime, 0, len(runtimes))
	for _, rt := range runtimes {
		rbgrt := rbgv1alpha1.EngineRuntime{
			ProfileName: rt.ProfileName,
		}
		for _, c := range rt.Containers {
			rbgrt.Containers = append(rbgrt.Containers, corev1.Container{
				Name: c.Name,
				Args: c.Args,
			})
		}
		result = append(result, rbgrt)
	}
	return result
}

// resolveEngineRuntimes returns the inline role runtimes if non-empty,
// otherwise falls back to the profile runtimes for that role.
func resolveEngineRuntimes(inline []v1alpha1.EngineRuntime, cfg *config.MergedConfig) []rbgv1alpha1.EngineRuntime {
	if len(inline) > 0 {
		return convertEngineRuntimes(inline)
	}
	return nil
}

// portIntOrString returns an IntOrString wrapping the given int32 port.
func portIntOrString(port int32) intstr.IntOrString {
	return intstr.FromInt32(port)
}

func ptr[T any](v T) *T { return &v }
