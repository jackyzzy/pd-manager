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

> 平台运维团队需要为某个模型（如 Qwen3-14B）上线 PD 分离推理服务，要求稳定可靠，支持手动扩缩容。

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
1. 调用 `POST /api/v1/pd-inference-services`，传入模型名、镜像和资源需求
2. 轮询 `GET /api/v1/pd-inference-services/{name}` 等待 Running
3. 使用返回的 `endpoint` 发送推理请求
4. 验证完成，调用 `DELETE /api/v1/pd-inference-services/{name}` 销毁

#### 场景三：平台服务监控集成

> 平台监控系统需要感知所有 PDInferenceService 的状态，用于大盘展示和告警。

**操作路径**：
- 调用 `GET /api/v1/pd-inference-services` 定期轮询，或
- Watch `PDInferenceService` CRD 资源获取实时变化事件

---

## 三、用户故事与验收标准

### US-01 创建推理服务

**As** 平台运维工程师，
**I want to** 通过声明一个 `PDInferenceService` 资源，自动完成 prefill、decode、scheduler 三个角色的部署，
**So that** 我不需要手动管理 RoleBasedGroup 的底层细节。

**验收标准（正常路径）**：
- [ ] 提交合法的 PDInferenceService 后，pd-manager 在 5s 内创建对应的 RBG
- [ ] 所有角色 Pod Ready 后，`status.phase` 变为 `Running`
- [ ] `status.endpoint` 包含可访问的 Router 服务地址（格式：`http://<cluster-ip>:<port>`）
- [ ] `status.roleStatuses` 列出 scheduler / prefill / decode 三个角色及其副本数
- [ ] 创建完成后，调用 endpoint 返回 HTTP 200（推理引擎就绪）

**验收标准（异常路径）**：
- [ ] 缺少必填字段时，Webhook 拒绝请求，返回 400 及具体缺失字段提示
- [ ] 引用不存在的 Profile 时，Webhook 拒绝请求，返回 400 及 Profile 名称
- [ ] 未引用 Profile 且未配置 `images` 时，Webhook 拒绝请求
- [ ] GPU 资源不足导致 Pod Pending 时，`status.phase` 保持 `Initializing`，`status.conditions` 记录 `InsufficientGPU` 原因
- [ ] 容器 CrashLoop 时，`status.phase` 变为 `Failed`，`status.conditions` 记录 `ContainerCrashLoop` 原因
- [ ] 同名实例已存在时，返回 409

**边界条件**：
- [ ] `scheduler` 角色固定为 1 副本，不可在 spec 中配置
- [ ] `prefill.replicas` 和 `decode.replicas` 最小值为 1，最大值为 100（v1 软限制）
- [ ] 同一 namespace 内名称唯一，不同 namespace 允许同名
- [ ] 创建后，以下字段**不可修改**（变更请求返回 400）：`model`、`modelStorage`、`engine`、`images`、`engineProfileRef`、`engineConfig`

---

### US-02 查询服务状态

**As** AI 应用开发者，
**I want to** 通过 REST API 轮询服务状态，知道服务何时就绪，
**So that** 我知道什么时候可以开始发送推理请求。

**验收标准（正常路径）**：
- [ ] `GET /{name}` 返回 `phase`、`endpoint`、`roleStatuses`、`conditions`、`lastEvent`
- [ ] Status 在实际状态发生变化后 5s 内完成更新
- [ ] `phase` 枚举值及含义明确（见下方状态机章节）

**验收标准（异常路径）**：
- [ ] 实例不存在时返回 404 及友好提示
- [ ] 实例处于 `Failed` 时，`conditions` 包含可读的失败原因和首次失败时间

**边界条件**：
- [ ] 查询不会触发任何副作用（幂等）
- [ ] 列表接口 `GET /` 不包含完整 spec，仅返回摘要（name、phase、endpoint、副本数、创建时间）

---

### US-03 手动扩缩容

**As** 平台运维工程师，
**I want to** 在业务高峰前手动调整 decode 或 prefill 副本数，
**So that** 服务能快速应对更高并发。

**验收标准（正常路径）**：
- [ ] `PUT /{name}` 支持修改 `prefill.replicas` 和/或 `decode.replicas`
- [ ] 副本数变更后，底层 RBG 对应角色的 replicas 在 5s 内同步更新
- [ ] 扩容期间 `status.phase` 保持 `Running`（不中断服务）
- [ ] 缩容时已有请求不被强制中断（由 SGLang 优雅退出保证，pd-manager 不干预）
- [ ] `status.roleStatuses` 反映最新副本数

