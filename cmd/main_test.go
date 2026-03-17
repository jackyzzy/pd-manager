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

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	rbgv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"

	pdaiv1alpha1 "github.com/pd-ai/pd-manager/api/v1alpha1"
	"github.com/pd-ai/pd-manager/internal/apiserver"
	"github.com/pd-ai/pd-manager/internal/config"
	"github.com/pd-ai/pd-manager/internal/controller"
	"github.com/pd-ai/pd-manager/internal/translator"
)

func getEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}

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

// TestMain_SchemeRegistered verifies that both pdai.io/v1alpha1 and the RBG scheme
// are registered in the global scheme variable defined in main.go.
func TestMain_SchemeRegistered(t *testing.T) {
	// scheme is the package-level var from main.go.
	gvs := scheme.AllKnownTypes()
	pdisFound := false
	rbgFound := false
	for gvk := range gvs {
		if gvk.Group == "pdai.pdai.io" && gvk.Kind == "PDInferenceService" {
			pdisFound = true
		}
		if gvk.Group == "workloads.x-k8s.io" && gvk.Kind == "RoleBasedGroup" {
			rbgFound = true
		}
	}
	if !pdisFound {
		t.Error("PDInferenceService not registered in scheme")
	}
	if !rbgFound {
		t.Error("RoleBasedGroup not registered in scheme (rbgv1alpha1 missing from init())")
	}
}

// TestMain_ManagerStartup verifies that a manager can be created with the full
// component set (controller + API server) and started without panic.
func TestMain_ManagerStartup(t *testing.T) {
	// Bootstrap envtest.
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			"/home/zzy/code/rbg/config/crd/bases",
		},
		ErrorIfCRDPathMissing: true,
	}
	if dir := getEnvTestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("failed to start envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("failed to stop envtest: %v", err)
		}
	})

	// Register schemes into the package-level scheme var from main.go.
	if err := pdaiv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbgv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// Set up the controller with ConfigMerger and RBGBuilder.
	if err := (&controller.PDInferenceServiceReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		ConfigMerger: config.NewMerger(mgr.GetClient()),
		RBGBuilder:   translator.NewRBGBuilder(),
	}).SetupWithManager(mgr); err != nil {
		t.Fatalf("failed to setup controller: %v", err)
	}

	// Register API server as a Runnable.
	apiPort := freePort(t)
	apiSrv := apiserver.New(fmt.Sprintf(":%d", apiPort), mgr.GetClient(), mgr.GetAPIReader(), mgr.GetScheme())
	if err := mgr.Add(apiSrv); err != nil {
		t.Fatalf("failed to add API server to manager: %v", err)
	}

	// Start manager and wait for it to be ready.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	mgrDone := make(chan error, 1)
	go func() { mgrDone <- mgr.Start(ctx) }()

	// Give the manager 10s to start all runnables.
	select {
	case err := <-mgrDone:
		if ctx.Err() == nil {
			t.Errorf("manager exited unexpectedly: %v", err)
		}
	case <-time.After(10 * time.Second):
		// Manager is still running after 10 seconds — this is the success case.
		cancel()
	}
}
