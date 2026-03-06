# pd-manager 技术方案

> 版本：v1.0
> 状态：设计中
> 关联文档：[产品规范](../specs.md) | [引擎配置设计](engine-config.md)

---

## 一、设计思路

### 核心问题

PD（Prefill-Decode）分离推理需要同时管理三种角色（scheduler/prefill/decode）的 Pod，每个角色有各自的 SGLang 启动参数、镜像、副本数和资源配置，且角色之间存在依赖顺序和 KV Cache 传输链路。直接操作 Kubernetes Deployment 管理这三个角色过于复杂，且无法原生表达角色间的依赖和协调关系。

### 分层抽象策略

pd-manager 采用**翻译层 + 底层委托**的架构：

- **上层（用户接口）**：提供简洁的 `PDInferenceService` CRD，屏蔽所有 RBG 细节。用户只需声明模型名、副本数、资源和参数模板，pd-manager 完成剩余工作。
- **下层（委托 RBG）**：所有工作负载编排（Pod 调度、角色依赖、滚动升级、缩容协调）均委托给 RBG（RoleBasedGroup）Operator，pd-manager 不重复实现。
- **翻译核心**：pd-manager 的核心价值在于将 PDInferenceService 字段**精确翻译**为 RBG RoleBasedGroup 规格，并将 RBG Status **聚合**回 PDInferenceService Status。

### 设计原则

| 原则 | 体现 |
|------|------|
| 单一职责 | pd-manager 只做翻译和状态聚合，不重复实现工作负载管理 |
| 引擎抽象 | Strategy Pattern 支持多推理引擎（v1 sglang，v2 vllm） |
| 参数透明 | extraArgs 透传不解析，避免 CRD 追逐 SGLang 版本 |
| 级联安全 | ownerReference 确保 PDInferenceService 删除时所有下层资源自动清理 |
| 配置分层 | 三级优先级：pd-manager 托管 > 用户 inline > Profile 模板 |

---

## 二、拓扑结构

### 请求链路拓扑

```
推理客户端
    │  OpenAI API /v1/completions
    ▼
sgl-router（scheduler Pod）
    │  cache-aware / round-robin 路由
    ├──▶ prefill Pod 0  ──KV Cache (RDMA/nixl)──▶ decode Pod 0
    ├──▶ prefill Pod 1  ──KV Cache (RDMA/nixl)──▶ decode Pod 1
    └──▶ prefill Pod N  ──KV Cache (RDMA/nixl)──▶ decode Pod N
```

### 控制面拓扑

```
用户
├── kubectl apply PDInferenceService
└── REST API POST /pd-inference-services
          │
          ▼
    pd-manager Operator（pd-system ns）
    ├── Reconciler（controller-runtime）
    │     │  Create / Update / Delete
    │     ▼
    │   RBG Operator → RoleBasedGroup
    │     ├── Role: scheduler（1副本）
    │     ├── Role: prefill（N副本）
    │     └── Role: decode（M副本）
    │
    ├── Admission Webhook（ValidatingWebhookConfiguration）
    │     └── 校验 PDInferenceService / PDEngineProfile 合法性
    │
    └── REST API Server（goroutine，:8080）
          └── 代理 Kubernetes API，面向非 K8s 用户
```

### 角色 DNS 与服务发现

RBG 为每个角色创建 Headless Service，sgl-router 通过 Label Selector 发现 prefill/decode Pod：

```bash
--prefill-selector role=prefill
--decode-selector  role=decode
```

---

## 三、部署方案

### 组件清单

| 组件 | 类型 | Namespace | 副本数 |
|------|------|-----------|--------|
| pd-manager controller | Deployment | pd-system | 1（v1），2+（v2） |
| pd-manager webhook | 内置同一进程 | pd-system | 同上 |
| pd-manager REST API | 内置同一进程（:8080） | pd-system | 同上 |
| RBG Operator | 前置依赖，独立部署 | kube-system | — |
| PDEngineProfile | CRD 资源 | pd-system | N 个 |

### 前置依赖

1. **RBG Operator** 已安装（提供 RoleBasedGroup CRD）
2. **cert-manager**（可选，用于 Webhook TLS 证书自动管理；v1 可手动配置证书）

### RBAC 权限