**验收标准（异常路径）**：
- [ ] 副本数 < 1 时返回 400
- [ ] 副本数 > 100 时返回 400（v1 软限制）
- [ ] PUT 请求**不允许**修改 `model`、`images`、`modelStorage`、`engineConfig` 等字段，尝试修改返回 400
- [ ] 实例处于 `Failed` 状态时，仍允许修改副本数（尝试恢复）

**边界条件**：
- [ ] 可以仅修改 prefill，或仅修改 decode，或同时修改
- [ ] 若配置了 `pdRatio`，修改 decode 副本数时，响应中给出 prefill 建议副本数（v1 仅提示，不自动修改）
- [ ] 并发 PUT 请求：以最后到达的请求为准（last-write-wins）

---

### US-04 删除推理服务

**As** AI 应用开发者，
**I want to** 调用一个 API 销毁推理服务，
**So that** 释放 GPU 资源，不产生额外费用。

**验收标准（正常路径）**：
- [ ] 调用 DELETE 后立即返回 200，后台异步执行删除
- [ ] 删除开始后 `status.phase` 变为 `Terminating`
- [ ] RBG、Pod、关联的 Service、RBGSA 等下层资源全部删除（通过 ownerReference 级联）
- [ ] 删除完成后，GET 该实例返回 404

**验收标准（异常路径）**：
- [ ] 实例不存在时返回 404
- [ ] 删除请求为幂等：对已在删除中的实例再次 DELETE，返回 200（不报错）

**边界条件**：
- [ ] 删除期间不阻塞，正在处理的推理请求由 SGLang 自然结束（pd-manager 不等待）
- [ ] v1 无强制删除机制（不支持 `--force` 或 grace period 参数）
- [ ] 如果 RBG 删除卡住（如 Finalizer 阻塞），`status.phase` 持续为 `Terminating`，用户需手动排查 RBG 状态

---

### US-05 使用 Profile 模板配置引擎参数

**As** 平台运维工程师，
**I want to** 预先定义针对特定硬件和模型的最优 SGLang 启动参数，供用户引用，
**So that** 用户不需要了解底层 SGLang flag，也能获得最优性能。

**验收标准（正常路径）**：
- [ ] 支持创建 `PDEngineProfile` CRD，包含 `images`、`applicability`、`engineRuntimes`、`extraArgs` 等字段
- [ ] `PDInferenceService` 通过 `engineProfileRef` 引用 Profile 时，镜像和 extraArgs 从 Profile 继承
- [ ] `PDInferenceService.images` 中配置的镜像覆盖 Profile 中对应角色的镜像
- [ ] `PDInferenceService.engineConfig.extraArgs` 追加在 Profile extraArgs 之后，同名参数以 inline 为准
- [ ] 未引用 Profile 时，`PDInferenceService.images` 三个字段（scheduler/prefill/decode）均为必填

**验收标准（异常路径）**：
- [ ] 引用的 Profile 不存在时，Webhook 在提交时拒绝（返回 400，不等到 Reconcile 才报错）
- [ ] Profile 被删除后，已运行的服务**不受影响**（RBG 已部署，不依赖 Profile 持续存在）
- [ ] Profile 被删除后，新建引用该 Profile 的服务被 Webhook 拒绝

**边界条件**：
- [ ] Profile 存放在 `pd-system` namespace，PDInferenceService 可跨 namespace 引用
- [ ] Profile 更新后，**不自动**更新已有服务（Profile 变更不触发 Reconcile）
- [ ] `PDEngineProfile` 无删除保护，运维需自行保证引用安全

---

### US-06 引擎参数校验

**As** 平台运维工程师，
**I want to** 在提交配置时立即得到错误提示，
**So that** 不需要等到 Pod 启动失败才发现配置问题。

**验收标准**：
- [ ] Webhook 校验失败时，返回 HTTP 400 及人类可读的错误信息，格式：`字段名: 错误原因`
- [ ] 所有校验在提交时同步完成（Webhook 同步拦截，不依赖异步 Reconcile）
- [ ] 校验规则见下方"完整校验规则表"

---

## 四、状态机定义

### Phase 枚举与含义

