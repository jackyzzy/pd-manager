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

package v1alpha1

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pdaiv1alpha1 "github.com/pd-ai/pd-manager/api/v1alpha1"
)

func makeValidPDIS() *pdaiv1alpha1.PDInferenceService {
	return &pdaiv1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: pdaiv1alpha1.PDInferenceServiceSpec{
			Model: "qwen3-14b",
			Router: pdaiv1alpha1.RouterRoleSpec{
				Image:    "sgl-router:latest",
				Replicas: 1,
				Args:     []string{"--host", "0.0.0.0", "--port", "8000"},
			},
			Prefill: pdaiv1alpha1.InferenceRoleSpec{
				Image:    "sglang:latest",
				Replicas: 1,
				GPU:      "1",
				Args:     []string{"--disaggregation-mode", "prefill"},
			},
			Decode: pdaiv1alpha1.InferenceRoleSpec{
				Image:    "sglang:latest",
				Replicas: 1,
				GPU:      "1",
				Args:     []string{"--disaggregation-mode", "decode"},
			},
		},
	}
}

var _ = Describe("PDInferenceService Webhook", func() {
	var (
		obj       *pdaiv1alpha1.PDInferenceService
		oldObj    *pdaiv1alpha1.PDInferenceService
		validator PDInferenceServiceCustomValidator
		defaulter PDInferenceServiceCustomDefaulter
	)

	BeforeEach(func() {
		obj = &pdaiv1alpha1.PDInferenceService{}
		oldObj = &pdaiv1alpha1.PDInferenceService{}
		validator = PDInferenceServiceCustomValidator{Client: k8sClient}
		defaulter = PDInferenceServiceCustomDefaulter{}
		Expect(validator).NotTo(BeNil())
		Expect(defaulter).NotTo(BeNil())
		Expect(oldObj).NotTo(BeNil())
		Expect(obj).NotTo(BeNil())
	})

	// --- Defaulting Tests ---

	Context("Defaulting Webhook", func() {
		It("TestDefault_Engine: should inject engine=sglang when not set", func() {
			obj.Spec.Engine = ""
			Expect(defaulter.Default(ctx, obj)).To(Succeed())
			Expect(string(obj.Spec.Engine)).To(Equal("sglang"))
		})

		It("TestDefault_RouterReplicas: should inject router.replicas=1 when 0", func() {
			obj.Spec.Router.Replicas = 0
			Expect(defaulter.Default(ctx, obj)).To(Succeed())
			Expect(obj.Spec.Router.Replicas).To(Equal(int32(1)))
		})

		It("TestDefault_RouterReplicas_NoOverwrite: should not overwrite existing replicas", func() {
			obj.Spec.Router.Replicas = 3
			Expect(defaulter.Default(ctx, obj)).To(Succeed())
			Expect(obj.Spec.Router.Replicas).To(Equal(int32(3)))
		})
	})

	// --- ValidateCreate Tests ---

	Context("ValidateCreate", func() {
		It("TestValidateCreate_Valid: should accept a complete valid spec", func() {
			_, err := validator.ValidateCreate(ctx, makeValidPDIS())
			Expect(err).NotTo(HaveOccurred())
		})

		It("TestValidateCreate_MissingModel: should reject when model is empty", func() {
			obj = makeValidPDIS()
			obj.Spec.Model = ""
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("model"))
		})

		It("TestValidateCreate_MissingRouterImage: should reject when router image is missing and no profile", func() {
			obj = makeValidPDIS()
			obj.Spec.Router.Image = ""
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("router"))
		})

		It("TestValidateCreate_MissingRouterArgs: should reject when router args are missing and no profile", func() {
			obj = makeValidPDIS()
			obj.Spec.Router.Args = nil
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("router"))
		})

		It("TestValidateCreate_MissingPrefillArgs: should reject when prefill args are missing and no profile", func() {
			obj = makeValidPDIS()
			obj.Spec.Prefill.Args = nil
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
		})

		It("TestValidateCreate_ZeroReplicas: should reject prefill replicas=0", func() {
			obj = makeValidPDIS()
			obj.Spec.Prefill.Replicas = 0
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("replicas"))
		})

		It("TestValidateCreate_InvalidVolumeMount: should reject volumeMount referencing undefined volume", func() {
			obj = makeValidPDIS()
			obj.Spec.Prefill.VolumeMounts = []pdaiv1alpha1.VolumeMountSpec{
				{Name: "nonexistent-vol", MountPath: "/models"},
			}
			// No volumes defined in spec.volumes, so this mount is invalid
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("nonexistent-vol"))
		})

		It("TestValidateCreate_ValidVolumeMount: should accept volumeMount referencing defined volume", func() {
			obj = makeValidPDIS()
			obj.Spec.Volumes = []pdaiv1alpha1.VolumeSpec{
				{Name: "model-storage", HostPath: &pdaiv1alpha1.HostPathVolume{Path: "/data"}},
			}
			obj.Spec.Prefill.VolumeMounts = []pdaiv1alpha1.VolumeMountSpec{
				{Name: "model-storage", MountPath: "/models"},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("TestValidateCreate_ProfileRef_NotFound: should reject when profileRef does not exist", func() {
			obj = makeValidPDIS()
			obj.Spec.EngineProfileRef = "nonexistent-profile"
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
		})

		It("TestValidateCreate_PDRatio_And_PrefillScaling_Conflict: should reject pdRatio + scaling.prefill together", func() {
			obj = makeValidPDIS()
			obj.Spec.PDRatio = "1:2"
			obj.Spec.Scaling = &pdaiv1alpha1.ScalingSpec{
				Prefill: &pdaiv1alpha1.HPASpec{MaxReplicas: 4},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
		})
	})

	// --- ValidateUpdate Tests ---

	Context("ValidateUpdate", func() {
		It("TestValidateUpdate_ImmutableModel: should reject model field change", func() {
			oldObj = makeValidPDIS()
			obj = makeValidPDIS()
			obj.Spec.Model = "llama-3-8b"

			_, err := validator.ValidateUpdate(ctx, oldObj, obj)
			Expect(err).To(HaveOccurred())
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("immutable"))
		})

		It("TestValidateUpdate_ReplicasAllowed: should allow updating replicas", func() {
			oldObj = makeValidPDIS()
			obj = makeValidPDIS()
			obj.Spec.Prefill.Replicas = 3

			_, err := validator.ValidateUpdate(ctx, oldObj, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("TestValidateUpdate_ImmutableEngine: should reject engine field change", func() {
			oldObj = makeValidPDIS()
			obj = makeValidPDIS()
			obj.Spec.Engine = "vllm"

			_, err := validator.ValidateUpdate(ctx, oldObj, obj)
			Expect(err).To(HaveOccurred())
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("immutable"))
		})

		It("TestValidateUpdate_ImmutableEngineProfileRef: should reject engineProfileRef change", func() {
			oldObj = makeValidPDIS()
			oldObj.Spec.EngineProfileRef = "profile-a"
			obj = makeValidPDIS()
			obj.Spec.EngineProfileRef = "profile-b"

			_, err := validator.ValidateUpdate(ctx, oldObj, obj)
			Expect(err).To(HaveOccurred())
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("immutable"))
		})
	})
})
