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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pdaiv1alpha1 "github.com/pd-ai/pd-manager/api/v1alpha1"
)

// ListProfiles handles GET /api/v1/pd-engine-profiles.
func (h *Handler) ListProfiles(w http.ResponseWriter, r *http.Request) {
	list := &pdaiv1alpha1.PDEngineProfileList{}
	if err := h.reader.List(context.Background(), list, client.InNamespace("default")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"items": list.Items,
	})
}

// CreateProfile handles POST /api/v1/pd-engine-profiles.
func (h *Handler) CreateProfile(w http.ResponseWriter, r *http.Request) {
	var profile pdaiv1alpha1.PDEngineProfile
	if err := json.NewDecoder(r.Body).Decode(&profile); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if profile.Namespace == "" {
		profile.Namespace = "default"
	}
	if err := h.client.Create(context.Background(), &profile); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(profile)
}

// GetProfile handles GET /api/v1/pd-engine-profiles/{name}.
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	profile := &pdaiv1alpha1.PDEngineProfile{}
	if err := h.reader.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, profile); err != nil {
		if errors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(profile)
}

// UpdateProfile handles PUT /api/v1/pd-engine-profiles/{name}.
// All spec fields are mutable; the full spec is replaced with the request body.
func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var req pdaiv1alpha1.PDEngineProfile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	profile := &pdaiv1alpha1.PDEngineProfile{}
	if err := h.reader.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, profile); err != nil {
		if errors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Replace spec wholesale — all fields are mutable on a config template.
	profile.Spec = req.Spec

	if err := h.client.Update(context.Background(), profile); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(profile)
}

// DeleteProfile handles DELETE /api/v1/pd-engine-profiles/{name}.
// Idempotent: returns 200 even if the resource does not exist.
func (h *Handler) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	profile := &pdaiv1alpha1.PDEngineProfile{}
	if err := h.reader.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, profile); err != nil {
		if errors.IsNotFound(err) {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.client.Delete(context.Background(), profile); err != nil {
		if errors.IsNotFound(err) {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