| Phase | 含义 | 用户动作 |
|-------|------|---------|
| `Pending` | PDInferenceService 已创建，Reconcile 尚未开始处理 | 等待 |
| `Initializing` | RBG 已创建，Pod 正在调度/启动中 | 等待；若长时间不变，检查 GPU 资源 |
| `Running` | 所有角色的 Pod 均已 Ready，endpoint 可访问 | 可发送推理请求 |
| `Failed` | 至少一个角色的 Pod 持续失败（CrashLoop / OOMKilled 等） | 检查 conditions，修复后重建 |
| `Terminating` | DELETE 已触发，资源正在清理中 | 等待；若长时间不变，检查 RBG Finalizer |

### Phase 转换条件

```
[提交] ──Webhook通过──▶ Pending
                              │ Reconcile 开始
                              ▼
                        Initializing
                         │        │
              所有Pod Ready       任一Pod持续失败
                  │                    │
                  ▼                    ▼
               Running              Failed
                  │                    │
                  └────────┬───────────┘
                      DELETE触发
                           │
                           ▼
                       Terminating
                           │
                    所有资源清理完成
                           │
                           ▼
                        (消失，GET返回404)
```

**"Running" 的判定条件**（同时满足）：
1. scheduler 角色的 Pod Ready
2. 所有 prefill 角色 Pod Ready
3. 所有 decode 角色 Pod Ready
4. scheduler Service 的 Endpoint 可解析

**"Failed" 的判定条件**（满足任一）：
- 任一角色有 Pod 连续 CrashLoop ≥ 3 次
- 任一角色有 Pod 状态为 OOMKilled
- Initializing 超过 **30 分钟**未转为 Running（超时后标记 Failed，原因 `StartupTimeout`）

**注意**：v1 **不自动重试**。Failed 状态需用户介入（修复配置后重建或手动触发，v1 不支持自动恢复）。

---

## 五、完整校验规则（Admission Webhook）

### 创建时校验

| 校验项 | 规则 | 错误示例 |
|--------|------|---------|
| `model` | 必填，非空字符串 | `model: 必填字段` |
| `modelStorage.type` | 必填，v1 只允许 `hostPath` | `modelStorage.type: 只允许 hostPath` |
| `modelStorage.hostPath` | 当 type=hostPath 时必填，非空 | `modelStorage.hostPath: 必填字段` |
| `images`（无 Profile） | 未引用 Profile 时，scheduler/prefill/decode 均必填 | `images.prefill: 未引用 Profile 时必填` |
| `engineProfileRef` | 若填写，对应 Profile 必须存在于 pd-system namespace | `engineProfileRef: Profile a30-nixl-14b 不存在` |
| `prefill.replicas` | 必填，整数，1–100 | `prefill.replicas: 必须在 1–100 之间` |
| `decode.replicas` | 必填，整数，1–100 | `decode.replicas: 必须在 1–100 之间` |
| `prefill.resources.gpu` | 必填，正整数字符串 | `prefill.resources.gpu: 必填字段` |
| `decode.resources.gpu` | 必填，正整数字符串 | `decode.resources.gpu: 必填字段` |
| `engine` | 只允许 `sglang`（v1） | `engine: v1 只支持 sglang` |
| `kvTransfer.backend` | 只允许 `mooncake` / `nixl` / `nccl` | `kvTransfer.backend: 不支持的后端 xxx` |
| `tensorParallelSize` | 正整数 | `tensorParallelSize: 必须为正整数` |
| `pdRatio` | 格式 `"N:M"`，N、M 均为正整数 | `pdRatio: 格式必须为 N:M，如 1:2` |
| `pdRatio` 与 `scaling.prefill` 互斥 | 不能同时配置（v2 预留字段） | `pdRatio 与 scaling.prefill 不能同时配置` |

### 更新时校验（PUT）

| 校验项 | 规则 |
|--------|------|
| 不可变字段 | `model`、`modelStorage`、`engine`、`images`、`engineProfileRef`、`engineConfig` 创建后不可修改，修改返回 400 |
| `prefill.replicas` | 整数，1–100 |
| `decode.replicas` | 整数，1–100 |

---

## 六、CRD 规格

### PDInferenceService

