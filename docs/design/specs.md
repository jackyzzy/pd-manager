# pd-manager Product Specs

> 版本：v1.0 草稿
> 状态：规划中
> 定位：AICP 推理平台 PD 分离推理实例管理服务（独立验证版本）

---

## 一、背景与定位

### 产品定位

pd-manager 是 AICP 推理平台的 PD（Prefill-Decode）分离推理实例管理服务。它将底层 RBG（RoleBasedGroup）工作负载的编排复杂性封装为简洁的用户 API，让平台运维工程师和 AI 应用开发者都能轻松管理 PD 分离推理实例的完整生命周期。

**v1 定位**：独立验证版本，优先完成核心生命周期管理能力，为后续集成到 AICP 多租户多集群平台打好基础。

### 与 AICP 平台的关系

```
AICP 平台（多租户 / 多集群）
    │
    ├── AI 应用服务 A
    ├── AI 应用服务 B
    └── pd-manager ← 本产品（v1 独立部署，v2 集成）
            │
            └── Kubernetes 集群（RBG Operator）
```

**v1**：pd-manager 独立部署，REST API 无认证，面向内网开发验证。
**v2+**：集成到 AICP 平台，接入平台 Token 认证，通过 Namespace 实现多租户隔离。

---

## 二、目标用户与使用场景

### 用户画像

| 用户角色 | 技术背景 | 主要诉求 |
|---------|---------|---------|
| **平台运维工程师** | 熟悉 Kubernetes，使用 kubectl / YAML | 稳定创建/删除实例，监控状态，问题排查 |
| **AI 应用开发者** | 了解模型推理，不熟悉 K8s | 通过 REST API 快速拉起推理服务，获取 Endpoint 调用 |

### 核心使用场景

#### 场景一：运维工程师部署推理服务

> 平台运维团队需要为某个模型（如 Qwen2-72B）上线 PD 分离推理服务，要求稳定可靠，支持手动扩缩容。

**操作路径**：
1. 运维工程师预先配置 `PDEngineProfile`（硬件 + 模型最优参数模板）
2. 创建 `PDInferenceService` YAML，引用 Profile
3. `kubectl apply` 或调用 REST API 创建实例
4. 查询 Status 确认服务就绪，获取 Router Endpoint
5. 将 Endpoint 注册到 API Gateway 供业务调用
6. 根据业务需求手动调整 prefill/decode 副本数
7. 下线时 `kubectl delete` 或调用 DELETE API

#### 场景二：AI 开发者快速验证模型

> AI 应用开发者需要临时拉起一个推理服务做功能验证，用完即销毁，不想学习 Kubernetes。

**操作路径**：
1. 调用 `POST /api/v1/pd-inference-services`，传入模型名和资源需求
2. 轮询 `GET /api/v1/pd-inference-services/{name}` 等待 Ready
3. 使用返回的 `endpoint` 发送推理请求
4. 验证完成，调用 `DELETE /api/v1/pd-inference-services/{name}` 销毁

#### 场景三：平台服务监控集成

> 平台监控系统需要感知所有 PDInferenceService 的状态，用于大盘展示和告警。

**操作路径**：
- 调用 `GET /api/v1/pd-inference-services` 定期轮询，或
- Watch `PDInferenceService` CRD 资源获取实时变化事件

---

## 三、用户故事

### US-01 创建推理服务（核心）

**As** 平台运维工程师，
**I want to** 通过声明一个 `PDInferenceService` 资源，自动完成 prefill、decode、scheduler 三个角色的部署，
**So that** 我不需要手动管理 RoleBasedGroup 的底层细节。

**验收标准**：
- 提交 PDInferenceService 后，pd-manager 自动创建对应的 RBG
- 所有角色 Pod Ready 后，Status.phase 变为 `Running`
- Status.endpoint 包含可访问的 Router 地址
- 失败时，Status.conditions 包含可读的失败原因

---

### US-02 查询服务状态

**As** AI 应用开发者，
**I want to** 通过 REST API 轮询服务状态，知道服务何时就绪，
**So that** 我知道什么时候可以开始发送推理请求。

