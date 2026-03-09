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

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	rbgv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"

	pdaiv1alpha1 "github.com/pd-ai/pd-manager/api/v1alpha1"
	"github.com/pd-ai/pd-manager/internal/config"
	"github.com/pd-ai/pd-manager/internal/translator"
)

const (
	testFinalizer = "pdai.io/finalizer"
	timeout       = 20 * time.Second
	interval      = 250 * time.Millisecond
)

func makePDISForController(name string) *pdaiv1alpha1.PDInferenceService {
	return &pdaiv1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
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
				Args:     []string{"--host", "$(POD_IP)", "--port", "8000", "--disaggregation-mode", "prefill"},
			},
			Decode: pdaiv1alpha1.InferenceRoleSpec{
				Image:    "sglang:latest",
				Replicas: 1,
				GPU:      "1",
				Args:     []string{"--host", "$(POD_IP)", "--port", "8000", "--disaggregation-mode", "decode"},
			},
		},
	}
}

var _ = Describe("PDInferenceService Controller", Ordered, func() {
	var mgrCancel context.CancelFunc

	BeforeAll(func() {
		// Register RBG scheme
		Expect(rbgv1alpha1.AddToScheme(k8sClient.Scheme())).To(Succeed())

		// Start a manager with the reconciler — once for all tests in this block
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:  k8sClient.Scheme(),
			Metrics: metricsserver.Options{BindAddress: "0"},
		})
		Expect(err).NotTo(HaveOccurred())

		Expect((&PDInferenceServiceReconciler{
			Client:       mgr.GetClient(),
			Scheme:       mgr.GetScheme(),
			ConfigMerger: config.NewMerger(mgr.GetClient()),
			RBGBuilder:   translator.NewRBGBuilder(),
		}).SetupWithManager(mgr)).To(Succeed())

		mgrCtx, cancel := context.WithCancel(ctx)
		mgrCancel = cancel
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()
	})

	AfterAll(func() {
		if mgrCancel != nil {
			mgrCancel()
		}
	})

	Context("When creating a PDInferenceService", func() {
		It("TestReconcile_Create_RBGCreated: should create an RBG named after the service", func() {
			pdis := makePDISForController("test-svc-create-rbg")
			Expect(k8sClient.Create(ctx, pdis)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, pdis)
			})

			Eventually(func() error {
				rbg := &rbgv1alpha1.RoleBasedGroup{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: "test-svc-create-rbg", Namespace: "default",
				}, rbg)
			}, timeout, interval).Should(Succeed())
		})

		It("TestReconcile_Create_FinalizerAdded: should add finalizer after reconcile", func() {
			pdis := makePDISForController("test-svc-finalizer")
			Expect(k8sClient.Create(ctx, pdis)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, pdis)
			})

			Eventually(func() bool {
				fetched := &pdaiv1alpha1.PDInferenceService{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name: "test-svc-finalizer", Namespace: "default",
				}, fetched); err != nil {
					return false
				}
				for _, f := range fetched.Finalizers {
					if f == testFinalizer {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})

		It("TestReconcile_Update_RBGUpdated: updating prefill replicas should propagate to RBG", func() {
			pdis := makePDISForController("test-svc-update")
			Expect(k8sClient.Create(ctx, pdis)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, pdis)
			})

			// Wait for RBG to be created
			Eventually(func() error {
				rbg := &rbgv1alpha1.RoleBasedGroup{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: "test-svc-update", Namespace: "default",
				}, rbg)
			}, timeout, interval).Should(Succeed())

			// Update prefill replicas
			fetched := &pdaiv1alpha1.PDInferenceService{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "test-svc-update", Namespace: "default",
			}, fetched)).To(Succeed())
			fetched.Spec.Prefill.Replicas = 3
			Expect(k8sClient.Update(ctx, fetched)).To(Succeed())

			// RBG prefill replicas should be updated
			Eventually(func() int32 {
				rbg := &rbgv1alpha1.RoleBasedGroup{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name: "test-svc-update", Namespace: "default",
				}, rbg); err != nil {
					return 0
				}
				for _, r := range rbg.Spec.Roles {
					if r.Name == "prefill" && r.Replicas != nil {
						return *r.Replicas
					}
				}
				return 0
			}, timeout, interval).Should(Equal(int32(3)))
		})

		It("TestReconcile_OwnerReference: generated RBG should have ownerReference to PDInferenceService", func() {
			pdis := makePDISForController("test-svc-ownerref")
			Expect(k8sClient.Create(ctx, pdis)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, pdis)
			})

			var rbg rbgv1alpha1.RoleBasedGroup
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: "test-svc-ownerref", Namespace: "default",
				}, &rbg)
			}, timeout, interval).Should(Succeed())

			Expect(rbg.OwnerReferences).NotTo(BeEmpty())
			Expect(rbg.OwnerReferences[0].Name).To(Equal("test-svc-ownerref"))
		})

		It("TestReconcile_ProfileResolveFailed_StatusFailed: invalid profile ref sets Phase=Failed", func() {
			pdis := makePDISForController("test-svc-profile-fail")
			pdis.Spec.EngineProfileRef = "nonexistent-profile"
			Expect(k8sClient.Create(ctx, pdis)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, pdis)
			})

			Eventually(func() pdaiv1alpha1.Phase {
				fetched := &pdaiv1alpha1.PDInferenceService{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name: "test-svc-profile-fail", Namespace: "default",
				}, fetched); err != nil {
					return ""
				}
				return fetched.Status.Phase
			}, timeout, interval).Should(Equal(pdaiv1alpha1.PhaseFailed))
		})
	})
})
