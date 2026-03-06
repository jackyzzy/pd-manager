# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**pd-manager** is a Kubernetes Operator service for managing PD (Prefill-Decode) disaggregated inference instances. It acts as a high-level orchestration layer on top of RBG (RoleBasedGroup) CRDs, translating user-facing `PDInferenceService` resources into RBG workloads that run SGLang inference engines.

## Tech Stack

- **Language**: Go
- **Framework**: controller-runtime (Kubernetes Operator pattern)
- **Workload API**: RBG (RoleBasedGroup) — `workloads.x-k8s.io/v1alpha1`
- **Inference Engine**: SGLang (primary), vLLM (planned)
- **Routing**: SGLang Model Gateway (sgl-router, Rust-based)
- **Scaling**: Kubernetes HPA / KEDA

## Architecture

Four-layer architecture:

```
User Interface Layer     →  REST API / CLI / Web Console
pd-manager Operator      →  Core translation & orchestration (this repo)
RBG Operator             →  Workload orchestration (RoleBasedGroup CRD)
SGLang Inference Engine  →  Prefill / Decode pods
```

### Core CRD: PDInferenceService

pd-manager exposes a single user-facing CRD (`pdai.io/v1alpha1 / PDInferenceService`) that abstracts PD disaggregation details. The Reconciler translates it into an RBG `RoleBasedGroup` with three roles:

| Role | SGLang Flag | Purpose |
|------|-------------|---------|
| `scheduler` | mini_lb router | Route requests to Prefill/Decode pairs |
| `prefill` | `--disaggregation-mode prefill` | Prefill computation + KV Cache transfer via RDMA |
| `decode` | `--disaggregation-mode decode` | Token generation using transferred KV Cache |

### Reconciler Pipeline (5 steps)

1. **Spec validation & defaulting** — Admission Webhook validates GPU type, model existence, P:D ratio; injects defaults (engine=sglang, transfer_backend=mooncake)
2. **RBG Spec construction** — Translate PDInferenceService fields to RBG role definitions with dependency declarations
3. **RBG resource apply** — Create/Update RoleBasedGroup with `ownerReference` pointing to PDInferenceService (enables cascade delete)
4. **Status aggregation** — Watch RBG status, aggregate Pod readiness and Service Endpoints into PDInferenceService Status using `meta/v1 Condition` pattern
5. **Scale coordination** — On HPA/KEDA trigger, recompute Prefill/Decode replica counts from `pdRatio`, update RBG Spec to drive coordinated scaling

## Key Design Decisions

- **RBG over LWS**: RBG is used (not LeaderWorkerSet) because it natively supports multi-role topologies, injects topology info into Pods, and provides coordinated scaling via its `coordination` field.
- **RBG client integration**: Import RBG's `client-go/` directory directly as a Go module — do not hand-maintain CRD schemas.
- **Engine abstraction**: Internal Strategy Pattern (`engine` field) to support sglang/vllm adapters without interface changes.
- **Service discovery**: RBG creates Headless Services with predictable DNS names (e.g., `sglang-pd-prefill-0.sglang-pd-prefill`); SGLang Model Gateway discovers Prefill/Decode pods via Label Selectors (`--prefill-selector`, `--decode-selector`).

## REST API

RESTful API alongside the CRD interface:

- `POST /api/v1/pd-inference-services` — Create instance
- `GET /api/v1/pd-inference-services/{name}` — Query status
- `PUT /api/v1/pd-inference-services/{name}` — Update (scale / config change)
- `DELETE /api/v1/pd-inference-services/{name}` — Delete instance

## Development Commands

> Commands will be added here as the project is bootstrapped.

```bash
# Generate CRD manifests and DeepCopy methods
make generate manifests

# Run controller locally against a cluster (uses KUBECONFIG)
make run

# Build the operator binary
make build

# Run unit tests
go test ./...

# Run a single test
go test ./internal/controller/... -run TestReconcilePDInferenceService

# Build and push Docker image
make docker-build docker-push IMG=<registry>/pd-manager:<tag>

# Deploy to cluster
make deploy IMG=<registry>/pd-manager:<tag>
```

## KV Cache Transfer

SGLang transfers KV Cache from Prefill to Decode via GPU Direct RDMA. Supported backends:

- **mooncake** (default) — Alibaba's RDMA-based transfer
- **nccl** — NVIDIA Collective Communications Library
- Requires high-performance RDMA NICs (e.g., eRDMA on Alibaba Cloud)

## Scaling Modes

1. **HPA/KEDA auto-scaling** — Based on GPU utilization, request queue depth
2. **pdRatio-linked scaling** — Maintains configured Prefill:Decode ratio across scale events; handled by RBG's Coordination engine
