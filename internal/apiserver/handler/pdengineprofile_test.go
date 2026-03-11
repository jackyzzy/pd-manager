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

package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pdaiv1alpha1 "github.com/pd-ai/pd-manager/api/v1alpha1"
	"github.com/pd-ai/pd-manager/internal/apiserver/handler"
)

func newProfileHandler(objs ...runtime.Object) *handler.Handler {
	s := newScheme()
	b := fake.NewClientBuilder().WithScheme(s)
	for _, o := range objs {
		b = b.WithRuntimeObjects(o)
	}
	return handler.New(b.Build(), s)
}

func makeProfile(name string) *pdaiv1alpha1.PDEngineProfile {
	return &pdaiv1alpha1.PDEngineProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: pdaiv1alpha1.PDEngineProfileSpec{
			Description: "test profile",
			Images: pdaiv1alpha1.RoleImages{
				Router:  "sgl-router:v1",
				Prefill: "sglang:v1",
				Decode:  "sglang:v1",
			},
			RoleArgs: &pdaiv1alpha1.RoleArgs{
				Router:  []string{"--host", "0.0.0.0"},
				Prefill: []string{"--disaggregation-mode", "prefill"},
				Decode:  []string{"--disaggregation-mode", "decode"},
			},
		},
	}
}

func TestCreateProfile_Success(t *testing.T) {
	h := newProfileHandler()
	data, _ := json.Marshal(makeProfile("my-profile"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pd-engine-profiles", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.CreateProfile(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created pdaiv1alpha1.PDEngineProfile
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "my-profile" {
		t.Errorf("expected name my-profile, got %s", created.Name)
	}
}

func TestCreateProfile_InvalidBody(t *testing.T) {
	h := newProfileHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pd-engine-profiles", bytes.NewBufferString("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.CreateProfile(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestListProfiles_Empty(t *testing.T) {
	h := newProfileHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pd-engine-profiles", nil)
	w := httptest.NewRecorder()
	h.ListProfiles(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var result map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&result)
	items, ok := result["items"].([]interface{})
	if !ok || len(items) != 0 {
		t.Errorf("expected items=[], got %v", result["items"])
	}
}

func TestListProfiles_WithItems(t *testing.T) {
	p1 := makeProfile("profile1")
	p2 := makeProfile("profile2")
	h := newProfileHandler(p1, p2)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pd-engine-profiles", nil)
	w := httptest.NewRecorder()
	h.ListProfiles(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var result map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&result)
	items, ok := result["items"].([]interface{})
	if !ok || len(items) != 2 {
		t.Errorf("expected 2 items, got %v", result["items"])
	}
}

func TestGetProfile_Found(t *testing.T) {
	p := makeProfile("my-profile")
	h := newProfileHandler(p)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pd-engine-profiles/my-profile", nil)
	req.SetPathValue("name", "my-profile")
	w := httptest.NewRecorder()
	h.GetProfile(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got pdaiv1alpha1.PDEngineProfile
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.Name != "my-profile" {
		t.Errorf("expected my-profile, got %s", got.Name)
	}
}

func TestGetProfile_NotFound(t *testing.T) {
	h := newProfileHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pd-engine-profiles/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	h.GetProfile(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestUpdateProfile_Success(t *testing.T) {
	p := makeProfile("my-profile")
	h := newProfileHandler(p)

	updated := makeProfile("my-profile")
	updated.Spec.Description = "updated description"
	updated.Spec.Images.Router = "sgl-router:v2"

	data, _ := json.Marshal(updated)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/pd-engine-profiles/my-profile", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("name", "my-profile")
	w := httptest.NewRecorder()
	h.UpdateProfile(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got pdaiv1alpha1.PDEngineProfile
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.Spec.Description != "updated description" {
		t.Errorf("expected updated description, got %q", got.Spec.Description)
	}
	if got.Spec.Images.Router != "sgl-router:v2" {
		t.Errorf("expected sgl-router:v2, got %q", got.Spec.Images.Router)
	}
}

func TestUpdateProfile_NotFound(t *testing.T) {
	h := newProfileHandler()
	data, _ := json.Marshal(makeProfile("nonexistent"))
	req := httptest.NewRequest(http.MethodPut, "/api/v1/pd-engine-profiles/nonexistent", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	h.UpdateProfile(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestDeleteProfile_Success(t *testing.T) {
	p := makeProfile("my-profile")
	h := newProfileHandler(p)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/pd-engine-profiles/my-profile", nil)
	req.SetPathValue("name", "my-profile")
	w := httptest.NewRecorder()
	h.DeleteProfile(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestDeleteProfile_Idempotent(t *testing.T) {
	h := newProfileHandler()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/pd-engine-profiles/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	h.DeleteProfile(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 (idempotent), got %d", w.Code)
	}
}
