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

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = pdaiv1alpha1.AddToScheme(s)
	return s
}

func newHandler(objs ...runtime.Object) *handler.Handler {
	s := newScheme()
	b := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&pdaiv1alpha1.PDInferenceService{})
	for _, o := range objs {
		b = b.WithRuntimeObjects(o)
	}
	cl := b.Build()
	return handler.New(cl, cl, s)
}

func makePDIS(name string) *pdaiv1alpha1.PDInferenceService {
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
				Args:     []string{"--host", "0.0.0.0"},
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

func postJSON(h *handler.Handler, body interface{}) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pd-inference-services", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Create(w, req)
	return w
}

func TestCreate_Success(t *testing.T) {
	h := newHandler()
	pdis := makePDIS("my-svc")
	w := postJSON(h, pdis)
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created pdaiv1alpha1.PDInferenceService
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "my-svc" {
		t.Errorf("expected name my-svc, got %s", created.Name)
	}
}

func TestCreate_InvalidBody(t *testing.T) {
	h := newHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pd-inference-services", bytes.NewBufferString("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Create(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestList_Empty(t *testing.T) {
	h := newHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pd-inference-services", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
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

func TestList_WithItems(t *testing.T) {
	p1 := makePDIS("svc1")
	p2 := makePDIS("svc2")
	h := newHandler(p1, p2)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pd-inference-services", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
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

func TestGet_Found(t *testing.T) {
	p := makePDIS("my-svc")
	p.Status.Phase = pdaiv1alpha1.PhaseRunning
	h := newHandler(p)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pd-inference-services/my-svc", nil)
	req.SetPathValue("name", "my-svc")
	w := httptest.NewRecorder()
	h.Get(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got pdaiv1alpha1.PDInferenceService
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.Name != "my-svc" {
		t.Errorf("expected my-svc, got %s", got.Name)
	}
}

func TestGet_NotFound(t *testing.T) {
	h := newHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pd-inference-services/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	h.Get(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestUpdate_ReplicasOnly(t *testing.T) {
	p := makePDIS("my-svc")
	h := newHandler(p)

	update := map[string]interface{}{
		"spec": map[string]interface{}{
			"prefill": map[string]interface{}{"replicas": 3},
		},
	}
	data, _ := json.Marshal(update)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/pd-inference-services/my-svc", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("name", "my-svc")
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got pdaiv1alpha1.PDInferenceService
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.Spec.Prefill.Replicas != 3 {
		t.Errorf("expected prefill.replicas=3, got %d", got.Spec.Prefill.Replicas)
	}
}

func TestUpdate_ImmutableField(t *testing.T) {
	p := makePDIS("my-svc")
	h := newHandler(p)

	update := map[string]interface{}{
		"spec": map[string]interface{}{
			"model": "new-model",
		},
	}
	data, _ := json.Marshal(update)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/pd-inference-services/my-svc", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("name", "my-svc")
	w := httptest.NewRecorder()
	h.Update(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDelete_Success(t *testing.T) {
	p := makePDIS("my-svc")
	h := newHandler(p)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/pd-inference-services/my-svc", nil)
	req.SetPathValue("name", "my-svc")
	w := httptest.NewRecorder()
	h.Delete(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestDelete_Idempotent(t *testing.T) {
	h := newHandler() // no objects
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/pd-inference-services/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	h.Delete(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 (idempotent), got %d", w.Code)
	}
}
