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

package business

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/pd-ai/pd-manager/test/utils"
)

// ── Test constants ────────────────────────────────────────────────────────────

const (
	// testNamespace is where the PDInferenceService is created.
	testNamespace = "default"

	// testServiceName is the PDInferenceService name used in all tests.
	testServiceName = "qwen3-14b-e2e"

	// modelPath is the host path to the model on the a30 node.
	modelPath = "/data/model/qwen3-14b"

	// servedModelName is the model name reported by the API.
	servedModelName = "Qwen/Qwen3-14B"

	// schedulerImage / prefillImage / decodeImage are the container images.
	schedulerImage = "lmsysorg/sgl-model-gateway:v0.3.1"
	prefillImage   = "lmsysorg/sglang:v0.5.8-cu130-amd64-runtime"
	decodeImage    = "lmsysorg/sglang:v0.5.8-cu130-amd64-runtime"

	// routerLocalPort is the local port used for kubectl port-forward to the scheduler.
	routerLocalPort = "18080"

	// Timeouts
	shortTimeout  = 2 * time.Minute
	podTimeout    = 30 * time.Minute // GPU pods take ~15-20 min to load the model
	inferTimeout  = 5 * time.Minute
)

// ── PDInferenceService fixture ────────────────────────────────────────────────

// pdisManifest is the YAML fixture.  It uses 2 GPUs per role (tp=2) to match
// Qwen3-14B's minimum tensor-parallel requirement on the a30 node.
var pdisManifest = fmt.Sprintf(`
apiVersion: pdai.pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: %s
  namespace: %s
spec:
  model: %s
  modelStorage:
    type: hostPath
    hostPath: %s
    mountPath: /models
  images:
    scheduler: %s
    prefill:   %s
    decode:    %s
  prefill:
    replicas: 1
    resources:
      gpu: "2"
  decode:
    replicas: 1
    resources:
      gpu: "2"
  router:
    strategy: round-robin
  engineConfig:
    tensorParallelSize: 2
    kvTransfer:
      backend: nixl
    extraArgs:
      prefill:
        - --trust-remote-code
        - --disable-radix-cache
        - --mem-fraction-static
        - "0.88"
        - --chunked-prefill-size
        - "8192"
        - --page-size
        - "128"
        - --cuda-graph-max-bs
        - "256"
      decode:
        - --trust-remote-code
        - --disable-radix-cache
        - --mem-fraction-static
        - "0.88"
        - --chunked-prefill-size
        - "8192"
        - --page-size
        - "128"
        - --cuda-graph-max-bs
        - "256"
      scheduler:
        - --health-check-timeout-secs
        - "6000000"
        - --health-check-interval-secs
        - "6000"
        - --worker-startup-timeout-secs
        - "3600"
        - --worker-startup-check-interval
        - "30"
        - --retry-max-retries
        - "30"
        - --retry-initial-backoff-ms
        - "30000"
        - --retry-max-backoff-ms
        - "60000"
`,
	testServiceName, testNamespace,
	servedModelName,
	modelPath,
	schedulerImage, prefillImage, decodeImage,
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// kubectl is a shorthand that runs a kubectl command and returns output.
func kubectl(args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	return utils.Run(cmd)
}

// podLogsContain returns true if the pod logs contain any of the given strings.
func podLogsContain(podName, namespace string, needles ...string) bool {
	out, err := kubectl("logs", podName, "-n", namespace, "--tail=200")
	if err != nil {
		return false
	}
	for _, needle := range needles {
		if strings.Contains(out, needle) {
			return true
		}
	}
	return false
}

// podHasError returns an error description if the pod logs contain a Python
// traceback, CUDA OOM, or the pod's last state is a fatal failure.
func podHasError(podName, namespace string) string {
	// Check logs for Python exceptions or CUDA OOM
	out, err := kubectl("logs", podName, "-n", namespace, "--tail=100")
	if err == nil {
		if strings.Contains(out, "Traceback (most recent call last)") {
			return "Python traceback found in logs"
		}
		if strings.Contains(out, "CUDA out of memory") {
			return "CUDA OOM found in logs"
		}
		if strings.Contains(out, "RuntimeError") && strings.Contains(out, "CUDA") {
			return "CUDA RuntimeError found in logs"
		}
	}

	// Check pod events for Warning reasons that indicate permanent failure
	events, err := kubectl("get", "events", "-n", namespace,
		"--field-selector", "involvedObject.name="+podName,
		"--sort-by=.lastTimestamp", "-o", "json")
	if err == nil {
		var evList struct {
			Items []struct {
				Type    string `json:"type"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(events), &evList) == nil {
			for _, ev := range evList.Items {
				if ev.Type == "Warning" {
					switch ev.Reason {
					case "OOMKilled", "CrashLoopBackOff", "BackOff", "Failed":
						return fmt.Sprintf("pod event Warning/%s: %s", ev.Reason, ev.Message)
					}
				}
			}
		}
	}
	return ""
}

// podStatus returns the pod phase and container state reason.
func podPhase(podName, namespace string) string {
	out, err := kubectl("get", "pod", podName, "-n", namespace,
		"-o", "jsonpath={.status.phase}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// podContainerState returns "running", "waiting/<reason>", "terminated/<reason>"
func podContainerState(podName, namespace string) string {
	out, err := kubectl("get", "pod", podName, "-n", namespace,
		"-o", "jsonpath={.status.containerStatuses[0].state}")
	if err != nil || out == "" {
		return "unknown"
	}
	// Parse the JSON state object
	var state struct {
		Running    *struct{}           `json:"running"`
		Waiting    *struct{ Reason string `json:"reason"` } `json:"waiting"`
		Terminated *struct{ Reason string `json:"reason"` } `json:"terminated"`
	}
	if json.Unmarshal([]byte(out), &state) == nil {
		switch {
		case state.Running != nil:
			return "running"
		case state.Waiting != nil:
			return "waiting/" + state.Waiting.Reason
		case state.Terminated != nil:
			return "terminated/" + state.Terminated.Reason
		}
	}
	return "unknown"
}

// getPodsForRole returns pod names for a given role label in the namespace.
func getPodsForRole(role, namespace string) []string {
	out, err := kubectl("get", "pods", "-n", namespace,
		"-l", "workloads.x-k8s.io/role-name="+role,
		"-o", "jsonpath={.items[*].metadata.name}")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	return strings.Fields(out)
}

// schedulerServiceName returns the Service name for the scheduler role.
// RBG creates a headless service per StatefulSet role with prefix "s-".
func schedulerServiceName() string {
	return fmt.Sprintf("s-%s-scheduler", testServiceName)
}

// portForwardRouter starts kubectl port-forward for the scheduler service
// and returns a cancel func.  Callers must invoke cancel() when done.
func portForwardRouter() (localAddr string, cancel context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "kubectl", "port-forward",
		"svc/"+schedulerServiceName(),
		routerLocalPort+":8000",
		"-n", testNamespace,
	)
	_ = cmd.Start()
	// Give port-forward time to establish
	time.Sleep(3 * time.Second)
	return "http://localhost:" + routerLocalPort, cancel
}

// httpGet performs a GET request and returns the response body and status code.
func httpGet(url string) (int, string, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String(), nil
}

// httpPost performs a POST with JSON body and returns the response.
func httpPost(url string, body interface{}) (int, string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload)) //nolint:noctx
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String(), nil
}

// ── Test suite ────────────────────────────────────────────────────────────────

var _ = Describe("PDInferenceService Business E2E", Ordered, func() {

	// ── Lifecycle ─────────────────────────────────────────────────────────

	BeforeAll(func() {
		By("applying PDInferenceService manifest")
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(pdisManifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "failed to create PDInferenceService")
	})

	AfterAll(func() {
		By("deleting PDInferenceService")
		_, _ = kubectl("delete", "pdinferenceservice", testServiceName, "-n", testNamespace,
			"--ignore-not-found=true")

		By("waiting for RoleBasedGroup cascade deletion")
		Eventually(func() string {
			out, _ := kubectl("get", "rolebasedgroup", testServiceName, "-n", testNamespace,
				"--ignore-not-found=true")
			return strings.TrimSpace(out)
		}, shortTimeout, 5*time.Second).Should(BeEmpty(), "RBG should be cascade-deleted")
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			By("collecting diagnostic info on failure")
			for _, role := range []string{"scheduler", "prefill", "decode"} {
				pods := getPodsForRole(role, testNamespace)
				for _, pod := range pods {
					logs, _ := kubectl("logs", pod, "-n", testNamespace, "--tail=50")
					_, _ = fmt.Fprintf(GinkgoWriter, "\n=== pod %s logs ===\n%s\n", pod, logs)
					desc, _ := kubectl("describe", "pod", pod, "-n", testNamespace)
					_, _ = fmt.Fprintf(GinkgoWriter, "\n=== pod %s describe ===\n%s\n", pod, desc)
				}
			}
			events, _ := kubectl("get", "events", "-n", testNamespace, "--sort-by=.lastTimestamp")
			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== events ===\n%s\n", events)
		}
	})

	// Default timeouts
	SetDefaultEventuallyTimeout(shortTimeout)
	SetDefaultEventuallyPollingInterval(10 * time.Second)

	// ── Tier 1: Kubernetes resource checks ────────────────────────────────

	Context("Tier 1: Kubernetes resource checks", func() {

		It("should create an RBG with three roles", func() {
			By("waiting for RBG to appear")
			Eventually(func() error {
				_, err := kubectl("get", "rolebasedgroup", testServiceName, "-n", testNamespace)
				return err
			}).Should(Succeed())

			By("verifying RBG has scheduler, prefill, decode roles")
			out, err := kubectl("get", "rolebasedgroup", testServiceName, "-n", testNamespace,
				"-o", "jsonpath={.spec.roles[*].name}")
			Expect(err).NotTo(HaveOccurred())
			for _, role := range []string{"scheduler", "prefill", "decode"} {
				Expect(out).To(ContainSubstring(role), "RBG should have role %q", role)
			}
		})

		It("should add finalizer to PDInferenceService", func() {
			Eventually(func() string {
				out, _ := kubectl("get", "pdinferenceservice", testServiceName, "-n", testNamespace,
					"-o", "jsonpath={.metadata.finalizers}")
				return out
			}).Should(ContainSubstring("finalizer"), "PDInferenceService should have a finalizer")
		})

		It("should set correct prefill/decode worker URLs in scheduler args", func() {
			By("checking scheduler role args in RBG spec")
			out, err := kubectl("get", "rolebasedgroup", testServiceName, "-n", testNamespace,
				"-o", "jsonpath={.spec.roles[?(@.name=='scheduler')].template.spec.containers[0].args}")
			Expect(err).NotTo(HaveOccurred())

			expectedPrefill := fmt.Sprintf("%s-prefill-0.s-%s-prefill.%s.svc.cluster.local:8000",
				testServiceName, testServiceName, testNamespace)
			expectedDecode := fmt.Sprintf("%s-decode-0.s-%s-decode.%s.svc.cluster.local:8000",
				testServiceName, testServiceName, testNamespace)

			Expect(out).To(ContainSubstring("--pd-disaggregation"),
				"scheduler should have --pd-disaggregation flag")
			Expect(out).To(ContainSubstring(expectedPrefill),
				"scheduler should have --prefill URL for prefill-0")
			Expect(out).To(ContainSubstring(expectedDecode),
				"scheduler should have --decode URL for decode-0")
		})
	})

	// ── Tier 2: Pod health checks ─────────────────────────────────────────

	Context("Tier 2: Pod health checks", func() {

		It("should schedule pods for all three roles within 5 minutes", func() {
			for _, role := range []string{"scheduler", "prefill", "decode"} {
				role := role
				Eventually(func() int {
					return len(getPodsForRole(role, testNamespace))
				}, 5*time.Minute, 15*time.Second).Should(BeNumerically(">", 0),
					"no pods found for role %q", role)
			}
		})

		It("should not have pod-level errors within 3 minutes of scheduling", func() {
			// Check 3 minutes after pods appear — long enough for image pull + init
			// but short enough to catch early crashes before model loading starts.
			time.Sleep(3 * time.Minute)

			for _, role := range []string{"scheduler", "prefill", "decode"} {
				pods := getPodsForRole(role, testNamespace)
				for _, pod := range pods {
					state := podContainerState(pod, testNamespace)
					// CrashLoopBackOff or OOMKilled are fatal early errors
					Expect(state).NotTo(ContainSubstring("CrashLoopBackOff"),
						"pod %s is in CrashLoopBackOff", pod)
					Expect(state).NotTo(ContainSubstring("OOMKilled"),
						"pod %s is OOMKilled", pod)

					if errMsg := podHasError(pod, testNamespace); errMsg != "" {
						Fail(fmt.Sprintf("pod %s has error: %s", pod, errMsg))
					}
				}
			}
		})

		It("should have all pods Running within the GPU startup timeout", func() {
			// GPU model loading takes ~15-20 min for Qwen3-14B with tp=2.
			for _, role := range []string{"scheduler", "prefill", "decode"} {
				role := role
				Eventually(func(g Gomega) {
					pods := getPodsForRole(role, testNamespace)
					g.Expect(pods).NotTo(BeEmpty(), "no pods for role %q", role)
					for _, pod := range pods {
						phase := podPhase(pod, testNamespace)
						g.Expect(phase).To(Equal("Running"),
							"pod %s (role %s) phase is %q, not Running", pod, role, phase)
					}
				}, podTimeout, 30*time.Second).Should(Succeed())
			}
		})

		It("should show server-ready messages in prefill/decode logs", func() {
			// After pods are Running (previous It), check logs for startup confirmation.
			for _, role := range []string{"prefill", "decode"} {
				pods := getPodsForRole(role, testNamespace)
				Expect(pods).NotTo(BeEmpty())
				for _, pod := range pods {
					Expect(podLogsContain(pod, testNamespace,
						"The server is launched",
						"Uvicorn running",
						"Application startup complete",
					)).To(BeTrue(), "pod %s (%s) logs do not contain server-ready message", pod, role)
				}
			}
		})

		It("should report PDInferenceService phase=Running after all pods are ready", func() {
			Eventually(func() string {
				out, _ := kubectl("get", "pdinferenceservice", testServiceName,
					"-n", testNamespace, "-o", "jsonpath={.status.phase}")
				return strings.TrimSpace(out)
			}, 2*time.Minute, 10*time.Second).Should(Equal("Running"))
		})
	})

	// ── Tier 3: Router API checks ─────────────────────────────────────────

	Context("Tier 3: Router API checks", func() {
		var routerBase string
		var cancelPF context.CancelFunc

		BeforeAll(func() {
			By("port-forwarding to scheduler service")
			routerBase, cancelPF = portForwardRouter()
		})
		AfterAll(func() {
			if cancelPF != nil {
				cancelPF()
			}
		})

		It("should respond 200 on GET /health", func() {
			Eventually(func(g Gomega) {
				code, body, err := httpGet(routerBase + "/health")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(code).To(Equal(200), "GET /health body: %s", body)
			}, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should list the model on GET /v1/models", func() {
			code, body, err := httpGet(routerBase + "/v1/models")
			Expect(err).NotTo(HaveOccurred())
			Expect(code).To(Equal(200), "GET /v1/models body: %s", body)
			Expect(body).To(ContainSubstring(servedModelName),
				"model %q not found in /v1/models response", servedModelName)
		})

		It("should have registered workers (GET /health_generate passes)", func() {
			// /health_generate sends a single-token generation to each worker.
			// A 200 response proves prefill and decode are registered and healthy.
			Eventually(func(g Gomega) {
				code, body, err := httpGet(routerBase + "/health_generate")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(code).To(Equal(200),
					"GET /health_generate returned %d; body: %s", code, body)
			}, 2*time.Minute, 10*time.Second).Should(Succeed())
		})
	})

	// ── Tier 4: Inference check ───────────────────────────────────────────

	Context("Tier 4: Inference check", func() {
		var routerBase string
		var cancelPF context.CancelFunc

		BeforeAll(func() {
			By("port-forwarding to scheduler service for inference")
			routerBase, cancelPF = portForwardRouter()
		})
		AfterAll(func() {
			if cancelPF != nil {
				cancelPF()
			}
		})

		It("should respond to a chat completion request", func() {
			payload := map[string]interface{}{
				"model": servedModelName,
				"messages": []map[string]string{
					{"role": "user", "content": "Reply with exactly the word: hello"},
				},
				"max_tokens": 20,
			}

			var responseBody string
			Eventually(func(g Gomega) {
				code, body, err := httpPost(routerBase+"/v1/chat/completions", payload)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(code).To(Equal(200),
					"POST /v1/chat/completions returned %d; body: %s", code, body)
				g.Expect(body).To(ContainSubstring(`"choices"`),
					"response does not contain 'choices': %s", body)
				responseBody = body
			}, inferTimeout, 15*time.Second).Should(Succeed())

			By("verifying the response contains generated text")
			var resp struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			Expect(json.Unmarshal([]byte(responseBody), &resp)).To(Succeed())
			Expect(resp.Choices).NotTo(BeEmpty(), "no choices in response")
			Expect(resp.Choices[0].Message.Content).NotTo(BeEmpty(), "empty content in response")
			_, _ = fmt.Fprintf(GinkgoWriter, "inference response: %q\n",
				resp.Choices[0].Message.Content)
		})
	})

	// ── Tier 5: Cascade deletion ──────────────────────────────────────────

	Context("Tier 5: Cascade deletion", func() {
		It("should cascade-delete the RBG when PDInferenceService is deleted", func() {
			By("deleting PDInferenceService")
			_, err := kubectl("delete", "pdinferenceservice", testServiceName,
				"-n", testNamespace)
			Expect(err).NotTo(HaveOccurred())

			By("verifying RBG is deleted")
			Eventually(func() string {
				out, _ := kubectl("get", "rolebasedgroup", testServiceName,
					"-n", testNamespace, "--ignore-not-found=true")
				return strings.TrimSpace(out)
			}, shortTimeout, 5*time.Second).Should(BeEmpty(), "RBG should be cascade-deleted")

			By("verifying all role pods are gone")
			for _, role := range []string{"scheduler", "prefill", "decode"} {
				Eventually(func() int {
					return len(getPodsForRole(role, testNamespace))
				}, shortTimeout, 10*time.Second).Should(Equal(0),
					"pods for role %q should be deleted", role)
			}
		})
	})
})
