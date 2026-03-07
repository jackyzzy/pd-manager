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
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	rbgv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"

	pdaiv1alpha1 "github.com/pd-ai/pd-manager/api/v1alpha1"
	"github.com/pd-ai/pd-manager/internal/config"
	"github.com/pd-ai/pd-manager/internal/translator"
)

const finalizer = "pdai.io/finalizer"

// PDInferenceServiceReconciler reconciles a PDInferenceService object.
type PDInferenceServiceReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	ConfigMerger *config.Merger
	RBGBuilder   *translator.RBGBuilder
}

// +kubebuilder:rbac:groups=pdai.pdai.io,resources=pdinferenceservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=pdai.pdai.io,resources=pdinferenceservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=pdai.pdai.io,resources=pdinferenceservices/finalizers,verbs=update
// +kubebuilder:rbac:groups=workloads.x-k8s.io,resources=rolebasedgroups,verbs=get;list;watch;create;update;patch;delete

// Reconcile implements the main reconciliation loop.
func (r *PDInferenceServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch the PDInferenceService
	var pdis pdaiv1alpha1.PDInferenceService
	if err := r.Get(ctx, req.NamespacedName, &pdis); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. Handle deletion
	if !pdis.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &pdis)
	}

	// 3. Ensure finalizer
	if err := r.ensureFinalizer(ctx, &pdis); err != nil {
		return ctrl.Result{}, err
	}

	// 4. Resolve merged config (Profile + inline)
	mergedConfig, err := r.ConfigMerger.Resolve(ctx, &pdis)
	if err != nil {
		logger.Error(err, "failed to resolve engine config")
		return r.setFailedStatus(ctx, &pdis, "ProfileResolveFailed", err.Error())
	}

	// 5. Build the desired RBG spec
	desiredRBG, err := r.RBGBuilder.Build(&pdis, mergedConfig)
	if err != nil {
		logger.Error(err, "failed to build RBG")
		return r.setFailedStatus(ctx, &pdis, "RBGBuildFailed", err.Error())
	}

	// 6. Apply (create or update) the RBG
	if err := r.applyRBG(ctx, &pdis, desiredRBG); err != nil {
		return ctrl.Result{}, err
	}

	// 7. Sync status
	return r.syncStatus(ctx, &pdis)
}

// ensureFinalizer adds the pd-manager finalizer to the object if not already present.
func (r *PDInferenceServiceReconciler) ensureFinalizer(ctx context.Context, pdis *pdaiv1alpha1.PDInferenceService) error {
	if controllerutil.ContainsFinalizer(pdis, finalizer) {
		return nil
	}
	controllerutil.AddFinalizer(pdis, finalizer)
	return r.Update(ctx, pdis)
}

// handleDeletion removes the finalizer after cleanup.
// ownerReference on the RBG causes cascade deletion automatically.
func (r *PDInferenceServiceReconciler) handleDeletion(ctx context.Context, pdis *pdaiv1alpha1.PDInferenceService) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(pdis, finalizer) {
		return ctrl.Result{}, nil
	}
	controllerutil.RemoveFinalizer(pdis, finalizer)
	if err := r.Update(ctx, pdis); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// applyRBG creates or updates the RoleBasedGroup to match the desired state.
func (r *PDInferenceServiceReconciler) applyRBG(ctx context.Context, pdis *pdaiv1alpha1.PDInferenceService, desired *rbgv1alpha1.RoleBasedGroup) error {
	existing := &rbgv1alpha1.RoleBasedGroup{}
	err := r.Get(ctx, types.NamespacedName{Name: pdis.Name, Namespace: pdis.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("get RBG: %w", err)
	}
	existing.Spec.Roles = desired.Spec.Roles
	return r.Update(ctx, existing)
}

// setFailedStatus updates the PDInferenceService Phase to Failed with a reason.
func (r *PDInferenceServiceReconciler) setFailedStatus(ctx context.Context, pdis *pdaiv1alpha1.PDInferenceService, reason, message string) (ctrl.Result, error) {
	pdis.Status.Phase = pdaiv1alpha1.PhaseFailed
	pdis.Status.LastEvent = &pdaiv1alpha1.EventRecord{Reason: reason, Message: message}
	return ctrl.Result{}, r.Status().Update(ctx, pdis)
}

// SetupWithManager registers the controller with the Manager and watches RBGs.
func (r *PDInferenceServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pdaiv1alpha1.PDInferenceService{}).
		Owns(&rbgv1alpha1.RoleBasedGroup{}).
		Named("pdinferenceservice").
		Complete(r)
}