```yaml
apiVersion: pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: qwen3-14b
  namespace: default
spec:
  # 模型逻辑名（必填，创建后不可修改）→ --served-model-name
  model: Qwen/Qwen3-14B

  # 推理引擎（v1 仅支持 sglang，创建后不可修改）
  # +kubebuilder:default=sglang
  engine: sglang

  # 模型文件存储（必填，创建后不可修改）
  modelStorage:
    type: hostPath            # v1 支持 hostPath，v2 规划 pvc
    hostPath: /data/model/qwen3-14b
    # mountPath: /models      # 容器内路径，默认 /models

  # 角色镜像（未引用 Profile 时必填，创建后不可修改）
  # 引用 Profile 时可省略（继承 Profile），也可部分填写（覆盖对应角色）
  images:
    scheduler: lmsysorg/sgl-model-gateway:v0.3.1
    prefill: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    decode: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime

  # Prefill 角色配置
  prefill:
    replicas: 1              # 必填，1–100，可通过 PUT 修改
    resources:
      gpu: "2"               # GPU 卡数（必填，创建后不可修改）
      gpuType: A30           # GPU 型号，用于节点调度（创建后不可修改）

  # Decode 角色配置
  decode:
    replicas: 2              # 必填，1–100，可通过 PUT 修改
    resources:
      gpu: "2"
      gpuType: A30

  # Scheduler 角色（固定 1 副本，不可配置；资源需求由 pd-manager 内置）

  # Router 配置（可选，创建后不可修改）
  router:
    # cache-aware | power-of-two | random | round-robin
    # +kubebuilder:default=round-robin
    strategy: cache-aware

  # P:D 比例（可选，格式 "N:M"，v1 仅存储和提示，不自动联动）
  pdRatio: "1:2"

  # 引用平台预定义的引擎参数模板（可选，创建后不可修改）
  engineProfileRef: a30-nixl-14b

  # 引擎参数（可选，覆盖 Profile 中同名字段，创建后不可修改）
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

  # HPA 弹性配置（可选，v1 仅存储，v2 生效）
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

status:
  # 见状态机章节
  phase: Running

  # Router 访问地址（Running 时填充）
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

  # 最近一次事件（Phase 变化或 Scale 操作）
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

  # 适用范围元数据（供用户选择 Profile 时参考，不参与翻译或校验逻辑）
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

  # 平台 Sidecar 注入，透传到 RBG engineRuntimes（可选，无 Patio 环境不填）
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
      backend: nixl

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

## 七、REST API 规格

Base URL: `http://<pd-manager-service>/api/v1`

### POST /pd-inference-services — 创建实例

```
Request Body: PDInferenceService spec（同 CRD spec 字段）

Response 201:
{
  "name": "qwen3-14b",
  "namespace": "default",
  "phase": "Pending",
  "createdAt": "2026-03-06T10:00:00Z"
}

Response 400: 校验失败，body 示例：
{
  "error": "validation failed",
  "details": ["images.prefill: 未引用 Profile 时必填", "prefill.resources.gpu: 必填字段"]
}

Response 409: 同名实例已存在
```

### GET /pd-inference-services — 查询列表

```
Query: ?namespace=default（可选，不传则返回所有 namespace）

Response 200:
{
  "items": [
    {
      "name": "qwen3-14b",
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

### GET /pd-inference-services/{name} — 查询单个

```
Query: ?namespace=default（可选，默认 default）

Response 200:
{
  "name": "qwen3-14b",
  "namespace": "default",
  "phase": "Running",
  "endpoint": "http://10.0.0.1:30080",
  "conditions": [...],
  "roleStatuses": [...],
  "lastEvent": {...},
  "spec": {...}
}

Response 404:
{
  "error": "not found",
  "message": "PDInferenceService qwen3-14b not found in namespace default"
}
```

### PUT /pd-inference-services/{name} — 更新（仅副本数）

```
Query: ?namespace=default（可选）

Request Body（字段均可选，至少填一个）:
{
  "prefill": { "replicas": 2 },
  "decode":  { "replicas": 4 }
}

Response 200: 更新成功，返回当前完整 status
Response 400: 副本数非法 或 尝试修改不可变字段
Response 404: 实例不存在

错误示例（修改不可变字段）：
{
  "error": "immutable field",
  "message": "model cannot be modified after creation"
}
```

### DELETE /pd-inference-services/{name} — 删除实例

```
Query: ?namespace=default（可选）

Response 200: 删除请求已接受，异步执行
{
  "message": "deletion of qwen3-14b initiated"
}

