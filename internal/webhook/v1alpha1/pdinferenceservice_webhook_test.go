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

		It("TestDefault_RouterStrategy: should inject router.strategy=round-robin when router is nil", func() {
			obj.Spec.Router = nil
			Expect(defaulter.Default(ctx, obj)).To(Succeed())
			Expect(obj.Spec.Router).NotTo(BeNil())
			Expect(string(obj.Spec.Router.Strategy)).To(Equal("round-robin"))
		})

		It("TestDefault_MountPath: should inject mountPath=/models when empty", func() {
			obj.Spec.ModelStorage.MountPath = ""
			Expect(defaulter.Default(ctx, obj)).To(Succeed())
			Expect(obj.Spec.ModelStorage.MountPath).To(Equal("/models"))
		})
	})

	// --- ValidateCreate Tests ---

	Context("ValidateCreate", func() {
		It("TestValidateCreate_NoProfile_MissingImages: should reject when no profileRef and no images", func() {
			obj.Spec = pdaiv1alpha1.PDInferenceServiceSpec{
				Model: "qwen3",
				ModelStorage: pdaiv1alpha1.ModelStorageSpec{
					Type:     pdaiv1alpha1.StorageTypeHostPath,
					HostPath: "/data",
				},
				Prefill: pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
				Decode:  pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
				// No Images, no EngineProfileRef
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("images"))
		})

		It("TestValidateCreate_InvalidKVBackend: should reject invalid KV backend", func() {
			obj.Spec = pdaiv1alpha1.PDInferenceServiceSpec{
				Model: "qwen3",
				ModelStorage: pdaiv1alpha1.ModelStorageSpec{
					Type:     pdaiv1alpha1.StorageTypeHostPath,
					HostPath: "/data",
				},
				Images: &pdaiv1alpha1.RoleImages{
					Scheduler: "s", Prefill: "p", Decode: "d",
				},
				Prefill: pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
				Decode:  pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
				EngineConfig: &pdaiv1alpha1.EngineConfig{
					KVTransfer: &pdaiv1alpha1.KVTransfer{Backend: "invalid"},
				},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("backend"))
		})

		It("TestValidateCreate_ProfileRef_NotFound: should reject when profileRef does not exist", func() {
			obj.ObjectMeta = metav1.ObjectMeta{
				Name:      "test-svc",
				Namespace: "default",
			}
			obj.Spec = pdaiv1alpha1.PDInferenceServiceSpec{
				Model: "qwen3",
				ModelStorage: pdaiv1alpha1.ModelStorageSpec{
					Type:     pdaiv1alpha1.StorageTypeHostPath,
					HostPath: "/data",
				},
				Prefill:          pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
				Decode:           pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
				EngineProfileRef: "nonexistent-profile",
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
		})

		It("TestValidateCreate_PDRatio_And_PrefillScaling_Conflict: should reject pdRatio + scaling.prefill together", func() {
			obj.Spec = pdaiv1alpha1.PDInferenceServiceSpec{
				Model: "qwen3",
				ModelStorage: pdaiv1alpha1.ModelStorageSpec{
					Type:     pdaiv1alpha1.StorageTypeHostPath,
					HostPath: "/data",
				},
				Images:  &pdaiv1alpha1.RoleImages{Scheduler: "s", Prefill: "p", Decode: "d"},
				Prefill: pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
				Decode:  pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
				PDRatio: "1:2",
				Scaling: &pdaiv1alpha1.ScalingSpec{
					Prefill: &pdaiv1alpha1.HPASpec{MaxReplicas: 4},
				},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
		})

		It("TestValidateCreate_ZeroReplicas: should reject prefill replicas=0", func() {
			obj.Spec = pdaiv1alpha1.PDInferenceServiceSpec{
				Model: "qwen3",
				ModelStorage: pdaiv1alpha1.ModelStorageSpec{
					Type:     pdaiv1alpha1.StorageTypeHostPath,
					HostPath: "/data",
				},
				Images:  &pdaiv1alpha1.RoleImages{Scheduler: "s", Prefill: "p", Decode: "d"},
				Prefill: pdaiv1alpha1.RoleSpec{Replicas: 0, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
				Decode:  pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
		})
	})

	// --- ValidateUpdate Tests ---

	Context("ValidateUpdate", func() {
		It("TestValidateUpdate_ImmutableModel: should reject model field change", func() {
			oldObj.Spec = pdaiv1alpha1.PDInferenceServiceSpec{
				Model: "qwen3-14b",
				ModelStorage: pdaiv1alpha1.ModelStorageSpec{
					Type:     pdaiv1alpha1.StorageTypeHostPath,
					HostPath: "/data",
				},
				Images:  &pdaiv1alpha1.RoleImages{Scheduler: "s", Prefill: "p", Decode: "d"},
				Prefill: pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
				Decode:  pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
			}
			obj.Spec = *oldObj.Spec.DeepCopy()
			obj.Spec.Model = "llama-3-8b" // changed

			_, err := validator.ValidateUpdate(ctx, oldObj, obj)
			Expect(err).To(HaveOccurred())
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("immutable"))
		})

		It("TestValidateUpdate_ReplicasAllowed: should allow updating replicas only", func() {
			oldObj.Spec = pdaiv1alpha1.PDInferenceServiceSpec{
				Model: "qwen3-14b",
				ModelStorage: pdaiv1alpha1.ModelStorageSpec{
					Type:     pdaiv1alpha1.StorageTypeHostPath,
					HostPath: "/data",
				},
				Images:  &pdaiv1alpha1.RoleImages{Scheduler: "s", Prefill: "p", Decode: "d"},
				Prefill: pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
				Decode:  pdaiv1alpha1.RoleSpec{Replicas: 1, Resources: pdaiv1alpha1.ResourceSpec{GPU: "1"}},
			}
			obj.Spec = *oldObj.Spec.DeepCopy()
			obj.Spec.Prefill.Replicas = 3 // only replicas changed

			_, err := validator.ValidateUpdate(ctx, oldObj, obj)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
