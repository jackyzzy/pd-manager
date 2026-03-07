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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rbgv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"

	pdaiv1alpha1 "github.com/pd-ai/pd-manager/api/v1alpha1"
)

func makeRBG(roleStatuses []rbgv1alpha1.RoleStatus, conditions []metav1.Condition) *rbgv1alpha1.RoleBasedGroup {
	return &rbgv1alpha1.RoleBasedGroup{
		Status: rbgv1alpha1.RoleBasedGroupStatus{
			RoleStatuses: roleStatuses,
			Conditions:   conditions,
		},
	}
}

func makePDIS(name string, createdSecondsAgo int) *pdaiv1alpha1.PDInferenceService {
	return &pdaiv1alpha1.PDInferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Duration(createdSecondsAgo) * time.Second)),
		},
	}
}

func TestComputePhase_RBGNotFound(t *testing.T) {
	pdis := makePDIS("test", 0)
	phase := computePhase(nil, pdis)
	if phase != pdaiv1alpha1.PhasePending {
		t.Errorf("expected Pending, got %s", phase)
	}
}

func TestComputePhase_PodsNotReady(t *testing.T) {
	rbg := makeRBG([]rbgv1alpha1.RoleStatus{
		{Name: "prefill", Replicas: 2, ReadyReplicas: 1},
		{Name: "decode", Replicas: 1, ReadyReplicas: 1},
		{Name: "scheduler", Replicas: 1, ReadyReplicas: 1},
	}, nil)
	pdis := makePDIS("test", 60) // 60 seconds ago, well within 30 min
	phase := computePhase(rbg, pdis)
	if phase != pdaiv1alpha1.PhaseInitializing {
		t.Errorf("expected Initializing, got %s", phase)
	}
}

func TestComputePhase_AllReady(t *testing.T) {
	rbg := makeRBG([]rbgv1alpha1.RoleStatus{
		{Name: "scheduler", Replicas: 1, ReadyReplicas: 1},
		{Name: "prefill", Replicas: 2, ReadyReplicas: 2},
		{Name: "decode", Replicas: 2, ReadyReplicas: 2},
	}, nil)
	pdis := makePDIS("test", 120)
	phase := computePhase(rbg, pdis)
	if phase != pdaiv1alpha1.PhaseRunning {
		t.Errorf("expected Running, got %s", phase)
	}
}

func TestComputePhase_CrashLoop(t *testing.T) {
	rbg := makeRBG(
		[]rbgv1alpha1.RoleStatus{{Name: "prefill", Replicas: 1, ReadyReplicas: 0}},
		[]metav1.Condition{{Type: "Ready", Status: "False", Reason: "CrashLoopBackOff"}},
	)
	pdis := makePDIS("test", 60)
	phase := computePhase(rbg, pdis)
	if phase != pdaiv1alpha1.PhaseFailed {
		t.Errorf("expected Failed, got %s", phase)
	}
}

func TestComputePhase_StartupTimeout(t *testing.T) {
	// Not ready, created 31 minutes ago → StartupTimeout → Failed
	rbg := makeRBG([]rbgv1alpha1.RoleStatus{
		{Name: "prefill", Replicas: 1, ReadyReplicas: 0},
	}, nil)
	pdis := makePDIS("test", 31*60) // 31 minutes ago
	phase := computePhase(rbg, pdis)
	if phase != pdaiv1alpha1.PhaseFailed {
		t.Errorf("expected Failed (StartupTimeout), got %s", phase)
	}
}

func TestComputePhase_DeletionTimestamp(t *testing.T) {
	pdis := makePDIS("test", 0)
	now := metav1.Now()
	pdis.DeletionTimestamp = &now
	phase := computePhase(nil, pdis)
	if phase != pdaiv1alpha1.PhaseTerminating {
		t.Errorf("expected Terminating, got %s", phase)
	}
}

func TestBuildRoleStatuses(t *testing.T) {
	rbg := makeRBG([]rbgv1alpha1.RoleStatus{
		{Name: "scheduler", Replicas: 1, ReadyReplicas: 1},
		{Name: "prefill", Replicas: 3, ReadyReplicas: 2},
		{Name: "decode", Replicas: 2, ReadyReplicas: 2},
	}, nil)
	statuses := buildRoleStatuses(rbg)
	if len(statuses) != 3 {
		t.Fatalf("expected 3 role statuses, got %d", len(statuses))
	}
	for _, s := range statuses {
		switch s.Name {
		case "scheduler":
			if s.Ready != 1 || s.Total != 1 {
				t.Errorf("scheduler: got ready=%d total=%d", s.Ready, s.Total)
			}
		case "prefill":
			if s.Ready != 2 || s.Total != 3 {
				t.Errorf("prefill: got ready=%d total=%d", s.Ready, s.Total)
			}
		case "decode":
			if s.Ready != 2 || s.Total != 2 {
				t.Errorf("decode: got ready=%d total=%d", s.Ready, s.Total)
			}
		default:
			t.Errorf("unexpected role %q", s.Name)
		}
	}
}

func TestSetReadyCondition_Running(t *testing.T) {
	pdis := makePDIS("test", 0)
	pdis.Status.Phase = pdaiv1alpha1.PhaseRunning
	setReadyCondition(pdis)

	if len(pdis.Status.Conditions) == 0 {
		t.Fatal("expected at least one condition, got none")
	}
	var found bool
	for _, c := range pdis.Status.Conditions {
		if c.Type == "Ready" {
			found = true
			if c.Status != metav1.ConditionTrue {
				t.Errorf("expected Ready=True for Running phase, got %s", c.Status)
			}
		}
	}
	if !found {
		t.Error("Ready condition not found")
	}
}

func TestSetReadyCondition_Failed(t *testing.T) {
	pdis := makePDIS("test", 0)
	pdis.Status.Phase = pdaiv1alpha1.PhaseFailed
	setReadyCondition(pdis)

	if len(pdis.Status.Conditions) == 0 {
		t.Fatal("expected at least one condition, got none")
	}
	var found bool
	for _, c := range pdis.Status.Conditions {
		if c.Type == "Ready" {
			found = true
			if c.Status != metav1.ConditionFalse {
				t.Errorf("expected Ready=False for Failed phase, got %s", c.Status)
			}
			if c.Reason == "" {
				t.Error("expected non-empty Reason for Failed phase")
			}
		}
	}
	if !found {
		t.Error("Ready condition not found")
	}
}