| 资源 | 权限 |
|------|------|
| `pdai.io` PDInferenceService | get/list/watch/update/patch/status |
| `pdai.io` PDEngineProfile | get/list/watch |
| `workloads.x-k8s.io` RoleBasedGroup | get/list/watch/create/update/patch/delete |
| `workloads.x-k8s.io` RoleBasedGroupScalingAdapter | get/list/watch |
| `autoscaling/v2` HorizontalPodAutoscaler | get/list/watch/create/update/patch/delete（v2）|
| `core` events | create/patch |

### 部署流程

```bash
# 1. 安装前置 RBG Operator
kubectl apply -f /path/to/rbg/config/

# 2. 安装 pd-manager CRD 和 RBAC
make install   # kubectl apply -f config/crd/ && kubectl apply -f config/rbac/

# 3. 部署 pd-manager
make deploy IMG=<registry>/pd-manager:<tag>

# 4. 创建 PDEngineProfile（平台运维操作）
kubectl apply -n pd-system -f config/samples/pdengineprofile-a30-nixl.yaml
```

---

## 四、架构分析

### Reconciler Pipeline（核心循环）

```
Reconcile(ctx, req) 触发条件:
  - PDInferenceService 创建/更新/删除
  - 被 pd-manager 创建的 RBG 发生变化（Watch ownerRef）

Step 1: Fetch & Finalizer
  └── Get PDInferenceService
      ├── 若正在删除 → 移除 Finalizer（ownerRef 级联删除 RBG），返回
      └── 否则 → 确保 Finalizer 存在

Step 2: Resolve & Merge Config
  └── 若有 engineProfileRef → 获取 PDEngineProfile
      └── 将 Profile.engineConfig 与 inline engineConfig 合并（三级优先级）

Step 3: Build RBG Spec
  └── 调用 translator.BuildRBG(pdis, mergedConfig)
      ├── scheduler 角色（固定 1 副本，sgl-router 镜像，路由参数）
      ├── prefill 角色（用户 replicas，sglang 镜像，PD 启动参数，模型挂载）
      └── decode 角色（用户 replicas，sglang 镜像，PD 启动参数，模型挂载）

Step 4: Apply RBG
  └── CreateOrUpdate RoleBasedGroup（ownerReference → PDInferenceService）

Step 5: Aggregate Status
  └── 读取 RBG Status → 聚合 PDInferenceService Status
      ├── 计算 phase
      ├── 填充 endpoint（scheduler Service）
      ├── 填充 roleStatuses
      └── 更新 ReadyCondition
```

### Engine Config 三级合并

```
优先级（高 → 低）：

  pd-manager 自动注入             ← 最高优先级，用户不可覆盖
  ↑ 覆盖
  PDInferenceService.engineConfig  ← 用户 inline 配置
  ↑ 覆盖
  PDEngineProfile.engineConfig     ← 平台 Profile 模板

结构化字段合并：inline 值覆盖 Profile 值
extraArgs 合并 ：append(profile.extraArgs, inline.extraArgs)
               （同名参数 inline 在后，SGLang 以最后出现为准）
```

### Status 聚合状态机

| RBG 状态 | PDInferenceService Phase |
|---------|--------------------------|
| RBG 不存在 | Pending |
| Pod 数 < 期望（调度/启动中） | Initializing |
| 所有 Pod Ready | Running |
| 任一 Pod CrashLoop ≥ 3 次 / OOMKilled | Failed |
| DeletionTimestamp 已设置 | Terminating |
| Initializing 超过 30 分钟 | Failed（StartupTimeout）|

### Webhook 职责

- **ValidatingWebhookConfiguration**：校验必填字段、枚举值、互斥字段、Profile 存在性（提交时同步拦截）
- **MutatingWebhookConfiguration**：注入默认值（`engine=sglang`、`router.strategy=round-robin`）

---

## 五、代码目录

> 使用 **kubebuilder v4** 生成项目骨架，在生成结构基础上扩展业务逻辑。

