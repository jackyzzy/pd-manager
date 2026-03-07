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

// Package business contains L3 business-scenario e2e tests that run against
// a real GPU cluster (a30).  They are gated by the environment variable
// BUSINESS_E2E=true so they are never executed accidentally.
//
// Run from WSL:
//
//	BUSINESS_E2E=true go test ./test/e2e/business/... -v -timeout 60m
package business

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/pd-ai/pd-manager/test/utils"
)

// TestBusinessE2E is the Ginkgo entry point.
func TestBusinessE2E(t *testing.T) {
	if os.Getenv("BUSINESS_E2E") != "true" {
		t.Skip("set BUSINESS_E2E=true to run business e2e tests")
	}
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting pd-manager business e2e suite\n")
	RunSpecs(t, "business e2e suite")
}

var _ = BeforeSuite(func() {
	By("verifying kubectl can reach the cluster")
	cmd := exec.Command("kubectl", "cluster-info")
	_, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "kubectl cannot reach the cluster — check KUBECONFIG")

	By("verifying pd-manager CRDs are installed")
	cmd = exec.Command("kubectl", "get", "crd", "pdinferenceservices.pdai.pdai.io")
	_, err = utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "pdinferenceservices CRD not found — run: kubectl apply -f config/crd/bases/")

	By("verifying pd-manager controller is running")
	cmd = exec.Command("kubectl", "get", "deployment", "pd-manager-controller-manager",
		"-n", "pd-manager-system")
	_, err = utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "pd-manager controller not found in pd-manager-system namespace")
})
