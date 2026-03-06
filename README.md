# pd-manager

Kubernetes Operator for managing PD (Prefill-Decode) disaggregated LLM inference instances, built on top of [RBG (RoleBasedGroup)](https://github.com/sgl-project/role-based-group) and [SGLang](https://github.com/sgl-project/sglang).

## Overview

PD disaggregation separates the Prefill and Decode phases of LLM inference onto dedicated nodes, enabling:
- Independent scaling of compute-bound (prefill) and memory-bandwidth-bound (decode) roles
- Higher throughput via GPU Direct RDMA KV Cache transfer
- Production-validated at scale (LMSYS: 96× H100, 128× H200)

**pd-manager** provides a clean, user-facing API (`PDInferenceService` CRD + REST API) that hides the complexity of multi-role workload orchestration, translating high-level business intent into RBG `RoleBasedGroup` resources.

```
User / Platform
      │
      ▼
PDInferenceService (CRD)  ←→  pd-manager Operator
                                       │
                                       ▼
                              RoleBasedGroup (RBG)
                        ┌─────────────┬────────────┬────────────┐
                        │  scheduler  │  prefill   │   decode   │
                        └─────────────┴────────────┴────────────┘
                               SGLang Inference Engine
```

## Architecture

### Three-Component Model

Each `PDInferenceService` creates an RBG with three roles:

| Role | Component | Description |
|------|-----------|-------------|
| `scheduler` | SGLang Model Gateway (sgl-router) | Routes requests; supports cache-aware, power-of-two, random, round-robin strategies; circuit breaker, retry with jitter, token bucket rate limiting; 40+ Prometheus metrics + OpenTelemetry tracing |
| `prefill` | SGLang `--disaggregation-mode prefill` | Executes forward computation; transfers KV Cache to Decode via GPU Direct RDMA |
| `decode` | SGLang `--disaggregation-mode decode` | Pre-allocates GPU memory; generates tokens using the transferred KV Cache |

### Request Flow

```
Client → Router
           │ selects a (Prefill, Decode) pair
           ├──▶ Decode: pre-allocates GPU memory
           └──▶ Prefill: runs forward pass
                    │ GPU Direct RDMA (mooncake / nccl)
                    ▼
               Decode: generates tokens using transferred KV Cache
                    │
                    ▼
               Response → Client
```

### Why RBG?

RBG (RoleBasedGroup) is designed for multi-role inference topologies:

- **Role dependency ordering** — scheduler → prefill → decode startup sequencing
- **Predictable DNS** — Headless Service per role (e.g., `sglang-pd-prefill-0.sglang-pd-prefill`); SGLang Model Gateway discovers pods via Label Selectors
- **HPA bridge** — `RoleBasedGroupScalingAdapter` (RBGSA) exposes the `scale` subresource per role, enabling standard Kubernetes HPA integration
- **Coordinated scale-up pacing** — `coordination.MaxSkew` limits deployment-progress difference between roles during scale-up
- **Topology injection** — full role topology info injected into every Pod

## PDInferenceService CRD

Users declare a single resource; pd-manager handles the rest.

```yaml
apiVersion: pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: qwen2-72b
spec:
  model: Qwen/Qwen2-72B
  engine: sglang                   # sglang | vllm (planned)

  prefill:
    replicas: 1
    resources:
      gpu: "8"
      gpuType: H100

  decode:
    replicas: 2
    resources:
      gpu: "8"
      gpuType: H100

  pdRatio: "1:2"                   # prefill replicas = decode replicas × ratio
                                   # mutually exclusive with prefill HPA

  router:
    strategy: cache-aware          # cache-aware | power-of-two | random | round-robin

  # Optional: reference a platform-managed engine profile
  engineProfileRef: h100-mooncake-72b

  # Optional: override or extend profile settings
  engineConfig:
    tensorParallelSize: 8
    kvTransfer:
      backend: mooncake            # mooncake | nccl
      config:                      # passed as-is to --kv-transfer-config JSON
        transport: rdma
        device: erdma0
    memFractionStatic: 0.9
    extraArgs:                     # appended to SGLang startup command
      prefill:
        - "--chunked-prefill-size=8192"
      decode:
        - "--max-running-requests=256"

  # Optional: HPA for decode role (requires pdRatio to drive prefill)
  scaling:
    decode:
      minReplicas: 1
      maxReplicas: 10
      metrics:
        - type: Resource
          resource:
            name: nvidia.com/gpu
            target:
              type: Utilization
              averageUtilization: 80
```

### Status

```yaml
status:
  phase: Running                   # Pending | Initializing | Running | Failed
  endpoint: "http://..."           # Router service endpoint
  conditions:
    - type: Ready
      status: "True"
  roleStatuses:
    - name: prefill
      replicas: 1
      readyReplicas: 1
    - name: decode
      replicas: 2
      readyReplicas: 2
```

## Engine Configuration

SGLang startup parameters are managed in three tiers (highest priority first):

| Tier | Source | Overridable? |
|------|--------|-------------|
| pd-manager owned | `--disaggregation-mode`, `--model`, `--served-model-name` | No |
| User inline | `PDInferenceService.engineConfig` | Yes |
| Platform profile | `PDEngineProfile` (referenced via `engineProfileRef`) | Baseline |

`extraArgs` from profile and inline config are **appended** (not overridden), allowing both to apply.

See [`docs/design/engine-config.md`](docs/design/engine-config.md) for full design.

## Scaling

### HPA Auto-scaling

pd-manager enables HPA on the decode role via RBG's `RoleBasedGroupScalingAdapter` bridge:

```
HPA ──writes──▶ RoleBasedGroupScalingAdapter.spec.replicas
                  │  (auto-created by RBG)
                  ▼
             RBG decode role replicas updated
                  │  (pd-manager watches RBG)
                  ▼
             prefill replicas = decode × pdRatio (written by pd-manager)
```

### pdRatio vs HPA

| | HPA | pdRatio |
|---|---|---|
| Decides | How many decode instances | How many prefill instances |
| Trigger | Load metrics (GPU util, queue depth) | decode replica count change |
| Conflict | If both target prefill → loop | Admission Webhook rejects the config |

## KV Cache Transfer Backends

| Backend | Protocol | Notes |
|---------|----------|-------|
| `mooncake` (default) | GPU Direct RDMA | Alibaba's high-performance transfer library |
| `nccl` | NCCL | NVIDIA Collective Communications Library |

The transfer tool is packaged inside the inference image. pd-manager only passes `--kv-transfer-backend` and `--kv-transfer-config` flags to SGLang.

## REST API

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/pd-inference-services` | List all instances |
| `POST` | `/api/v1/pd-inference-services` | Create instance |
| `GET` | `/api/v1/pd-inference-services/{name}` | Query status |
| `PUT` | `/api/v1/pd-inference-services/{name}` | Update (scale / config) |
| `DELETE` | `/api/v1/pd-inference-services/{name}` | Delete instance |

## Infrastructure Requirements

- Kubernetes cluster with RBG Operator installed
- GPU nodes with 32GB+ VRAM (H100 / H200 recommended)
- High-performance RDMA NICs for KV Cache transfer (e.g., eRDMA on Alibaba Cloud)
- Minimum 6 GPUs for a minimal PD deployment

## Development

```bash
make generate manifests            # Generate CRD manifests and DeepCopy
make build                         # Build operator binary
make run                           # Run locally against cluster (uses KUBECONFIG)
go test ./...                      # Run all unit tests
make docker-build docker-push IMG=<registry>/pd-manager:<tag>
make deploy IMG=<registry>/pd-manager:<tag>
```

## References

- [RBG (RoleBasedGroup)](https://github.com/sgl-project/role-based-group)
- [SGLang Model Gateway (sgl-router)](https://github.com/sgl-project/sgl-router)
- [SGLang PD Disaggregation](https://docs.sglang.ai/backend/disaggregated_prefill.html)
- [Kubernetes controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)
- [Engine Config Design](docs/design/engine-config.md)