```
pd-manager/
├── api/
│   └── v1alpha1/
│       ├── pdInferenceservice_types.go    # PDInferenceService 类型定义（kubebuilder 骨架）
│       ├── pdengineprofile_types.go       # PDEngineProfile 类型定义（kubebuilder 骨架）
│       ├── groupversion_info.go           # API Group 注册（kubebuilder 生成）
│       └── zz_generated.deepcopy.go      # DeepCopy 方法（make generate 自动生成，不可手改）
│
├── cmd/
│   └── main.go                           # 进程入口（kubebuilder 生成；注册 Scheme/Controller/Webhook）
│
├── internal/
│   ├── controller/
│   │   ├── pdInferenceservice_controller.go      # 核心 Reconciler（kubebuilder 骨架 + 业务逻辑）
│   │   ├── pdInferenceservice_controller_test.go
│   │   └── suite_test.go                         # envtest 测试套件（kubebuilder 生成）
│   │
│   ├── translator/                        # 手动创建：翻译层
│   │   ├── rbg_builder.go                # 构造完整 RoleBasedGroup Spec
│   │   ├── role_builder.go               # 构造单个 RoleSpec（scheduler/prefill/decode）
│   │   └── sglang/
│   │       └── args_builder.go           # SGLang 启动参数拼装
│   │
│   ├── config/                           # 手动创建：三级 engineConfig 合并
│   │   └── merger.go
│   │
│   ├── webhook/
│   │   ├── pdInferenceservice_webhook.go  # Validating + Defaulting（kubebuilder 骨架 + 业务逻辑）
│   │   ├── pdInferenceservice_webhook_test.go
│   │   └── suite_test.go
│   │
│   └── apiserver/                         # 手动创建：REST API 服务器
│       ├── server.go                      # HTTP Server 启动（与 controller 共进程）
│       └── handler/
│           └── pdInferenceservice.go      # 5 个 HTTP Handler
│
├── config/                                # kubebuilder 生成 Kustomize 配置
│   ├── crd/                              # CRD YAML（make manifests 自动生成）
│   ├── rbac/                             # ClusterRole / ClusterRoleBinding
│   ├── webhook/                          # ValidatingWebhookConfiguration + Service
│   ├── certmanager/                      # cert-manager Certificate（可选）
│   ├── manager/                          # Deployment + ConfigMap
│   ├── samples/                          # CRD 示例 YAML（手动维护）
│   └── default/                          # Kustomization 组合入口
│
├── hack/
│   └── boilerplate.go.txt                 # License 头模板（kubebuilder 生成）
│
├── PROJECT                                # kubebuilder 项目元数据
├── Makefile                               # kubebuilder 生成（generate/manifests/build/run/deploy）
├── go.mod
└── go.sum
```

---

## 六、核心数据结构与接口

### 6.1 PDInferenceService（CRD 类型）

```go
// api/v1alpha1/pdInferenceservice_types.go

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pdis
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".status.endpoint"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type PDInferenceService struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   PDInferenceServiceSpec   `json:"spec,omitempty"`
    Status PDInferenceServiceStatus `json:"status,omitempty"`
}

type PDInferenceServiceSpec struct {
    // +kubebuilder:validation:Required
    Model string `json:"model"`

    // +kubebuilder:validation:Enum=sglang
    // +kubebuilder:default=sglang
    Engine EngineType `json:"engine,omitempty"`

    // +kubebuilder:validation:Required
    ModelStorage ModelStorageSpec `json:"modelStorage"`

    Images           *RoleImages   `json:"images,omitempty"`
    Prefill          RoleSpec      `json:"prefill"`
    Decode           RoleSpec      `json:"decode"`
    Router           *RouterSpec   `json:"router,omitempty"`
    PDRatio          string        `json:"pdRatio,omitempty"`
    EngineProfileRef string        `json:"engineProfileRef,omitempty"`
    EngineConfig     *EngineConfig `json:"engineConfig,omitempty"`
    Scaling          *ScalingSpec  `json:"scaling,omitempty"`
}

type PDInferenceServiceStatus struct {
    // +kubebuilder:validation:Enum=Pending;Initializing;Running;Failed;Terminating
    Phase        Phase              `json:"phase,omitempty"`
    Endpoint     string             `json:"endpoint,omitempty"`
    Conditions   []metav1.Condition `json:"conditions,omitempty"`
    RoleStatuses []RoleStatus       `json:"roleStatuses,omitempty"`
    LastEvent    *EventRecord       `json:"lastEvent,omitempty"`
}

type RoleSpec struct {
    // +kubebuilder:validation:Minimum=1
    // +kubebuilder:validation:Maximum=100
    Replicas  int32        `json:"replicas"`
    Resources ResourceSpec `json:"resources"`
}

type ResourceSpec struct {
    GPU     string `json:"gpu"`
    GPUType string `json:"gpuType,omitempty"`
}

type ModelStorageSpec struct {
    // +kubebuilder:validation:Enum=hostPath;pvc
    Type      StorageType `json:"type"`
    HostPath  string      `json:"hostPath,omitempty"`
    MountPath string      `json:"mountPath,omitempty"` // default: /models
}

type RoleImages struct {
    Scheduler string `json:"scheduler"`
    Prefill   string `json:"prefill"`
    Decode    string `json:"decode"`
}

type EngineConfig struct {
    TensorParallelSize *int32         `json:"tensorParallelSize,omitempty"`
    KVTransfer         *KVTransfer    `json:"kvTransfer,omitempty"`
    ExtraArgs          *RoleExtraArgs `json:"extraArgs,omitempty"`
}

type KVTransfer struct {
    // +kubebuilder:validation:Enum=mooncake;nixl;nccl
    Backend KVBackend `json:"backend"`
}

type RoleExtraArgs struct {
    Prefill   []string `json:"prefill,omitempty"`
    Decode    []string `json:"decode,omitempty"`
    Scheduler []string `json:"scheduler,omitempty"`
}

type RouterSpec struct {
    // +kubebuilder:validation:Enum=cache-aware;power-of-two;random;round-robin
    // +kubebuilder:default=round-robin
    Strategy RouterStrategy `json:"strategy,omitempty"`
}

type Phase string
const (
    PhasePending      Phase = "Pending"
    PhaseInitializing Phase = "Initializing"
    PhaseRunning      Phase = "Running"
    PhaseFailed       Phase = "Failed"
    PhaseTerminating  Phase = "Terminating"
)
```

