# pd-manager

Kubernetes Operator for managing PD (Prefill-Decode) disaggregated inference instances on top of [RBG (RoleBasedGroup)](https://github.com/sgl-project/role-based-group) workloads.

## Overview

PD disaggregation separates the Prefill and Decode phases of LLM inference onto dedicated nodes, enabling independent scaling and higher throughput. **pd-manager** provides a clean, user-facing API (`PDInferenceService` CRD + REST API) that abstracts the complexity of multi-role workload orchestration, translating high-level business intent into RBG `RoleBasedGroup` resources.

```
User / Platform
      │
      ▼
PDInferenceService (CRD)  ─── pd-manager Operator
                                       │
                                       ▼
                              RoleBasedGroup (RBG)
                           ┌──────────┬──────────┐
                           │ scheduler│ prefill  │ decode │
                           └──────────┴──────────┘────────┘
                                  SGLang Inference Engine
```

## Architecture

### Three-Component Inference Model

Each `PDInferenceService` creates an RBG with three roles:

| Role | Component | Description |
|------|-----------|-------------|
| `scheduler` | SGLang Model Gateway (sgl-router) | Routes requests; supports cache-aware, power-of-two, random, round-robin strategies |
| `prefill` | SGLang `--disaggregation-mode prefill` | Executes forward computation; transfers KV Cache to Decode via GPU Direct RDMA |
| `decode` | SGLang `--disaggregation-mode decode` | Pre-allocates GPU memory; generates tokens using the transferred KV Cache |

### Request Flow

```
Client → Router → (Prefill, Decode) pair selected
                → Decode pre-allocates GPU memory
                → Prefill runs forward pass
                → KV Cache transferred via RDMA (mooncake / nccl)
                → Decode generates tokens
```

### Why RBG?

RBG (RoleBasedGroup) is designed specifically for multi-role inference topologies:

- Declarative role dependency ordering (scheduler → prefill → decode)
- Predictable Headless Service DNS per role (e.g., `sglang-pd-prefill-0.sglang-pd-prefill`)
- Native coordinated scaling via `coordination` field — maintains P:D ratio across scale events
- Topology injection into every Pod — no external service discovery needed

## PDInferenceService CRD

Users declare a single resource; pd-manager handles the rest.

```yaml
apiVersion: pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: llama3-pd
spec:
  model: meta-llama/Llama-3.1-70B-Instruct
  engine: sglang                  # sglang | vllm (planned)
  transferBackend: mooncake        # mooncake | nccl
  prefill:
    replicas: 2
    resources:
      gpu: "8"
      gpuType: H100
  decode:
    replicas: 4
    resources:
      gpu: "8"
      gpuType: H100
  pdRatio: "1:2"                  # maintained during auto-scaling
  router:
    strategy: cache-aware
```

## REST API

In addition to the CRD interface, pd-manager exposes a RESTful API:

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/pd-inference-services` | Create instance |
| `GET` | `/api/v1/pd-inference-services/{name}` | Query status |
| `PUT` | `/api/v1/pd-inference-services/{name}` | Update (scale / config) |
| `DELETE` | `/api/v1/pd-inference-services/{name}` | Delete instance |

## Infrastructure Requirements

- Kubernetes cluster with RBG Operator installed
- GPU nodes with 32GB+ VRAM (H100/H200 recommended)
- High-performance RDMA NICs for KV Cache transfer (e.g., eRDMA on Alibaba Cloud)
- Minimum 6 GPUs for a minimal PD deployment

## KV Cache Transfer Backends

| Backend | Protocol | Notes |
|---------|----------|-------|
| mooncake | GPU Direct RDMA | Default; Alibaba's high-performance transfer library |
| nccl | NCCL | NVIDIA Collective Communications Library |

## Scaling

pd-manager supports two scaling modes:

1. **HPA / KEDA auto-scaling** — Triggered by GPU utilization or request queue depth metrics
2. **pdRatio-linked scaling** — Automatically recomputes Prefill/Decode replica counts to maintain the configured `pdRatio` on every scale event

## Engine Extensibility

pd-manager uses a Strategy Pattern internally to support multiple inference engines. The `engine` field in `PDInferenceService` selects the adapter (sglang today, vllm planned), which handles engine-specific startup flags, routing configuration, and KV Cache transfer setup.

## Development

```bash
make generate manifests   # Generate CRD manifests and DeepCopy
make build                # Build operator binary
make run                  # Run locally against cluster (uses KUBECONFIG)
go test ./...             # Run all unit tests
make docker-build docker-push IMG=<registry>/pd-manager:<tag>
make deploy IMG=<registry>/pd-manager:<tag>
```

## References

- [RBG (RoleBasedGroup)](https://github.com/sgl-project/role-based-group)
- [SGLang Model Gateway](https://github.com/sgl-project/sgl-router)
- [SGLang PD Disaggregation](https://docs.sglang.ai/backend/disaggregated_prefill.html)
- [Kubernetes controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)