**验收标准**：
- `GET /api/v1/pd-inference-services/{name}` 返回 phase、endpoint、角色副本详情
- phase 枚举：`Pending → Initializing → Running / Failed`
- 包含最近一次状态变更事件（时间 + 原因）

---

### US-03 手动扩缩容

**As** 平台运维工程师，
**I want to** 在业务高峰前手动增加 decode 副本数，
**So that** 服务能应对更高并发而不用等 HPA 慢慢响应。

**验收标准**：
- `PUT /api/v1/pd-inference-services/{name}` 支持修改 `prefill.replicas` 和 `decode.replicas`
- 修改生效后 RBG 副本数同步更新
- 如果配置了 pdRatio，修改 decode 副本时给出 prefill 联动提示（v1 不强制联动，由用户决定）

---

### US-04 删除推理服务

**As** AI 应用开发者，
**I want to** 调用一个 API 销毁推理服务，
**So that** 释放 GPU 资源，不产生额外费用。

**验收标准**：
- DELETE 操作触发后，RBG、Pod、RBGSA 等所有下层资源级联删除
- 删除过程中 phase 变为 `Terminating`
- 删除完成后资源消失，再次 GET 返回 404

---

### US-05 使用 Profile 模板配置引擎参数

**As** 平台运维工程师，
**I want to** 预先定义针对特定硬件和模型的最优 SGLang 启动参数，供用户引用，
**So that** 用户不需要了解底层 SGLang flag，也能获得最优性能。

**验收标准**：
- 支持创建 `PDEngineProfile` CRD，包含 images、applicability、engineRuntimes、extraArgs 等字段
- `PDInferenceService` 通过 `engineProfileRef` 引用 Profile，或直接在 `images` 字段中配置镜像
- inline `engineConfig` 字段覆盖 Profile 中同名字段
- `extraArgs` 合并追加（Profile + inline 都生效，inline 同名参数覆盖 Profile）

---

### US-06 引擎参数校验

**As** 平台运维工程师，
**I want to** 在提交配置时立即得到错误提示，
**So that** 不需要等到 Pod 启动失败才发现配置问题。

**验收标准**（v1 基本校验）：
- 必填字段缺失时拒绝创建（model、modelStorage、prefill/decode 资源）
- 未引用 Profile 时，`images.scheduler/prefill/decode` 均为必填
- `kvTransfer.backend` 只允许 `mooncake` / `nixl` / `nccl`
- `engine` 只允许 `sglang`（v1）
- `tensorParallelSize` 必须是正整数

---

## 四、v1 交付范围

### In Scope

| 功能 | 优先级 | 说明 |
|------|--------|------|
| PDInferenceService CRUD（CRD） | P0 | 创建/查询/删除，Update 仅支持副本数修改 |
| REST API（无认证） | P0 | 5 个接口，内网使用 |
| PDEngineProfile CRD | P0 | 平台侧配置模板，含 images / applicability / engineRuntimes |
| `images` 字段（PDInferenceService 直接配置镜像） | P0 | 无 Profile 时三个角色镜像均为必填 |
| `modelStorage` 字段（hostPath 挂载模型） | P0 | v1 支持 hostPath，pd-manager 翻译为 RBG volumes |
| Admission Webhook（基本校验） | P0 | 必填字段 + 枚举值校验 + images 互斥规则 |
| Status 聚合（phase + endpoint + 角色详情 + 事件） | P0 | 基于 RBG Status Watch |
| 手动扩缩容（PUT replicas） | P1 | prefill / decode 分别可调 |
| pdRatio 字段（存储 + 文档说明） | P1 | v1 存储但不强制联动，v2 自动联动 |
| 级联删除 | P0 | ownerReference 实现，立即生效 |
| 失败状态暴露（不自动重试） | P0 | Status.conditions 记录失败原因 |

### Out of Scope（v2+）

| 功能 | 计划版本 |
|------|---------|
| HPA 自动弹性 + pdRatio 联动 | v2 |
| 多租户 Namespace 隔离 | v2 |
| AICP 平台 Token 认证 | v2 |
| Webhook 回调事件推送 | v2 |
| 多集群管理 | v3 |
| vLLM 引擎支持 | v2 |

---

## 五、CRD 规格