### 6.2 PDEngineProfile（CRD 类型）

```go
// api/v1alpha1/pdengineprofile_types.go

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
type PDEngineProfile struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec PDEngineProfileSpec `json:"spec,omitempty"`
}

type PDEngineProfileSpec struct {
    Description    string              `json:"description,omitempty"`
    Applicability  *ApplicabilitySpec  `json:"applicability,omitempty"`
    Images         RoleImages          `json:"images"`
    EngineRuntimes *RoleEngineRuntimes `json:"engineRuntimes,omitempty"`
    EngineConfig   EngineConfig        `json:"engineConfig"`
}

type ApplicabilitySpec struct {
    GPUTypes              []string        `json:"gpuTypes,omitempty"`
    MinGPUMemoryGiB       *int32          `json:"minGpuMemoryGiB,omitempty"`
    TensorParallelSize    *int32          `json:"tensorParallelSize,omitempty"`
    ModelSizeRange        *ModelSizeRange `json:"modelSizeRange,omitempty"`
    OptimizedFor          string          `json:"optimizedFor,omitempty"`
    SGLangVersionRequired string          `json:"sglangVersionRequired,omitempty"`
}
```

### 6.3 内部翻译接口

```go
// internal/translator/interface.go

// RBGBuilder 将合并后的配置翻译为 RoleBasedGroup Spec
type RBGBuilder interface {
    Build(pdis *v1alpha1.PDInferenceService, config *MergedConfig) (*rbgv1alpha1.RoleBasedGroup, error)
}

// MergedConfig 是三级优先级合并后的最终有效配置
type MergedConfig struct {
    Images             v1alpha1.RoleImages
    TensorParallelSize *int32
    KVTransfer         *v1alpha1.KVTransfer
    ExtraArgs          v1alpha1.RoleExtraArgs   // 已完成 Profile + inline 追加合并
    EngineRuntimes     *v1alpha1.RoleEngineRuntimes
}

// ArgsBuilder 构造单个角色的 SGLang 完整启动参数
type ArgsBuilder interface {
    BuildArgs(role RoleType, pdis *v1alpha1.PDInferenceService, config *MergedConfig) []string
}
```

---

## 七、核心字段

### 字段-逻辑映射表

