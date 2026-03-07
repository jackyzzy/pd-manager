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

package apiserver_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pdaiv1alpha1 "github.com/pd-ai/pd-manager/api/v1alpha1"
	"github.com/pd-ai/pd-manager/internal/apiserver"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func newTestServer(t *testing.T) (*apiserver.Server, int) {
	t.Helper()
	s := runtime.NewScheme()
	_ = pdaiv1alpha1.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	port := freePort(t)
	srv := apiserver.New(fmt.Sprintf(":%d", port), cl, s)
	return srv, port
}

func TestServer_Routes(t *testing.T) {
	srv, port := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { _ = srv.Start(ctx) }()

	// Wait for server to be ready.
	base := fmt.Sprintf("http://localhost:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Routes with predictable non-404 responses (list=200, healthz=200, delete=200 idempotent).
	definite := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/pd-inference-services"},
		{http.MethodGet, "/healthz"},
		{http.MethodDelete, "/api/v1/pd-inference-services/notexist"},
	}
	for _, rt := range definite {
		req, _ := http.NewRequest(rt.method, base+rt.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("%s %s: request failed: %v", rt.method, rt.path, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got unexpected %d (route may not be registered)", rt.method, rt.path, resp.StatusCode)
		}
	}

	// Routes that operate on named resources: 404 is acceptable (resource not found).
	// 405 would indicate the route is NOT registered for this method.
	resource := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/pd-inference-services/test"},
		{http.MethodPut, "/api/v1/pd-inference-services/test"},
	}
	for _, rt := range resource {
		req, _ := http.NewRequest(rt.method, base+rt.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("%s %s: request failed: %v", rt.method, rt.path, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got 405 (route not registered for this method)", rt.method, rt.path)
		}
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	srv, port := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()

	// Wait for server to start.
	base := fmt.Sprintf("http://localhost:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel() // trigger graceful shutdown

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("server shutdown returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("server did not shut down within 5 seconds")
	}
}

func TestServer_HealthCheck(t *testing.T) {
	srv, port := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { _ = srv.Start(ctx) }()

	base := fmt.Sprintf("http://localhost:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return // pass
			}
			lastErr = fmt.Errorf("expected 200, got %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("healthz check failed: %v", lastErr)
}