### PDInferenceService

```yaml
apiVersion: pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: qwen3-14b
  namespace: default
spec:
  # 模型逻辑名（必填）→ --served-model-name
  model: Qwen/Qwen3-14B

  # 推理引擎（v1 仅支持 sglang）
  # +kubebuilder:default=sglang
  engine: sglang

  # 模型文件存储（必填）
  modelStorage:
    type: hostPath            # v1 支持 hostPath，v2 规划 pvc
    hostPath: /data/model/qwen3-14b
    # mountPath: /models      # 容器内路径，默认 /models

  # 角色镜像（未引用 Profile 时必填；引用 Profile 时可省略或部分覆盖）
  images:
    scheduler: lmsysorg/sgl-model-gateway:v0.3.1
    prefill: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    decode: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime

  # Prefill 角色配置
  prefill:
    replicas: 1              # 必填，>= 1
    resources:
      gpu: "2"               # GPU 卡数（必填）
      gpuType: A30           # GPU 型号，用于调度

  # Decode 角色配置
  decode:
    replicas: 2              # 必填，>= 1
    resources:
      gpu: "2"
      gpuType: A30

  # Router 配置（可选）
  router:
    # cache-aware | power-of-two | random | round-robin
    # +kubebuilder:default=round-robin
    strategy: cache-aware

  # P:D 比例（可选，v1 仅存储，不强制联动）
  # 格式："1:2" 表示 1 prefill 对应 2 decode
  pdRatio: "1:2"

  # 引用平台预定义的引擎参数模板（可选）
  engineProfileRef: a30-nixl-14b

  # 引擎参数（可选，覆盖 Profile 中同名字段）
  engineConfig:
    tensorParallelSize: 2
    kvTransfer:
      # mooncake | nixl | nccl
      backend: nixl
    extraArgs:               # 追加到启动命令，prefill/decode/scheduler 分别配置
      prefill:
        - "--mem-fraction-static=0.88"
        - "--chunked-prefill-size=8192"
      decode:
        - "--mem-fraction-static=0.88"
      scheduler:
        - "--policy=cache-aware"

status:
  # Pending | Initializing | Running | Failed | Terminating
  phase: Running

  # Router 访问地址
  endpoint: "http://10.0.0.1:30080"

  # 标准 Condition 列表
  conditions:
    - type: Ready
      status: "True"
      lastTransitionTime: "2026-03-06T10:00:00Z"
      reason: AllRolesReady
      message: "All 3 roles are ready"

  # 各角色状态
  roleStatuses:
    - name: scheduler
      replicas: 1
      readyReplicas: 1
    - name: prefill
      replicas: 1
      readyReplicas: 1
    - name: decode
      replicas: 2
      readyReplicas: 2

  # 最近一次事件
  lastEvent:
    time: "2026-03-06T10:00:00Z"
    reason: ScaleSucceeded
    message: "Decode replicas scaled from 2 to 4"
```

### PDEngineProfile