| 字段 | 逻辑作用 | 翻译结果 |
|------|---------|---------|
| `spec.model` | 模型逻辑名 | `--served-model-name` |
| `spec.modelStorage.hostPath` | 节点模型路径 | RBG `volumes.hostPath` + `volumeMounts` |
| `spec.images.*` | 各角色容器镜像 | RBG `roles[*].template.spec.containers[0].image` |
| `spec.prefill.replicas` | Prefill 副本数 | RBG `roles[prefill].replicas` |
| `spec.decode.replicas` | Decode 副本数 | RBG `roles[decode].replicas` |
| `spec.prefill.resources.gpu` | GPU 卡数 | RBG `resources.limits["nvidia.com/gpu"]` |
| `spec.prefill.resources.gpuType` | GPU 型号 | RBG nodeSelector / tolerations |
| `spec.router.strategy` | 路由策略 | scheduler 容器 `--policy` 参数 |
| `spec.pdRatio` | P:D 副本比 | v1 仅存储提示；v2 自动联动 prefill replicas |
| `spec.engineProfileRef` | Profile 引用 | 触发 Profile 查找与 config 合并 |
| `spec.engineConfig.tensorParallelSize` | 张量并行度 | `--tp-size`（并影响 GPU resource 计算）|
| `spec.engineConfig.kvTransfer.backend` | KV 传输后端 | `--disaggregation-transfer-backend` |
| `spec.engineConfig.extraArgs.*` | 各角色透传参数 | 追加到对应角色启动命令 |
| `spec.scaling.decode` | Decode HPA 配置 | v1 仅存储；v2 创建 HPA 指向 RBGSA |

### pd-manager 自动注入参数（最高优先级，不可覆盖）

| 注入参数 | 角色 | 值来源 |
|---------|------|--------|
| `--model-path <path>` | prefill / decode | `modelStorage.mountPath`（默认 `/models`）|
| `--served-model-name <name>` | prefill / decode | `spec.model` |
| `--host $(POD_IP)` | prefill / decode | K8s Downward API |
| `--port 8000` | 全部 | 固定值 |
| `--disaggregation-mode prefill` | prefill | 角色确定 |
| `--disaggregation-mode decode` | decode | 角色确定 |
| `--disaggregation-transfer-backend <x>` | prefill / decode | `kvTransfer.backend` 翻译 |

---

## 八、核心逻辑

### 8.1 Reconcile 主流程

```go
// internal/controller/pdInferenceservice_controller.go

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    var pdis v1alpha1.PDInferenceService
    if err := r.Get(ctx, req.NamespacedName, &pdis); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 处理删除
    if !pdis.DeletionTimestamp.IsZero() {
        return r.handleDeletion(ctx, &pdis)
    }
    if err := r.ensureFinalizer(ctx, &pdis); err != nil {
        return ctrl.Result{}, err
    }

    // 解析并合并 Profile + inline engineConfig
    mergedConfig, err := r.configMerger.Resolve(ctx, &pdis)
    if err != nil {
        return r.setFailedStatus(ctx, &pdis, "ProfileResolveFailed", err.Error())
    }

    // 构造 RBG Spec
    desiredRBG, err := r.rbgBuilder.Build(&pdis, mergedConfig)
    if err != nil {
        return r.setFailedStatus(ctx, &pdis, "RBGBuildFailed", err.Error())
    }

    // CreateOrUpdate RBG（设置 ownerReference）
    if err := r.applyRBG(ctx, &pdis, desiredRBG); err != nil {
        return ctrl.Result{}, err
    }

    // 聚合 RBG Status → PDInferenceService Status
    return r.syncStatus(ctx, &pdis)
}
```

### 8.2 Config 合并（三级优先级）

```go
// internal/config/merger.go

func (m *Merger) Resolve(ctx context.Context, pdis *v1alpha1.PDInferenceService) (*MergedConfig, error) {
    config := &MergedConfig{}

    // 层级 3（最低）：从 Profile 加载基础配置
    if pdis.Spec.EngineProfileRef != "" {
        profile, err := m.getProfile(ctx, pdis.Spec.EngineProfileRef)
        if err != nil {
            return nil, err
        }
        config.applyProfile(profile)
    }

    // 层级 2：inline 结构化字段覆盖 Profile
    if ec := pdis.Spec.EngineConfig; ec != nil {
        if ec.TensorParallelSize != nil {
            config.TensorParallelSize = ec.TensorParallelSize
        }
        if ec.KVTransfer != nil {
            config.KVTransfer = ec.KVTransfer
        }
        // extraArgs：Profile 在前，inline 追加在后
        if ec.ExtraArgs != nil {
            config.ExtraArgs.Prefill   = append(config.ExtraArgs.Prefill,   ec.ExtraArgs.Prefill...)
            config.ExtraArgs.Decode    = append(config.ExtraArgs.Decode,    ec.ExtraArgs.Decode...)
            config.ExtraArgs.Scheduler = append(config.ExtraArgs.Scheduler, ec.ExtraArgs.Scheduler...)
        }
    }

    // inline 镜像部分覆盖 Profile 镜像
    if img := pdis.Spec.Images; img != nil {
        config.mergeImages(img)
    }

    return config, nil
}
```

