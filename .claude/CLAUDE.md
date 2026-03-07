# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**pd-manager** is a Kubernetes Operator that provides a high-level user-facing API for managing PD (Prefill-Decode) disaggregated LLM inference instances. It acts as a translation and orchestration layer on top of RBG (RoleBasedGroup) CRDs, converting user-declared `PDInferenceService` resources into multi-role RBG workloads running SGLang inference engines.

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
SGLang Inference Engine  →  Prefill / Decode / Scheduler pods
```

### Core CRD: PDInferenceService

pd-manager exposes a single user-facing CRD (`pdai.io/v1alpha1 / PDInferenceService`). The Reconciler translates it into an RBG `RoleBasedGroup` with three roles:

| Role | SGLang Flag | Purpose |
|------|-------------|---------|
| `scheduler` | sgl-router | Routes requests; cache-aware / power-of-two / round-robin strategies |
| `prefill` | `--disaggregation-mode prefill` | Forward computation + KV Cache transfer via GPU Direct RDMA |
| `decode` | `--disaggregation-mode decode` | Pre-allocates GPU memory; token generation using transferred KV Cache |

### Reconciler Pipeline (5 steps)

1. **Spec validation & defaulting** — Admission Webhook validates GPU type, model existence, P:D ratio; injects defaults (engine=sglang, transferBackend=mooncake)
2. **RBG Spec construction** — Translate PDInferenceService fields to RBG role definitions with dependency declarations (scheduler → prefill → decode)
3. **RBG resource apply** — Create/Update RoleBasedGroup with `ownerReference` pointing to PDInferenceService (enables cascade delete)
4. **Status aggregation** — Watch RBG status, aggregate Pod readiness and Service Endpoints into PDInferenceService Status using `meta/v1 Condition` pattern
5. **Scale coordination** — Two sub-responsibilities:
   - **HPA bridge setup**: Set `scalingAdapter.enable: true` on the decode role in RBG spec. RBG will auto-create a `RoleBasedGroupScalingAdapter` (RBGSA) CR that implements the `scale` subresource. pd-manager then creates an HPA object pointing to this RBGSA. HPA writes `rbgsa.spec.replicas`; the RBGSA reconciler propagates that into `rbg.spec.roles[decode].replicas`.
   - **pdRatio maintenance**: When decode replicas change (via HPA → RBGSA → RBG), pd-manager detects the change by watching RBG, recomputes `prefill replicas = decode replicas × ratio`, and writes the new value back to `rbg.spec.roles[prefill].replicas`.
   - **Coordination field**: The RBG `coordination` field controls scale-up *pacing* (MaxSkew = max deployment-progress difference between roles), NOT ratio enforcement. pd-manager sets this field to coordinate the pace at which prefill and decode scale up together.

## Key Design Decisions

- **RBG client integration**: Import RBG's `client-go/` directory directly as a Go module — do not hand-maintain CRD schemas.
- **Engine abstraction**: Internal Strategy Pattern (`engine` field) to support sglang/vllm adapters without interface changes.
- **Service discovery**: RBG creates Headless Services with predictable DNS names (e.g., `sglang-pd-prefill-0.sglang-pd-prefill`); SGLang Model Gateway discovers Prefill/Decode pods via Label Selectors (`--prefill-selector`, `--decode-selector`).
- **pdRatio vs HPA**: These are complementary, not overlapping. HPA decides decode count; pdRatio derives prefill count from decode. They are mutually exclusive on prefill — Admission Webhook rejects configs that set both pdRatio and prefill HPA.
- **Engine config layering**: Three-tier priority — pd-manager-owned args (immutable) > user inline `engineConfig` > `PDEngineProfile` template. See `docs/design/engine-config.md`.
- **KV transfer config passthrough**: `kvTransfer.config` is serialized as JSON and passed directly to SGLang as `--kv-transfer-config`; pd-manager does not parse its semantics.

## Scaling — RBG HPA Bridge

```
HPA ──writes──▶ RoleBasedGroupScalingAdapter.spec.replicas
                  │  (RBGSA auto-created by RBG when scalingAdapter.enable=true)
                  ▼
             RBG.spec.roles[decode].replicas
                  │  (pd-manager watches RBG)
                  ▼
             pd-manager recomputes prefill = decode × ratio
                  ▼
             RBG.spec.roles[prefill].replicas
```

| Responsibility | Owner |
|----------------|-------|
| Set `scalingAdapter.enable: true` on decode role | pd-manager |
| Auto-create RoleBasedGroupScalingAdapter (RBGSA) CR | RBG |
| Create HPA with scaleTargetRef pointing to RBGSA | pd-manager |
| Sync HPA → RBGSA → RBG decode replicas | RBG (RBGSA reconciler) |
| Watch RBG, recompute and write prefill replicas per pdRatio | pd-manager |
| Coordinate scale-up pacing (coordination MaxSkew) | pd-manager configures + RBG executes |

## REST API

- `GET /api/v1/pd-inference-services` — List all instances
- `POST /api/v1/pd-inference-services` — Create instance
- `GET /api/v1/pd-inference-services/{name}` — Query status
- `PUT /api/v1/pd-inference-services/{name}` — Update (scale / config change)
- `DELETE /api/v1/pd-inference-services/{name}` — Delete instance

## Development Commands

```bash
make generate manifests   # Generate CRD manifests and DeepCopy methods
make build                # Build the operator binary
make run                  # Run locally against cluster (uses KUBECONFIG)
go test ./...             # Run all unit tests
go test ./internal/controller/... -run TestReconcilePDInferenceService
make docker-build docker-push IMG=<registry>/pd-manager:<tag>
make deploy IMG=<registry>/pd-manager:<tag>
```

## Environmental Information
### code develop environment
wsl环境:
本地wsl 
代码位置: /home/zzy/code/pd-manager
### test environment
a30环境：
地址：183.56.181.9
端口：34451
用户名：a30
a30为单节点环境，既有docker，又有基于containerd的k8s，两者相互隔离
可以免密码登录

## Key Reference Files

- `docs/design/engine-config.md` — SGLang engine config layering design (PDEngineProfile CRD, extraArgs merge rules, translation to SGLang flags)



需要修改之后，设计验收方式，并进行验收。