```yaml
apiVersion: pdai.io/v1alpha1
kind: PDEngineProfile
metadata:
  name: a30-nixl-qwen3-14b
  namespace: pd-system         # 平台统一管理，跨 namespace 引用
spec:
  description: "A30 双卡 + nixl 传输，适配 Qwen3-14B，均衡吞吐/延迟"

  # 适用范围元数据（帮助用户选择合适的 Profile，不参与翻译逻辑）
  applicability:
    gpuTypes: [A30]
    minGpuMemoryGiB: 24
    tensorParallelSize: 2
    modelSizeRange:
      min: "7B"
      max: "20B"
    optimizedFor: balanced      # high-throughput | low-latency | balanced
    sglangVersionRequired: ">=0.5.8"

  # 角色镜像（必填）
  images:
    scheduler: lmsysorg/sgl-model-gateway:v0.3.1
    prefill: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    decode: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime

  # 平台 Sidecar 注入，透传到 RBG engineRuntimes（可选，无 Patio 的环境不填）
  engineRuntimes:
    prefill:
      - profileName: sglang-pd-runtime
        containers:
          - name: patio-runtime
            args:
              - '--instance-info={"data":{"port":8000,"worker_type":"prefill","bootstrap_port":34000},"topo_type":"sglang"}'
    decode:
      - profileName: sglang-pd-runtime
        containers:
          - name: patio-runtime
            args:
              - '--instance-info={"data":{"port":8000,"worker_type":"decode"},"topo_type":"sglang"}'

  engineConfig:
    tensorParallelSize: 2
    kvTransfer:
      backend: nixl             # mooncake | nixl | nccl

    # extraArgs 是参数主体：收录该场景下经过验证的最优参数组合
    extraArgs:
      prefill:
        - "--trust-remote-code"
        - "--disable-radix-cache"
        - "--tp-size=2"
        - "--disaggregation-bootstrap-port=34000"
        - "--mem-fraction-static=0.88"
        - "--chunked-prefill-size=8192"
        - "--page-size=128"
        - "--cuda-graph-max-bs=256"
      decode:
        - "--trust-remote-code"
        - "--disable-radix-cache"
        - "--tp-size=2"
        - "--mem-fraction-static=0.88"
        - "--chunked-prefill-size=8192"
        - "--page-size=128"
        - "--cuda-graph-max-bs=256"
      scheduler:
        - "--pd-disaggregation"
        - "--policy=random"
        - "--prometheus-host=0.0.0.0"
        - "--prometheus-port=9090"
```

---

## 六、REST API 规格

Base URL: `http://<pd-manager-service>/api/v1`

### 创建实例

```
POST /pd-inference-services
Content-Type: application/json

Request Body: PDInferenceService spec（同 CRD spec 字段）

Response 201:
{
  "name": "qwen2-72b",
  "namespace": "default",
  "phase": "Pending",
  "createdAt": "2026-03-06T10:00:00Z"
}

Response 400: 校验失败（字段缺失或枚举值非法）
Response 409: 同名实例已存在
```

### 查询列表

```
GET /pd-inference-services?namespace=default

Response 200:
{
  "items": [
    {
      "name": "qwen2-72b",
      "namespace": "default",
      "phase": "Running",
      "endpoint": "http://10.0.0.1:30080",
      "prefillReplicas": 1,
      "decodeReplicas": 2,
      "createdAt": "2026-03-06T10:00:00Z"
    }
  ]
}
```

### 查询单个

```
GET /pd-inference-services/{name}?namespace=default

Response 200:
{
  "name": "qwen2-72b",
  "namespace": "default",
  "phase": "Running",
  "endpoint": "http://10.0.0.1:30080",
  "conditions": [...],
  "roleStatuses": [...],
  "lastEvent": {...},
  "spec": {...}
}

Response 404: 实例不存在
```

### 更新（仅支持副本数）

```
PUT /pd-inference-services/{name}
Content-Type: application/json

Request Body:
{
  "prefill": { "replicas": 2 },   // 可选
  "decode": { "replicas": 4 }     // 可选
}

Response 200: 更新成功，返回当前状态
Response 400: 副本数非法（< 1）
Response 404: 实例不存在
```

### 删除实例

```
DELETE /pd-inference-services/{name}?namespace=default

Response 200: 删除请求已接受，后台异步执行
Response 404: 实例不存在
```

---

## 七、配置示例

### 示例一：最小化配置（使用 Profile，无需配置镜像）

```yaml
apiVersion: pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: qwen3-14b-minimal
spec:
  model: Qwen/Qwen3-14B
  modelStorage:
    type: hostPath
    hostPath: /data/model/qwen3-14b
  prefill:
    replicas: 1
    resources:
      gpu: "2"
      gpuType: A30
  decode:
    replicas: 2
    resources:
      gpu: "2"
      gpuType: A30
  engineProfileRef: a30-nixl-qwen3-14b
```

### 示例二：不使用 Profile，直接配置（images 为必填）