### 8.3 SGLang 参数构造

```go
// internal/translator/sglang/args_builder.go

func (b *ArgsBuilder) BuildArgs(role RoleType, pdis *v1alpha1.PDInferenceService, cfg *MergedConfig) []string {
    var args []string

    // 层级 1：pd-manager 托管参数（最高优先级，不可被 extraArgs 覆盖）
    args = append(args,
        "--model-path", mountPath(pdis.Spec.ModelStorage),
        "--served-model-name", pdis.Spec.Model,
        "--host", "$(POD_IP)",
        "--port", "8000",
    )
    switch role {
    case RolePrefill:
        args = append(args, "--disaggregation-mode", "prefill")
    case RoleDecode:
        args = append(args, "--disaggregation-mode", "decode")
    }
    if cfg.KVTransfer != nil && role != RoleScheduler {
        args = append(args, "--disaggregation-transfer-backend", string(cfg.KVTransfer.Backend))
    }

    // 层级 2 + 3：用户 extraArgs（Profile 在前，inline 在后，SGLang 以最后出现为准）
    switch role {
    case RolePrefill:
        args = append(args, cfg.ExtraArgs.Prefill...)
    case RoleDecode:
        args = append(args, cfg.ExtraArgs.Decode...)
    case RoleScheduler:
        args = append(args, cfg.ExtraArgs.Scheduler...)
    }

    return args
}
```

### 8.4 Status 聚合

```go
// internal/controller/pdInferenceservice_controller.go（syncStatus 方法）

func (r *Reconciler) syncStatus(ctx context.Context, pdis *v1alpha1.PDInferenceService) (ctrl.Result, error) {
    rbg := &rbgv1alpha1.RoleBasedGroup{}
    if err := r.Get(ctx, rbgKey(pdis), rbg); err != nil {
        pdis.Status.Phase = v1alpha1.PhasePending
        return ctrl.Result{RequeueAfter: 5 * time.Second}, r.Status().Update(ctx, pdis)
    }

    phase := computePhase(rbg, pdis)  // 实现状态机映射
    pdis.Status.Phase = phase

    if phase == v1alpha1.PhaseRunning {
        pdis.Status.Endpoint = r.resolveEndpoint(ctx, pdis)
    }

    pdis.Status.RoleStatuses = buildRoleStatuses(rbg)
    setReadyCondition(pdis, phase)

    return ctrl.Result{RequeueAfter: 10 * time.Second}, r.Status().Update(ctx, pdis)
}
```

### 8.5 Webhook 校验

