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
apiVersion: pdai.pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: qwen2-72b
spec:
  model: Qwen/Qwen2-72B
  engine: sglang                   # sglang | vllm (planned)

  modelStorage:
    type: hostPath                 # hostPath | pvc
    hostPath: /data/models
    mountPath: /models             # default: /models

  images:
    scheduler: lmsysorg/sgl-model-gateway:v0.3.1
    prefill: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    decode: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime

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

  # Optional: reference a platform-managed engine profile (from pd-system namespace)
  engineProfileRef: h100-mooncake-72b

  # Optional: override or extend profile settings
  engineConfig:
    tensorParallelSize: 8
    kvTransfer:
      backend: mooncake            # mooncake | nixl | nccl
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
```

### Status

```yaml
status:
  phase: Running                   # Pending | Initializing | Running | Failed | Terminating
  endpoint: "http://..."           # Router service endpoint
  conditions:
    - type: Ready
      status: "True"
      reason: AllRolesReady
  roleStatuses:
    - name: scheduler
      ready: 1
      total: 1
    - name: prefill
      ready: 1
      total: 1
    - name: decode
      ready: 2
      total: 2
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
| `mooncake` | GPU Direct RDMA | Alibaba's high-performance transfer library |
| `nixl` | NVIDIA NIXL | NVIDIA Inference Xfer Library |
| `nccl` | NCCL | NVIDIA Collective Communications Library |

pd-manager passes `--disaggregation-transfer-backend` to SGLang for prefill and decode roles.

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
make docker-build docker-push IMG=<registry>/pd-manager:<tag>
make deploy IMG=<registry>/pd-manager:<tag>
```

### Testing

本项目采用两层测试策略：

**L1 单元测试**（纯 Go `testing` 包，无外部依赖）：直接调用被测函数，使用 `fake.Client` 模拟 Kubernetes API，毫秒级完成。覆盖类型定义、配置合并、SGLang 参数构建、RBG 翻译、REST Handler 及 HTTP Server 路由。

**L2 集成测试**（`envtest` + Ginkgo/Gomega）：在内存中启动真实的 Kubernetes API Server 和 etcd，注册 CRD（包括 RBG），运行真实的 Reconciler 或 Webhook。用于验证 Reconcile 主流程、Finalizer、ownerReference 和 Webhook 校验规则。Controller 测试通过 Ginkgo `Ordered` + `BeforeAll` 只注册一个 Manager 实例，避免控制器重复注册冲突。

```bash
# 准备 envtest 二进制（仅需执行一次）
make envtest

# L1 单元测试（快速，约 1 秒）
go test ./api/... ./internal/config/... ./internal/translator/... ./internal/apiserver/... -v

# L2 集成测试（需要 envtest，约 1~2 分钟）
go test ./internal/controller/... ./internal/webhook/... ./cmd/... -v -timeout 5m

# 全量测试
go test ./... -timeout 5m
```

最新测试结果（本地 WSL）：

| 包 | 用例数 | 耗时 |
|----|--------|------|
| `api/v1alpha1` | 9 | ~30ms |
| `internal/config` | 7 | ~220ms |
| `internal/translator` (含 sglang) | 15 | ~80ms |
| `internal/apiserver` (含 handler) | 13 | ~450ms |
| `internal/controller` | 14 | ~14s |
| `internal/webhook/v1alpha1` | 10 | ~8s |
| `cmd` | 2 | ~23s |

See [`docs/test/automated-test.md`](docs/test/automated-test.md) for the full test guide and [`docs/test/manual-validation.md`](docs/test/manual-validation.md) for the a30 end-to-end validation playbook.

## References

- [RBG (RoleBasedGroup)](https://github.com/sgl-project/role-based-group)
- [SGLang Model Gateway (sgl-router)](https://github.com/sgl-project/sgl-router)
- [SGLang PD Disaggregation](https://docs.sglang.ai/backend/disaggregated_prefill.html)
- [Kubernetes controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)
- [Engine Config Design](docs/design/engine-config.md)