```yaml
apiVersion: pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: qwen3-14b-standalone
spec:
  model: Qwen/Qwen3-14B
  modelStorage:
    type: hostPath
    hostPath: /data/model/qwen3-14b
  images:
    scheduler: lmsysorg/sgl-model-gateway:v0.3.1
    prefill: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    decode: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
  prefill:
    replicas: 1
    resources:
      gpu: "2"
      gpuType: A30
  decode:
    replicas: 2
    resources:
      gpu: "2"
      gpuType: A30
  engineConfig:
    tensorParallelSize: 2
    kvTransfer:
      backend: nixl
    extraArgs:
      prefill:
        - "--trust-remote-code"
        - "--disable-radix-cache"
        - "--tp-size=2"
        - "--disaggregation-bootstrap-port=34000"
        - "--mem-fraction-static=0.88"
        - "--chunked-prefill-size=8192"
      decode:
        - "--trust-remote-code"
        - "--disable-radix-cache"
        - "--tp-size=2"
        - "--mem-fraction-static=0.88"
      scheduler:
        - "--pd-disaggregation"
        - "--policy=random"
```

### 示例三：通过 REST API 创建（curl）

```bash
curl -X POST http://pd-manager:8080/api/v1/pd-inference-services \
  -H "Content-Type: application/json" \
  -d '{
    "metadata": {"name": "test-service", "namespace": "default"},
    "spec": {
      "model": "Qwen/Qwen3-14B",
      "modelStorage": {"type": "hostPath", "hostPath": "/data/model/qwen3-14b"},
      "images": {
        "scheduler": "lmsysorg/sgl-model-gateway:v0.3.1",
        "prefill": "lmsysorg/sglang:v0.5.8-cu130-amd64-runtime",
        "decode": "lmsysorg/sglang:v0.5.8-cu130-amd64-runtime"
      },
      "prefill": {"replicas": 1, "resources": {"gpu": "2", "gpuType": "A30"}},
      "decode": {"replicas": 2, "resources": {"gpu": "2", "gpuType": "A30"}},
      "engineConfig": {
        "tensorParallelSize": 2,
        "kvTransfer": {"backend": "nixl"}
      }
    }
  }'

# 等待 Ready
watch curl -s http://pd-manager:8080/api/v1/pd-inference-services/test-service | jq .phase
```

---

## 八、错误状态与排查

| 错误场景 | Status.phase | Condition reason | 用户操作 |
|---------|-------------|-----------------|---------|
| GPU 资源不足，Pod Pending | `Initializing` | `InsufficientGPU` | 减少副本数或等待资源释放 |
| 镜像拉取失败 | `Failed` | `ImagePullFailed` | 检查镜像名称和仓库权限 |
| SGLang 启动参数错误（CrashLoop） | `Failed` | `ContainerCrashLoop` | 检查 extraArgs 参数合法性 |
| Profile 不存在 | `Failed` | `ProfileNotFound` | 检查 engineProfileRef 名称 |
| 未引用 Profile 且 images 字段缺失 | `Rejected`（Webhook） | — | 补全 images.scheduler/prefill/decode |
| modelStorage 未配置 | `Rejected`（Webhook） | — | 补全 modelStorage.type 和路径 |
| RBG Operator 未安装 | `Failed` | `RBGCRDNotFound` | 联系平台运维安装 RBG Operator |

**重要**：pd-manager v1 **不自动重试**，所有失败状态需用户修改配置后重新创建或更新。

---

## 九、v2 预留扩展点

以下字段在 v1 中解析存储但不生效，为 v2 平滑升级预留：

| 字段 | v1 行为 | v2 行为 |
|------|--------|--------|
| `pdRatio` | 存储，不强制联动 | HPA 触发时自动联动 prefill 副本 |
| `scaling.decode.minReplicas/maxReplicas` | 存储，不创建 HPA | 自动创建 HPA 指向 RBGSA |
| `modelStorage.type: pvc` | 不支持，仅 hostPath | 支持 PVC 挂载 |
| `metadata.namespace` | 支持，但无隔离策略 | 结合 AICP 平台 Namespace 隔离 |

---

## 十、非功能性要求

| 指标 | v1 目标 |
|------|--------|
| Reconcile 延迟 | 资源变更后 < 5s 更新 Status |
| 并发实例数 | 支持 20+ PDInferenceService 同时运行 |
| API 响应时间 | GET/DELETE < 500ms，POST/PUT < 2s |
| 可用性 | pd-manager 控制器 crash 后自动重启，不影响已运行实例 |