```go
// internal/webhook/pdInferenceservice_webhook.go

func (w *Validator) ValidateCreate(ctx context.Context, obj runtime.Object) error {
    pdis := obj.(*v1alpha1.PDInferenceService)
    var errs field.ErrorList

    // 必填字段
    if pdis.Spec.Model == "" {
        errs = append(errs, field.Required(field.NewPath("spec", "model"), ""))
    }
    validateModelStorage(&pdis.Spec.ModelStorage, &errs)

    // images 二选一校验
    if pdis.Spec.EngineProfileRef == "" {
        validateImages(pdis.Spec.Images, &errs)
    } else {
        if err := w.validateProfileExists(ctx, pdis.Spec.EngineProfileRef); err != nil {
            errs = append(errs, err)
        }
    }

    validateReplicas(pdis.Spec.Prefill.Replicas, field.NewPath("spec", "prefill", "replicas"), &errs)
    validateReplicas(pdis.Spec.Decode.Replicas, field.NewPath("spec", "decode", "replicas"), &errs)

    // 互斥字段
    if pdis.Spec.PDRatio != "" && pdis.Spec.Scaling != nil && pdis.Spec.Scaling.Prefill != nil {
        errs = append(errs, field.Forbidden(
            field.NewPath("spec", "pdRatio"),
            "pdRatio 与 scaling.prefill 不能同时配置",
        ))
    }

    return errs.ToAggregate()
}

func (w *Validator) ValidateUpdate(ctx context.Context, old, new runtime.Object) error {
    oldPDIS := old.(*v1alpha1.PDInferenceService)
    newPDIS := new.(*v1alpha1.PDInferenceService)
    var errs field.ErrorList

    // 不可变字段校验
    immutableFields := []struct {
        name string
        old, new interface{}
    }{
        {"spec.model", oldPDIS.Spec.Model, newPDIS.Spec.Model},
        {"spec.modelStorage", oldPDIS.Spec.ModelStorage, newPDIS.Spec.ModelStorage},
        {"spec.engine", oldPDIS.Spec.Engine, newPDIS.Spec.Engine},
        {"spec.images", oldPDIS.Spec.Images, newPDIS.Spec.Images},
        {"spec.engineProfileRef", oldPDIS.Spec.EngineProfileRef, newPDIS.Spec.EngineProfileRef},
        {"spec.engineConfig", oldPDIS.Spec.EngineConfig, newPDIS.Spec.EngineConfig},
    }
    for _, f := range immutableFields {
        if !reflect.DeepEqual(f.old, f.new) {
            errs = append(errs, field.Forbidden(field.NewPath(f.name), "创建后不可修改"))
        }
    }

    return errs.ToAggregate()
}
```

---

## 九、实施阶段

### Phase 1：项目脚手架（kubebuilder 初始化）

**目标**：使用 kubebuilder 生成项目骨架，完成 CRD 类型定义和代码生成。

**kubebuilder 初始化命令**：

```bash
# 初始化项目（domain = CRD group 后缀）
kubebuilder init \
  --domain pdai.io \
  --repo github.com/<org>/pd-manager \
  --project-name pd-manager

# 创建 PDInferenceService（含 controller）
kubebuilder create api \
  --group pdai --version v1alpha1 --kind PDInferenceService \
  --resource --controller

# 创建 PDEngineProfile（仅 resource，无 controller）
kubebuilder create api \
  --group pdai --version v1alpha1 --kind PDEngineProfile \
  --resource --no-controller

# 创建 Validating + Defaulting Webhook
kubebuilder create webhook \
  --group pdai --version v1alpha1 --kind PDInferenceService \
  --defaulting --programmatic-validation
```

**填充 CRD 类型**（在 kubebuilder 骨架上）：
- 写入第六章的完整 Go 类型定义
- 添加所有 `// +kubebuilder:...` markers（validation、default、printcolumn 等）

**代码生成**：

```bash
make generate    # controller-gen 生成 DeepCopy 方法
make manifests   # controller-gen 生成 CRD / RBAC / Webhook YAML
```

**添加 RBG 依赖**：

```bash
# 方式 A：本地开发（使用 replace 指向本地 rbg 目录）
go mod edit -replace sigs.k8s.io/rbgs=/home/zzy/code/rbg
go mod tidy

# 方式 B：正式发布（发布后使用版本号）
go get sigs.k8s.io/rbgs@<version>
```

**验收**：`make build` 无编译错误；`make manifests` 生成合法 CRD YAML。

**工期估计**：2 天

---

### Phase 2：核心 Reconciler（翻译层）

**目标**：实现 PDInferenceService → RBG 的完整翻译和 CRUD 管理。

**任务**：
- [ ] `internal/config/merger.go` — 三级 engineConfig 合并
- [ ] `internal/translator/sglang/args_builder.go` — SGLang 参数拼装（含自动注入参数）
- [ ] `internal/translator/rbg_builder.go` — 构造完整 RBG Spec（三个角色）
- [ ] `internal/controller/pdInferenceservice_controller.go` — Reconcile 主流程
  - Finalizer 管理（`pdai.io/finalizer`）
  - CreateOrUpdate RBG（`ctrl.SetControllerReference` 设置 ownerReference）
  - 处理删除（级联由 ownerReference 保证，Reconciler 只需移除 Finalizer）
- [ ] 单元测试（merger、args_builder、rbg_builder）
- [ ] 集成验证：对照 a30 实际 YAML，确认生成的 RBG Spec 等价

**工期估计**：4 天

---

### Phase 3：Admission Webhook

**目标**：提交时同步校验，防止非法配置进入 Reconcile。

