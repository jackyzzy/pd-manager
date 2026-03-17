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

package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pdaiv1alpha1 "github.com/pd-ai/pd-manager/api/v1alpha1"
)

// Handler holds the Kubernetes client used to proxy API requests.
type Handler struct {
	client client.Client
	reader client.Reader // direct, non-cached reader for accurate list/get results
	scheme *runtime.Scheme
}

// New creates a Handler backed by the given client and direct reader.
// reader should be mgr.GetAPIReader() to bypass the informer cache and always
// return up-to-date results from the Kubernetes API server.
func New(cl client.Client, reader client.Reader, scheme *runtime.Scheme) *Handler {
	return &Handler{client: cl, reader: reader, scheme: scheme}
}

// Create handles POST /api/v1/pd-inference-services.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var pdis pdaiv1alpha1.PDInferenceService
	if err := json.NewDecoder(r.Body).Decode(&pdis); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if pdis.Namespace == "" {
		pdis.Namespace = "default"
	}
	if err := h.client.Create(context.Background(), &pdis); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(pdis)
}

// List handles GET /api/v1/pd-inference-services.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	list := &pdaiv1alpha1.PDInferenceServiceList{}
	if err := h.reader.List(context.Background(), list, client.InNamespace("default")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"items": list.Items,
	})
}

// Get handles GET /api/v1/pd-inference-services/{name}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pdis := &pdaiv1alpha1.PDInferenceService{}
	if err := h.reader.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, pdis); err != nil {
		if errors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pdis)
}

// updateRequest is the body shape accepted by Update.
// Only prefill.replicas and decode.replicas may be changed.
type updateRequest struct {
	Spec struct {
		Model   string `json:"model,omitempty"`
		Prefill *struct {
			Replicas *int32 `json:"replicas,omitempty"`
		} `json:"prefill,omitempty"`
		Decode *struct {
			Replicas *int32 `json:"replicas,omitempty"`
		} `json:"decode,omitempty"`
	} `json:"spec,omitempty"`
}

// Update handles PUT /api/v1/pd-inference-services/{name}.
// Only prefill.replicas and decode.replicas may be changed; all other fields are immutable.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Reject attempts to change immutable fields.
	if req.Spec.Model != "" {
		http.Error(w, "model is immutable", http.StatusBadRequest)
		return
	}

	pdis := &pdaiv1alpha1.PDInferenceService{}
	if err := h.reader.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, pdis); err != nil {
		if errors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if req.Spec.Prefill != nil && req.Spec.Prefill.Replicas != nil {
		pdis.Spec.Prefill.Replicas = *req.Spec.Prefill.Replicas
	}
	if req.Spec.Decode != nil && req.Spec.Decode.Replicas != nil {
		pdis.Spec.Decode.Replicas = *req.Spec.Decode.Replicas
	}

	if err := h.client.Update(context.Background(), pdis); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pdis)
}

// Delete handles DELETE /api/v1/pd-inference-services/{name}.
// Idempotent: returns 200 even if the resource does not exist.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pdis := &pdaiv1alpha1.PDInferenceService{}
	if err := h.reader.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, pdis); err != nil {
		if errors.IsNotFound(err) {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.client.Delete(context.Background(), pdis); err != nil {
		if errors.IsNotFound(err) {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
