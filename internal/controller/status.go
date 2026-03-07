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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	rbgv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"

	pdaiv1alpha1 "github.com/pd-ai/pd-manager/api/v1alpha1"
)

// syncStatus reads the RBG status and aggregates it into the PDInferenceService status.
func (r *PDInferenceServiceReconciler) syncStatus(ctx context.Context, pdis *pdaiv1alpha1.PDInferenceService) (ctrl.Result, error) {
	rbg := &rbgv1alpha1.RoleBasedGroup{}
	err := r.Get(ctx, types.NamespacedName{Name: pdis.Name, Namespace: pdis.Namespace}, rbg)
	if errors.IsNotFound(err) {
		rbg = nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	pdis.Status.Phase = computePhase(rbg, pdis)
	if rbg != nil {
		pdis.Status.RoleStatuses = buildRoleStatuses(rbg)
	}
	setReadyCondition(pdis)

	if err := r.Status().Update(ctx, pdis); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// computePhase derives the lifecycle phase from the RBG status and the PDInferenceService.
func computePhase(rbg *rbgv1alpha1.RoleBasedGroup, pdis *pdaiv1alpha1.PDInferenceService) pdaiv1alpha1.Phase {
	if !pdis.DeletionTimestamp.IsZero() {
		return pdaiv1alpha1.PhaseTerminating
	}
	if rbg == nil {
		return pdaiv1alpha1.PhasePending
	}
	if hasCrashLoop(rbg) {
		return pdaiv1alpha1.PhaseFailed
	}
	if isInitializingTooLong(rbg, pdis) {
		return pdaiv1alpha1.PhaseFailed
	}
	if allRolesReady(rbg) {
		return pdaiv1alpha1.PhaseRunning
	}
	return pdaiv1alpha1.PhaseInitializing
}

// allRolesReady returns true when every role's readyReplicas equals its desired replicas.
func allRolesReady(rbg *rbgv1alpha1.RoleBasedGroup) bool {
	if len(rbg.Status.RoleStatuses) == 0 {
		return false
	}
	for _, rs := range rbg.Status.RoleStatuses {
		if rs.ReadyReplicas < rs.Replicas {
			return false
		}
	}
	return true
}

// hasCrashLoop returns true when any condition indicates a CrashLoopBackOff or OOMKilled.
func hasCrashLoop(rbg *rbgv1alpha1.RoleBasedGroup) bool {
	for _, cond := range rbg.Status.Conditions {
		if cond.Reason == "CrashLoopBackOff" || cond.Reason == "OOMKilled" {
			return true
		}
	}
	return false
}

// isInitializingTooLong returns true when the service has been initializing for > 30 minutes.
func isInitializingTooLong(rbg *rbgv1alpha1.RoleBasedGroup, pdis *pdaiv1alpha1.PDInferenceService) bool {
	if allRolesReady(rbg) {
		return false
	}
	return time.Since(pdis.CreationTimestamp.Time) > 30*time.Minute
}

// buildRoleStatuses maps RBG role statuses to PDInferenceService role statuses.
func buildRoleStatuses(rbg *rbgv1alpha1.RoleBasedGroup) []pdaiv1alpha1.RoleStatus {
	statuses := make([]pdaiv1alpha1.RoleStatus, 0, len(rbg.Status.RoleStatuses))
	for _, rs := range rbg.Status.RoleStatuses {
		statuses = append(statuses, pdaiv1alpha1.RoleStatus{
			Name:  rs.Name,
			Ready: rs.ReadyReplicas,
			Total: rs.Replicas,
		})
	}
	return statuses
}

// setReadyCondition updates the Ready condition on the PDInferenceService based on the current phase.
func setReadyCondition(pdis *pdaiv1alpha1.PDInferenceService) {
	var status metav1.ConditionStatus
	var reason, message string

	switch pdis.Status.Phase {
	case pdaiv1alpha1.PhaseRunning:
		status = metav1.ConditionTrue
		reason = "AllRolesReady"
		message = "All roles are ready"
	case pdaiv1alpha1.PhaseFailed:
		status = metav1.ConditionFalse
		reason = "ServiceFailed"
		message = "Service has entered a failed state"
		if pdis.Status.LastEvent != nil && pdis.Status.LastEvent.Reason != "" {
			reason = pdis.Status.LastEvent.Reason
			message = pdis.Status.LastEvent.Message
		}
	case pdaiv1alpha1.PhaseInitializing:
		status = metav1.ConditionFalse
		reason = "Initializing"
		message = "Service is initializing"
	case pdaiv1alpha1.PhasePending:
		status = metav1.ConditionFalse
		reason = "Pending"
		message = "Service is pending"
	case pdaiv1alpha1.PhaseTerminating:
		status = metav1.ConditionFalse
		reason = "Terminating"
		message = "Service is terminating"
	default:
		status = metav1.ConditionUnknown
		reason = "Unknown"
		message = "Service state is unknown"
	}

	now := metav1.Now()
	newCond := metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	}

	// Update or append the Ready condition.
	for i, c := range pdis.Status.Conditions {
		if c.Type == "Ready" {
			if c.Status != status {
				pdis.Status.Conditions[i] = newCond
			} else {
				// Status unchanged: preserve the existing LastTransitionTime.
				pdis.Status.Conditions[i].Reason = reason
				pdis.Status.Conditions[i].Message = message
			}
			return
		}
	}
	pdis.Status.Conditions = append(pdis.Status.Conditions, newCond)
}