**任务**：
- [ ] `internal/webhook/pdInferenceservice_webhook.go` — Validating（ValidateCreate/Update）+ Defaulting
- [ ] `internal/webhook/pdengineprofile_webhook.go` — PDEngineProfile 基本校验
- [ ] `config/webhook/` — 配置证书和 WebhookConfiguration（v1 支持手动证书或 cert-manager）
- [ ] 集成测试：覆盖 specs.md 第五章所有校验规则

**工期估计**：2 天

---

### Phase 4：Status 聚合 + REST API

**目标**：将 RBG 状态聚合为 PDInferenceService Status；暴露 REST API 供非 K8s 用户调用。

**任务**：
- [ ] `syncStatus()` 实现 — Phase 计算、endpoint 解析、condition 更新
- [ ] Watch RBG（在 Controller SetupWithManager 中添加 Watches）
- [ ] `internal/apiserver/server.go` — HTTP Server，与 controller 在同一进程中以 goroutine 启动
- [ ] 5 个 API Handler（调用 K8s API，不额外存储状态）：
  - `POST /pd-inference-services`
  - `GET /pd-inference-services[/{name}]`
  - `PUT /pd-inference-services/{name}`（仅允许副本数）
  - `DELETE /pd-inference-services/{name}`
- [ ] 验收：US-01 至 US-04 所有 AC 通过

**工期估计**：3 天

---

### Phase 5：手动扩缩容 + pdRatio 提示

**目标**：PUT API 支持修改副本数；pdRatio 配置时给出 prefill 建议副本。

**任务**：
- [ ] PUT Handler 实现不可变字段校验 + replicas 修改
- [ ] 若配置了 pdRatio，PUT decode replicas 时，响应附带 `suggestedPrefillReplicas`
- [ ] 验收：US-03 所有 AC 通过

**工期估计**：1 天

---

### Phase 6（v2）：HPA 弹性 + pdRatio 自动联动

**目标**：Decode 副本自动弹性；pdRatio 自动维护 Prefill:Decode 比例。

**HPA 链路**：

```
HPA ──writes──▶ RBGSA.spec.replicas
                  │  (RBG 在 scalingAdapter.enable=true 时自动创建 RBGSA)
                  ▼
             RBG.spec.roles[decode].replicas
                  │  (pd-manager Watch RBG 变化)
                  ▼
             pd-manager 计算 prefill = decode × pdRatio
                  ▼
             RBG.spec.roles[prefill].replicas
```

**任务**：
- [ ] Reconciler 检测 `spec.scaling.decode`，为 RBG decode 角色设置 `scalingAdapter.enable: true`
- [ ] Watch RBGSA 就绪后，创建 HPA（`scaleTargetRef` 指向 RBGSA）
- [ ] Watch RBG decode 副本变化，自动更新 prefill 副本
- [ ] Admission Webhook 拒绝同时配置 pdRatio 和 scaling.prefill
- [ ] 验收：完整 HPA → RBGSA → RBG → pd-manager → RBG 联动链路

**工期估计**：5 天

---

## 十、关键依赖与风险

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| RBG API 变更 | 翻译层需同步更新 | 直接 import RBG client-go 模块，不手写类型；go.mod 锁定版本 |
| SGLang 参数变更 | 旧 Profile 失效 | extraArgs 透传设计，参数名变更只需更新 Profile，不改 CRD |
| Webhook 证书管理 | 部署门槛高 | v1 提供手动证书脚本；cert-manager 作为可选集成 |
| RBGSA 创建时序（v2） | HPA 过早创建报错 | Watch RBGSA 存在后再创建 HPA；失败时 RequeueAfter 重试 |
| RBG Finalizer 阻塞删除 | Terminating 卡住 | 在 Status 中暴露卡住原因，运维手动处理 |

---

## 十一、依赖版本

| 依赖 | 版本 |
|------|------|
| Go | 1.24+ |
| kubebuilder | v4.x |
| controller-runtime | v0.19+ |
| k8s.io/api、client-go | v0.34+ |
| RBG（sigs.k8s.io/rbgs） | 与 `/home/zzy/code/rbg` 同步 |
| SGLang 推理引擎镜像 | `lmsysorg/sglang:v0.5.8-cu130-amd64-runtime` |
| sgl-router 镜像 | `lmsysorg/sgl-model-gateway:v0.3.1` |
