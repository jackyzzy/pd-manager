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

package apiserver

import (
	"context"
	"net/http"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/pd-ai/pd-manager/internal/apiserver/handler"
)

// Server is an HTTP server that exposes the pd-manager REST API.
// It implements the controller-runtime manager.Runnable interface.
type Server struct {
	srv *http.Server
}

// New creates a Server that listens on addr and uses the given client to proxy
// requests to the Kubernetes API.
func New(addr string, cl client.Client, scheme *runtime.Scheme) *Server {
	h := handler.New(cl, scheme)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/pd-inference-services", h.List)
	mux.HandleFunc("POST /api/v1/pd-inference-services", h.Create)
	mux.HandleFunc("GET /api/v1/pd-inference-services/{name}", h.Get)
	mux.HandleFunc("PUT /api/v1/pd-inference-services/{name}", h.Update)
	mux.HandleFunc("DELETE /api/v1/pd-inference-services/{name}", h.Delete)

	mux.HandleFunc("GET /api/v1/pd-engine-profiles", h.ListProfiles)
	mux.HandleFunc("POST /api/v1/pd-engine-profiles", h.CreateProfile)
	mux.HandleFunc("GET /api/v1/pd-engine-profiles/{name}", h.GetProfile)
	mux.HandleFunc("PUT /api/v1/pd-engine-profiles/{name}", h.UpdateProfile)
	mux.HandleFunc("DELETE /api/v1/pd-engine-profiles/{name}", h.DeleteProfile)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return &Server{
		srv: &http.Server{Addr: addr, Handler: mux},
	}
}

// Start starts the HTTP server in the foreground and blocks until ctx is cancelled,
// at which point it performs a graceful shutdown. This satisfies the
// controller-runtime manager.Runnable interface.
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return s.srv.Shutdown(context.Background())
	}
}