Response 404: 实例不存在
Response 200（已在删除中）: 同上（幂等）
```

---

## 八、v1 交付范围

### In Scope

| 功能 | 优先级 | 说明 |
|------|--------|------|
| PDInferenceService CRUD（CRD） | P0 | 创建/查询/删除；Update 仅支持副本数 |
| REST API（无认证） | P0 | 5 个接口，内网使用 |
| PDEngineProfile CRD | P0 | 含 images / applicability / engineRuntimes |
| `images` 字段（直接配置镜像） | P0 | 无 Profile 时三个角色镜像必填 |
| `modelStorage.hostPath` 挂载 | P0 | pd-manager 翻译为 RBG volumes |
| Admission Webhook | P0 | 提交时同步校验，见校验规则表 |
| Status 聚合 | P0 | phase + endpoint + roleStatuses + conditions + lastEvent |
| 级联删除 | P0 | ownerReference 实现 |
| 失败状态暴露（不自动重试） | P0 | conditions 记录原因 |
| 手动扩缩容（PUT replicas） | P1 | prefill / decode 分别可调 |
| pdRatio 字段 | P1 | v1 存储 + 扩容时给出提示，不自动联动 |

### Out of Scope（v2+）

| 功能 | 计划版本 | 原因 |
|------|---------|------|
| HPA 自动弹性 + pdRatio 自动联动 | v2 | 需要 RBGSA 集成与联动逻辑 |
| 多租户 Namespace 隔离 | v2 | 依赖 AICP 平台接入 |
| AICP 平台 Token 认证 | v2 | 依赖 AICP 平台接入 |
| Profile 更新自动同步到运行中服务 | v2 | 涉及滚动更新策略设计 |
| `modelStorage.type: pvc` | v2 | hostPath 满足 v1 验证需求 |
| vLLM 引擎支持 | v2 | SGLang 优先验证 |
| 多集群管理 | v3 | — |

---

## 九、配置示例

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
    resources: {gpu: "2", gpuType: A30}
  decode:
    replicas: 2
    resources: {gpu: "2", gpuType: A30}
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
    resources: {gpu: "2", gpuType: A30}
  decode:
    replicas: 2
    resources: {gpu: "2", gpuType: A30}
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

## 十、错误状态与排查

| 错误场景 | Status.phase | Condition reason | 用户操作 |
|---------|-------------|-----------------|---------|
| GPU 资源不足，Pod Pending | `Initializing` | `InsufficientGPU` | 减少副本数或等待资源释放 |
| 镜像拉取失败 | `Failed` | `ImagePullFailed` | 检查镜像名称和仓库权限 |
| SGLang 启动参数错误（CrashLoop） | `Failed` | `ContainerCrashLoop` | 检查 extraArgs 参数合法性 |
| OOM 导致容器被杀 | `Failed` | `OOMKilled` | 增大 gpu 数量或调小 mem-fraction-static |
| 启动超时（30 分钟） | `Failed` | `StartupTimeout` | 检查 Pod 日志，确认 SGLang 是否卡住 |
| Profile 不存在（Webhook 通过后被删） | `Failed` | `ProfileNotFound` | 恢复 Profile 或重建服务不引用 Profile |
| 未引用 Profile 且 images 缺失 | Webhook 拒绝（不创建） | — | 补全 images.scheduler/prefill/decode |
| modelStorage 未配置 | Webhook 拒绝（不创建） | — | 补全 modelStorage 字段 |
| RBG Operator 未安装 | `Failed` | `RBGCRDNotFound` | 联系平台运维安装 RBG Operator |
| 删除卡住（Finalizer 阻塞） | `Terminating`（长时间） | — | 检查 RBG 资源状态，手动清理 Finalizer |

**重要**：pd-manager v1 **不自动重试**。所有 Failed 状态需用户介入（修改配置后删除重建）。

---

## 十一、v2 预留扩展点

以下字段在 v1 中解析存储但不生效，为 v2 平滑升级预留：

| 字段 | v1 行为 | v2 行为 |
|------|--------|--------|
| `pdRatio` | 存储；PUT scale 时给出 prefill 建议副本数 | HPA 触发时自动联动 prefill 副本 |
| `scaling.decode.*` | 存储，不创建 HPA | 自动创建 HPA 指向 RBGSA |
| `modelStorage.type: pvc` | 不支持，Webhook 拒绝 | 支持 PVC 挂载 |
| `metadata.namespace` | 支持，无隔离策略 | 结合 AICP 平台 Namespace 隔离 |

---

## 十二、非功能性要求

| 指标 | v1 目标 |
|------|--------|
| Reconcile 延迟 | 资源变更后 < 5s 更新 Status |
| Status 最大滞后 | 不超过 5s |
| 并发实例数 | 支持 20+ PDInferenceService 同时运行 |
| API 响应时间 | GET/DELETE < 500ms，POST/PUT < 2s |
| 可用性 | pd-manager 控制器 crash 后自动重启，不影响已运行实例 |
| 启动超时判定 | Initializing 超过 30 分钟自动标记 Failed |
