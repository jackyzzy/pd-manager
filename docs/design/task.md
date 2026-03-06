# pd-manager 任务拆解（TDD）

> 版本：v1.0
> 状态：设计中
> 关联文档：[产品规范](../specs.md) | [引擎配置设计](engine-config.md) | [技术方案](plan.md)

---

## 前置：kubebuilder 初始化（不计入编号任务）

在开始任何编码任务之前，先完成项目脚手架：

```bash
# 初始化项目（在 /home/zzy/code/pd-manager 目录下）
kubebuilder init \
  --domain pdai.io \
  --repo github.com/pd-ai/pd-manager \
  --project-name pd-manager

# 创建 PDInferenceService API + Controller
kubebuilder create api \
  --group pdai \
  --version v1alpha1 \
  --kind PDInferenceService \
  --resource --controller

# 创建 PDEngineProfile API（无 controller）
kubebuilder create api \
  --group pdai \
  --version v1alpha1 \
  --kind PDEngineProfile \
  --resource --no-controller

# 创建 Validating + Defaulting Webhook
kubebuilder create webhook \
  --group pdai \
  --version v1alpha1 \
  --kind PDInferenceService \
  --defaulting --programmatic-validation

# 添加 RBG 依赖（指向本地 /home/zzy/code/rbg）
go mod edit -replace sigs.k8s.io/rbgs=/home/zzy/code/rbg

# 生成 DeepCopy + CRD YAML
make generate manifests
```

**验收**：`make build` 无编译错误，`config/crd/` 下生成了 CRD YAML。

---

## 任务列表

### T01 — PDInferenceService 类型测试

**文件**：`api/v1alpha1/pdInferenceservice_types_test.go`（新建）

**目标**：验证类型字段完整性和 JSON 序列化正确性。

**测试用例**：
1. `TestPDInferenceServiceSpec_RequiredFields`：构造最小合法 Spec（只含必填字段：model、modelStorage、images、prefill、decode），序列化为 JSON 再反序列化，确认字段一致
2. `TestPhase_Constants`：验证 Phase 枚举常量值为 "Pending"/"Initializing"/"Running"/"Failed"/"Terminating"
3. `TestKVBackend_Constants`：验证 KVBackend 枚举值为 "mooncake"/"nixl"/"nccl"
4. `TestModelStorageSpec_DefaultMountPath`：MountPath 省略时 JSON omitempty，可被外部默认值逻辑填充
5. `TestEngineConfig_NilSafe`：EngineConfig 为 nil 时整个 Spec 可正常序列化

**运行命令**：
```bash
go test ./api/v1alpha1/... -run TestPDInferenceService -v
```

---

### T02 — PDInferenceService 类型实现

**文件**：`api/v1alpha1/pdInferenceservice_types.go`（修改 kubebuilder 骨架）

**目标**：填充完整字段定义，添加 kubebuilder markers。

**实现内容**：
```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pdis
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".status.endpoint"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type PDInferenceService struct { ... }

type PDInferenceServiceSpec struct {
    Model            string            `json:"model"`
    Engine           EngineType        `json:"engine,omitempty"`
    ModelStorage     ModelStorageSpec  `json:"modelStorage"`
    Images           *RoleImages       `json:"images,omitempty"`
    Prefill          RoleSpec          `json:"prefill"`
    Decode           RoleSpec          `json:"decode"`
    Router           *RouterSpec       `json:"router,omitempty"`
    PDRatio          string            `json:"pdRatio,omitempty"`
    EngineProfileRef string            `json:"engineProfileRef,omitempty"`
    EngineConfig     *EngineConfig     `json:"engineConfig,omitempty"`
    Scaling          *ScalingSpec      `json:"scaling,omitempty"`
}

type RoleSpec struct {
    // +kubebuilder:validation:Minimum=1
    // +kubebuilder:validation:Maximum=100
    Replicas  int32        `json:"replicas"`
    Resources ResourceSpec `json:"resources"`
}

type KVTransfer struct {
    // +kubebuilder:validation:Enum=mooncake;nixl;nccl
    Backend KVBackend `json:"backend"`
}

// +kubebuilder:validation:Enum=Pending;Initializing;Running;Failed;Terminating
type Phase string
const (
    PhasePending      Phase = "Pending"
    PhaseInitializing Phase = "Initializing"
    PhaseRunning      Phase = "Running"
    PhaseFailed       Phase = "Failed"
    PhaseTerminating  Phase = "Terminating"
)
```

**验收**：`make generate manifests` 无报错，T01 测试全部通过。

---

### T03 — PDEngineProfile 类型测试

**文件**：`api/v1alpha1/pdengineprofile_types_test.go`（新建）

**目标**：验证 PDEngineProfile 字段及 EngineConfig 共享类型。

**测试用例**：
1. `TestPDEngineProfileSpec_Images_Required`：PDEngineProfileSpec 必须包含 Images 字段（非 nil/空），JSON 序列化后 images 字段存在
2. `TestApplicabilitySpec_Optional`：Applicability 为 nil 时整个 Profile Spec 可正常序列化
3. `TestEngineConfig_ExtraArgs_AllRoles`：EngineConfig.ExtraArgs 的 prefill/decode/scheduler 三个字段均为 `[]string`，可独立赋值
4. `TestRoleEngineRuntimes_JSON`：EngineRuntimes 字段可以序列化/反序列化，containers[].args 字段保留原始字符串（包含特殊字符如 `{` `}` `"`）

**运行命令**：
```bash
go test ./api/v1alpha1/... -run TestPDEngineProfile -v
```

---

### T04 — PDEngineProfile 类型实现

**文件**：`api/v1alpha1/pdengineprofile_types.go`（修改 kubebuilder 骨架）

**目标**：填充 PDEngineProfile 完整字段定义。

**实现内容**：
```go
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=pdep
type PDEngineProfile struct { ... }

type PDEngineProfileSpec struct {
    Description    string               `json:"description,omitempty"`
    Applicability  *ApplicabilitySpec   `json:"applicability,omitempty"`
    Images         RoleImages           `json:"images"`
    EngineRuntimes *RoleEngineRuntimes  `json:"engineRuntimes,omitempty"`
    EngineConfig   EngineConfig         `json:"engineConfig"`
}

type ApplicabilitySpec struct {
    GPUTypes              []string        `json:"gpuTypes,omitempty"`
    MinGPUMemoryGiB       *int32          `json:"minGpuMemoryGiB,omitempty"`
    TensorParallelSize    *int32          `json:"tensorParallelSize,omitempty"`
    ModelSizeRange        *ModelSizeRange `json:"modelSizeRange,omitempty"`
    OptimizedFor          string          `json:"optimizedFor,omitempty"`
    SGLangVersionRequired string          `json:"sglangVersionRequired,omitempty"`
}

type RoleEngineRuntimes struct {
    Prefill   []EngineRuntime `json:"prefill,omitempty"`
    Decode    []EngineRuntime `json:"decode,omitempty"`
    Scheduler []EngineRuntime `json:"scheduler,omitempty"`
}

// EngineRuntime 透传到 RBG engineRuntimes，pd-manager 不解析语义
type EngineRuntime struct {
    ProfileName string             `json:"profileName"`
    Containers  []RuntimeContainer `json:"containers,omitempty"`
}

type RuntimeContainer struct {
    Name string   `json:"name"`
    Args []string `json:"args,omitempty"`
}
```

**验收**：`make generate manifests`，T03 测试全部通过。

---

### T05 — Config Merger 测试

**文件**：`internal/config/merger_test.go`（新建）

**目标**：验证三级优先级合并逻辑（Profile + inline → MergedConfig）。

**测试用例**：
1. `TestMerge_NoProfile`：无 Profile 时，MergedConfig 直接来自 inline engineConfig
2. `TestMerge_ProfileOnly`：只有 Profile，无 inline，MergedConfig 等于 Profile 配置
3. `TestMerge_InlineOverridesStructuredFields`：inline.TensorParallelSize 覆盖 Profile.TensorParallelSize
4. `TestMerge_InlineOverridesKVTransfer`：inline.KVTransfer.Backend 覆盖 Profile.KVTransfer.Backend
5. `TestMerge_ExtraArgs_Appended`：prefill extraArgs = Profile.prefill + inline.prefill（顺序保证）
6. `TestMerge_InlineImages_PartialOverride`：inline 只指定 decode 镜像时，scheduler/prefill 继承 Profile 镜像
7. `TestMerge_ExtraArgs_SameKey_InlineLast`：Profile 有 `--mem-fraction-static=0.88`，inline 有 `--mem-fraction-static=0.95`，两者均出现但 inline 在后（SGLang 以最后为准）

**运行命令**：
```bash
go test ./internal/config/... -v
```

---

### T06 — Config Merger 实现

**文件**：`internal/config/merger.go`（新建）

**目标**：实现三级优先级合并，返回 `MergedConfig`。

**实现内容**：
```go
package config

// MergedConfig 是三级优先级合并后的最终配置
type MergedConfig struct {
    Images             v1alpha1.RoleImages
    TensorParallelSize *int32
    KVTransfer         *v1alpha1.KVTransfer
    ExtraArgs          v1alpha1.RoleExtraArgs
    EngineRuntimes     *v1alpha1.RoleEngineRuntimes
}

type Merger struct {
    client client.Client
}

func (m *Merger) Resolve(ctx context.Context, pdis *v1alpha1.PDInferenceService) (*MergedConfig, error) {
    cfg := &MergedConfig{}

    // Step 1: 从 Profile 加载基础配置
    if pdis.Spec.EngineProfileRef != "" {
        profile := &v1alpha1.PDEngineProfile{}
        if err := m.client.Get(ctx, types.NamespacedName{
            Namespace: "pd-system",
            Name:      pdis.Spec.EngineProfileRef,
        }, profile); err != nil {
            return nil, fmt.Errorf("get profile %s: %w", pdis.Spec.EngineProfileRef, err)
        }
        cfg.applyProfile(profile)
    }

    // Step 2: inline 结构化字段覆盖 Profile（指针非 nil 才覆盖）
    // Step 3: inline 镜像部分覆盖（非空字段才覆盖）
    // Step 4: extraArgs 追加合并（Profile 在前，inline 在后）
    if ec := pdis.Spec.EngineConfig; ec != nil {
        if ec.TensorParallelSize != nil {
            cfg.TensorParallelSize = ec.TensorParallelSize
        }
        if ec.KVTransfer != nil {
            cfg.KVTransfer = ec.KVTransfer
        }
        if ec.ExtraArgs != nil {
            cfg.ExtraArgs.Prefill   = append(cfg.ExtraArgs.Prefill,   ec.ExtraArgs.Prefill...)
            cfg.ExtraArgs.Decode    = append(cfg.ExtraArgs.Decode,    ec.ExtraArgs.Decode...)
            cfg.ExtraArgs.Scheduler = append(cfg.ExtraArgs.Scheduler, ec.ExtraArgs.Scheduler...)
        }
    }
    if img := pdis.Spec.Images; img != nil {
        cfg.mergeImages(img)
    }

    return cfg, nil
}
```

**验收**：T05 测试全部通过。

---

### T07 — SGLang Args Builder 测试

**文件**：`internal/translator/sglang/args_builder_test.go`（新建）

**目标**：验证各角色 SGLang 启动参数的正确拼装。

**测试用例**：
1. `TestBuildArgs_Prefill_AutoInjected`：prefill 角色必须包含 `--model-path /models`、`--served-model-name qwen3-14b`、`--host $(POD_IP)`、`--port 8000`、`--disaggregation-mode prefill`
2. `TestBuildArgs_Decode_AutoInjected`：decode 角色必须包含 `--disaggregation-mode decode`，不含 `--disaggregation-mode prefill`
3. `TestBuildArgs_Scheduler_NoDisaggregation`：scheduler 角色不含 `--disaggregation-mode` 和 `--disaggregation-transfer-backend`
4. `TestBuildArgs_KVTransfer_Backend`：cfg.KVTransfer.Backend = nixl → 参数包含 `--disaggregation-transfer-backend nixl`（prefill/decode）
5. `TestBuildArgs_ExtraArgs_AppendedAfterAutoInject`：extraArgs 出现在 auto-inject 参数之后
6. `TestBuildArgs_CustomMountPath`：modelStorage.MountPath = `/data/models` → `--model-path /data/models`
7. `TestBuildArgs_DefaultMountPath`：modelStorage.MountPath 为空 → `--model-path /models`

**运行命令**：
```bash
go test ./internal/translator/sglang/... -v
```

---

### T08 — SGLang Args Builder 实现

**文件**：`internal/translator/sglang/args_builder.go`（新建）

**目标**：实现各角色 SGLang 启动参数拼装。

**实现内容**：
```go
package sglang

type RoleType string

const (
    RolePrefill   RoleType = "prefill"
    RoleDecode    RoleType = "decode"
    RoleScheduler RoleType = "scheduler"
)

type ArgsBuilder struct{}

func (b *ArgsBuilder) BuildArgs(
    role RoleType,
    pdis *v1alpha1.PDInferenceService,
    cfg *config.MergedConfig,
) []string {
    var args []string

    // 层级 1：pd-manager 托管参数（最高优先级，始终在最前）
    mountPath := "/models"
    if pdis.Spec.ModelStorage.MountPath != "" {
        mountPath = pdis.Spec.ModelStorage.MountPath
    }
    args = append(args,
        "--model-path", mountPath,
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

    // 层级 2：用户 extraArgs（Profile + inline 追加，透传不解析）
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

**验收**：T07 测试全部通过。

---

### T09 — RBG Builder 测试

**文件**：`internal/translator/rbg_builder_test.go`（新建）

**目标**：验证 PDInferenceService → RoleBasedGroup 翻译的完整性。

**测试用例**（使用 fake client，不需要 envtest）：
1. `TestBuild_ThreeRolesCreated`：RBG 包含 scheduler/prefill/decode 三个角色
2. `TestBuild_SchedulerRole`：scheduler 角色 replicas=1，镜像为 images.Scheduler，包含 `--policy` 参数
3. `TestBuild_PrefillRole`：prefill 角色 replicas 与 pdis.Spec.Prefill.Replicas 一致，包含 GPU 资源 limit
4. `TestBuild_DecodeRole`：decode 角色 replicas 与 pdis.Spec.Decode.Replicas 一致
5. `TestBuild_ModelVolume`：hostPath 类型 modelStorage 生成正确的 Volume + VolumeMount
6. `TestBuild_OwnerReference`：生成的 RBG 有 ownerReference 指向 PDInferenceService
7. `TestBuild_GPUNodeSelector`：resources.GPUType = "A30" → RBG nodeSelector 包含 GPU 型号标签
8. `TestBuild_DownwardAPI_PodIP`：prefill/decode 容器的环境变量包含 `POD_IP` via Downward API

**运行命令**：
```bash
go test ./internal/translator/... -v
```

---

### T10 — RBG Builder 实现

**文件**：`internal/translator/rbg_builder.go`（新建）

**目标**：实现完整的 PDInferenceService → RoleBasedGroup 翻译。

**核心逻辑**：
- 调用 `sglang.ArgsBuilder.BuildArgs()` 获取各角色启动参数
- 构造三个 RoleSpec：scheduler（固定 1 副本）、prefill（用户 replicas）、decode（用户 replicas）
- 为 prefill/decode 构造 hostPath Volume + VolumeMount
- 为 prefill/decode 注入 POD_IP Downward API 环境变量
- 设置 GPU 资源 `nvidia.com/gpu: N`（N = resources.GPU）
- 若 resources.GPUType 非空，设置 nodeSelector
- 若有 EngineRuntimes，透传到 RBG roleSpec.engineRuntimes
- 设置 ownerReference（controller=true, blockOwnerDeletion=true）

**验收**：T09 测试全部通过。

---

### T11 — Admission Webhook 测试

**文件**：`internal/webhook/pdInferenceservice_webhook_test.go`（修改 kubebuilder 骨架）

**目标**：验证所有校验规则和默认值注入。

**测试用例**（使用 envtest 套件）：

*Defaulting 测试*：
1. `TestDefault_Engine`：未设置 engine → 默认注入 `sglang`
2. `TestDefault_RouterStrategy`：未设置 router.strategy → 默认注入 `round-robin`
3. `TestDefault_MountPath`：未设置 modelStorage.mountPath → 默认注入 `/models`

*Validating 创建测试*：
4. `TestValidateCreate_NoProfile_MissingImages`：无 engineProfileRef 且无 images → 拒绝，错误含 "images"
5. `TestValidateCreate_InvalidKVBackend`：kvTransfer.backend = "invalid" → 拒绝，错误含 "backend"
6. `TestValidateCreate_ProfileRef_NotFound`：engineProfileRef 指向不存在的 Profile → 拒绝
7. `TestValidateCreate_PDRatio_And_PrefillScaling_Conflict`：同时设置 pdRatio 和 scaling.prefill → 拒绝
8. `TestValidateCreate_ZeroReplicas`：prefill.replicas = 0 → 拒绝

*Validating 更新测试*：
9. `TestValidateUpdate_ImmutableModel`：更新 spec.model → 拒绝，错误含 "immutable"
10. `TestValidateUpdate_ReplicasAllowed`：只更新 prefill.replicas → 允许

**运行命令**：
```bash
go test ./internal/webhook/... -v
```

---

### T12 — Admission Webhook 实现

**文件**：`internal/webhook/pdInferenceservice_webhook.go`（修改 kubebuilder 骨架）

**目标**：实现 Defaulting + Validating Webhook。

**实现内容**：
```go
// Default 注入
func (w *PDInferenceServiceCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
    pdis := obj.(*v1alpha1.PDInferenceService)
    if pdis.Spec.Engine == "" {
        pdis.Spec.Engine = "sglang"
    }
    if pdis.Spec.Router == nil {
        pdis.Spec.Router = &v1alpha1.RouterSpec{Strategy: "round-robin"}
    }
    if pdis.Spec.ModelStorage.MountPath == "" {
        pdis.Spec.ModelStorage.MountPath = "/models"
    }
    return nil
}

// ValidateCreate 校验必填字段、images 规则、Profile 存在性、枚举值、互斥字段
func (w *PDInferenceServiceCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) { ... }

// ValidateUpdate 校验不可变字段
// 不可变：model、modelStorage、engine、images、engineProfileRef、engineConfig
// 允许修改：prefill.replicas、decode.replicas
func (w *PDInferenceServiceCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) { ... }
```

**验收**：T11 测试全部通过。

---

### T13 — Controller Reconciler 测试

**文件**：`internal/controller/pdInferenceservice_controller_test.go`（修改 kubebuilder 骨架）

**目标**：验证 Reconcile 主流程，使用 envtest 真实 K8s API Server。

**测试用例**：
1. `TestReconcile_Create_RBGCreated`：创建 PDInferenceService → RBG 被创建，名称为 `pdis-{name}`
2. `TestReconcile_Create_FinalizerAdded`：创建后 PDInferenceService 包含 Finalizer `pdai.io/finalizer`
3. `TestReconcile_Update_RBGUpdated`：更新 prefill.replicas → RBG prefill 角色 replicas 同步更新
4. `TestReconcile_Delete_RBGDeleted`：设置 DeletionTimestamp → Reconcile 移除 Finalizer，RBG 被级联删除
5. `TestReconcile_ProfileResolveFailed_StatusFailed`：engineProfileRef 指向不存在的 Profile → Status.Phase = Failed，Condition 含错误原因
6. `TestReconcile_OwnerReference`：生成的 RBG ownerReference.name = PDInferenceService.name

**运行命令**：
```bash
go test ./internal/controller/... -run TestReconcile -v
```

---

### T14 — Controller Reconciler 实现

**文件**：`internal/controller/pdInferenceservice_controller.go`（修改 kubebuilder 骨架）

**目标**：实现完整 Reconcile 主流程。

**核心实现**：
```go
const finalizer = "pdai.io/finalizer"

func (r *PDInferenceServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    var pdis v1alpha1.PDInferenceService
    if err := r.Get(ctx, req.NamespacedName, &pdis); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    if !pdis.DeletionTimestamp.IsZero() {
        return r.handleDeletion(ctx, &pdis)
    }
    if err := r.ensureFinalizer(ctx, &pdis); err != nil {
        return ctrl.Result{}, err
    }

    mergedConfig, err := r.configMerger.Resolve(ctx, &pdis)
    if err != nil {
        return r.setFailedStatus(ctx, &pdis, "ProfileResolveFailed", err.Error())
    }

    desiredRBG, err := r.rbgBuilder.Build(&pdis, mergedConfig)
    if err != nil {
        return r.setFailedStatus(ctx, &pdis, "RBGBuildFailed", err.Error())
    }
    if err := r.applyRBG(ctx, &pdis, desiredRBG); err != nil {
        return ctrl.Result{}, err
    }

    return r.syncStatus(ctx, &pdis)
}
```

**Watches 设置**（`SetupWithManager`）：
```go
return ctrl.NewControllerManagedBy(mgr).
    For(&v1alpha1.PDInferenceService{}).
    Owns(&rbgv1alpha1.RoleBasedGroup{}).
    Complete(r)
```

**验收**：T13 测试全部通过。

---

### T15 — Status 聚合测试

**文件**：`internal/controller/status_test.go`（新建）

**目标**：验证 RBG Status → PDInferenceService Status 的聚合逻辑。

**测试用例**（纯单元测试，不需要 envtest）：
1. `TestComputePhase_RBGNotFound`：RBG 不存在 → Phase = Pending
2. `TestComputePhase_PodsNotReady`：RBG 存在但 readyReplicas < totalReplicas → Phase = Initializing
3. `TestComputePhase_AllReady`：所有角色 readyReplicas == replicas → Phase = Running
4. `TestComputePhase_CrashLoop`：任一角色 Pod 有 CrashLoopBackOff 条件 → Phase = Failed
5. `TestComputePhase_StartupTimeout`：Initializing 超过 30min（通过 creationTimestamp 判断）→ Phase = Failed，reason = StartupTimeout
6. `TestComputePhase_DeletionTimestamp`：PDInferenceService.DeletionTimestamp 非零 → Phase = Terminating
7. `TestBuildRoleStatuses`：从 RBG roleStatuses 映射为 PDInferenceService roleStatuses（ready/total 正确）
8. `TestSetReadyCondition_Running`：Phase = Running → conditions[Ready].Status = True
9. `TestSetReadyCondition_Failed`：Phase = Failed → conditions[Ready].Status = False，reason 非空

**运行命令**：
```bash
go test ./internal/controller/... -run "TestComputePhase|TestBuildRoleStatuses|TestSetReadyCondition" -v
```

---

### T16 — Status 聚合实现

**文件**：`internal/controller/status.go`（新建）

**目标**：实现 Phase 计算、endpoint 解析、condition 更新。

**实现内容**：
```go
func computePhase(rbg *rbgv1alpha1.RoleBasedGroup, pdis *v1alpha1.PDInferenceService) v1alpha1.Phase {
    if !pdis.DeletionTimestamp.IsZero() {
        return v1alpha1.PhaseTerminating
    }
    if rbg == nil {
        return v1alpha1.PhasePending
    }
    if hasCrashLoop(rbg) {
        return v1alpha1.PhaseFailed
    }
    if isInitializing(rbg) && time.Since(pdis.CreationTimestamp.Time) > 30*time.Minute {
        return v1alpha1.PhaseFailed  // reason: StartupTimeout
    }
    if allRolesReady(rbg) {
        return v1alpha1.PhaseRunning
    }
    return v1alpha1.PhaseInitializing
}

func (r *PDInferenceServiceReconciler) syncStatus(ctx context.Context, pdis *v1alpha1.PDInferenceService) (ctrl.Result, error) {
    rbg := &rbgv1alpha1.RoleBasedGroup{}
    if err := r.Get(ctx, rbgKey(pdis), rbg); err != nil {
        rbg = nil
    }
    pdis.Status.Phase = computePhase(rbg, pdis)
    if pdis.Status.Phase == v1alpha1.PhaseRunning {
        pdis.Status.Endpoint = r.resolveEndpoint(ctx, pdis)
    }
    if rbg != nil {
        pdis.Status.RoleStatuses = buildRoleStatuses(rbg)
    }
    setReadyCondition(pdis, pdis.Status.Phase)
    return ctrl.Result{RequeueAfter: 10 * time.Second}, r.Status().Update(ctx, pdis)
}
```

**验收**：T15 测试全部通过。

---

### T17 — API Handler 测试

**文件**：`internal/apiserver/handler/pdInferenceservice_test.go`（新建）

**目标**：验证 5 个 HTTP Handler 的请求/响应语义，使用 fake K8s client。

**测试用例**：
1. `TestCreate_Success`：POST 合法 body → 201 Created，返回创建后的对象（含 name）
2. `TestCreate_InvalidBody`：POST 无效 JSON → 400 Bad Request
3. `TestList_Empty`：GET list，无资源 → 200，items=[]
4. `TestList_WithItems`：GET list，有 2 个资源 → 200，items 长度 2
5. `TestGet_Found`：GET /{name} → 200，返回正确的 phase/endpoint
6. `TestGet_NotFound`：GET /nonexistent → 404
7. `TestUpdate_ReplicasOnly`：PUT /{name} body 含 prefill.replicas=3 → 200，K8s 对象 prefill.replicas=3
8. `TestUpdate_ImmutableField`：PUT /{name} body 含 model 变更 → 400，错误含 "immutable"
9. `TestDelete_Success`：DELETE /{name} → 200
10. `TestDelete_Idempotent`：对已删除资源 DELETE → 200（不报 404）

**运行命令**：
```bash
go test ./internal/apiserver/handler/... -v
```

---

### T18 — API Handler 实现

**文件**：`internal/apiserver/handler/pdInferenceservice.go`（新建）

**目标**：实现 5 个 HTTP Handler，代理 Kubernetes API。

**实现内容**：
```go
type Handler struct {
    client client.Client
    scheme *runtime.Scheme
}

// POST /api/v1/pd-inference-services
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) { ... }

// GET /api/v1/pd-inference-services
func (h *Handler) List(w http.ResponseWriter, r *http.Request) { ... }

// GET /api/v1/pd-inference-services/{name}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) { ... }

// PUT /api/v1/pd-inference-services/{name}
// 只允许修改 prefill.replicas 和 decode.replicas
// 若配置了 pdRatio 且修改了 decode.replicas，响应中附带 prefill 建议值
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) { ... }

// DELETE /api/v1/pd-inference-services/{name}
// 幂等：已删除时也返回 200
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) { ... }
```

**验收**：T17 测试全部通过。

---

### T19 — API Server 测试

**文件**：`internal/apiserver/server_test.go`（新建）

**目标**：验证 HTTP Server 路由注册和启动/关闭逻辑。

**测试用例**：
1. `TestServer_Routes`：启动 Server（随机端口），对 5 个路由各发一次请求，确认 HTTP 状态码不是 404（路由注册正确）
2. `TestServer_GracefulShutdown`：调用 Stop()，Server 在 5 秒内关闭，不报错
3. `TestServer_HealthCheck`：GET `/healthz` → 200 OK

**运行命令**：
```bash
go test ./internal/apiserver/... -run TestServer -v
```

---

### T20 — API Server 实现

**文件**：`internal/apiserver/server.go`（新建）

**目标**：实现 HTTP Server，与 Controller Manager 共进程运行。

**实现内容**：
```go
type Server struct {
    addr string
    srv  *http.Server
}

func New(addr string, client client.Client, scheme *runtime.Scheme) *Server {
    h := handler.New(client, scheme)
    mux := http.NewServeMux()

    mux.HandleFunc("POST /api/v1/pd-inference-services", h.Create)
    mux.HandleFunc("GET /api/v1/pd-inference-services", h.List)
    mux.HandleFunc("GET /api/v1/pd-inference-services/{name}", h.Get)
    mux.HandleFunc("PUT /api/v1/pd-inference-services/{name}", h.Update)
    mux.HandleFunc("DELETE /api/v1/pd-inference-services/{name}", h.Delete)
    mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    })

    return &Server{addr: addr, srv: &http.Server{Addr: addr, Handler: mux}}
}

// Start 实现 manager.Runnable 接口
func (s *Server) Start(ctx context.Context) error {
    go func() { _ = s.srv.ListenAndServe() }()
    <-ctx.Done()
    return s.srv.Shutdown(context.Background())
}
```

在 `cmd/main.go` 中通过 `mgr.Add(apiServer)` 注入。

**验收**：T19 测试全部通过。

---

### T21 — Main 集成冒烟测试

**文件**：`cmd/main_test.go`（新建）

**目标**：验证 main.go 能正确组装所有组件并启动，使用 envtest 环境。

**测试用例**：
1. `TestMain_ManagerStartup`：使用 envtest 启动 Manager（包含 Controller + Webhook + API Server），30 秒内所有 Runnable 就绪，无 panic
2. `TestMain_SchemeRegistered`：Scheme 已注册 `pdai.io/v1alpha1` 和 RBG Scheme
3. `TestMain_WebhookRegistered`：Webhook Server 启动后，向 webhook 端口发 TLS 请求，返回 200（而非连接拒绝）

**运行命令**：
```bash
go test ./cmd/... -v -timeout 60s
```

---

### T22 — Main 实现

**文件**：`cmd/main.go`（修改 kubebuilder 骨架）

**目标**：组装所有组件，启动 Controller Manager + Webhook + API Server。

**实现内容**（在 kubebuilder 骨架基础上添加）：
```go
func main() {
    // 1. 解析 flags（kubebuilder 骨架已生成）
    // 2. 注册 Scheme：v1alpha1 + rbgv1alpha1
    // 3. 创建 Manager（含 Webhook Server）

    // 4. 注册 Controller
    if err := (&controller.PDInferenceServiceReconciler{
        Client:       mgr.GetClient(),
        Scheme:       mgr.GetScheme(),
        ConfigMerger: config.NewMerger(mgr.GetClient()),
        RBGBuilder:   translator.NewRBGBuilder(mgr.GetScheme()),
    }).SetupWithManager(mgr); err != nil {
        setupLog.Error(err, "unable to create controller")
        os.Exit(1)
    }

    // 5. 注册 Webhook
    if err := (&webhook.PDInferenceServiceCustomDefaulter{}).
        SetupWebhookWithManager(mgr); err != nil { ... }
    if err := (&webhook.PDInferenceServiceCustomValidator{
        Client: mgr.GetClient(),
    }).SetupWebhookWithManager(mgr); err != nil { ... }

    // 6. 注册 API Server（作为 Runnable）
    apiServer := apiserver.New(":8080", mgr.GetClient(), mgr.GetScheme())
    if err := mgr.Add(apiServer); err != nil { ... }

    // 7. 启动 Manager
    if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil { ... }
}
```

**验收**：T21 测试全部通过；`make build` 无报错；`make run`（指向真实集群）可创建并查看 PDInferenceService。

---

### T23 — 手动验收测试执行文档

**文件**：`docs/test/manual-validation.md`（新建）

**目标**：提供在 a30 环境（183.56.181.9:34451）手动端到端验证的操作手册，覆盖 US-01 到 US-04 全部验收标准。

**文档内容结构**：

```
# pd-manager 手动验收测试手册

## 环境信息
- 开发环境：本地 WSL（/home/zzy/code/pd-manager）
- 测试环境：a30（ssh a30@183.56.181.9 -p 34451，免密登录）
- 前置条件：a30 已安装 RBG Operator、kubectl 已配置、GPU 节点已标记

## 一、部署步骤

### 1.1 在 WSL 构建镜像
make docker-build IMG=<registry>/pd-manager:dev
make docker-push IMG=<registry>/pd-manager:dev

### 1.2 在 a30 部署
ssh a30@183.56.181.9 -p 34451
cd /path/to/pd-manager
kubectl apply -k config/default
kubectl rollout status deploy/pd-manager-controller-manager -n pd-system

## 二、验收用例

### US-01：创建推理服务
kubectl apply -f examples/qwen3-14b.yaml
kubectl get pdis qwen3-14b -w
# 预期：30 秒内 Phase = Initializing

### US-02：查询服务状态
kubectl get pdis qwen3-14b -o yaml
# 预期：status.phase=Running，status.endpoint 有值，三角色均 ready

### US-03：手动扩容（REST API）
curl -X PUT http://<pd-manager-svc>:8080/api/v1/pd-inference-services/qwen3-14b \
  -H 'Content-Type: application/json' \
  -d '{"spec":{"decode":{"replicas":2}}}'
# 预期：200，RBG decode replicas = 2
kubectl get pdis qwen3-14b -o jsonpath='{.spec.decode.replicas}'

### US-04：删除服务
kubectl delete pdis qwen3-14b
kubectl get rbg -l app=qwen3-14b
# 预期：RBG 级联删除，Pod 消失

## 三、异常场景验证
# 不存在的 Profile → Webhook 拒绝
kubectl apply -f examples/invalid-profile-ref.yaml
# 预期：Error from server: engineProfileRef "nonexistent" not found

# 修改不可变字段 → Webhook 拒绝
kubectl patch pdis qwen3-14b --type=merge -p '{"spec":{"model":"new-model"}}'
# 预期：The PDInferenceService "qwen3-14b" is invalid: spec.model: Forbidden: immutable

# CrashLoop → Phase = Failed
# 使用带错误启动参数的 Profile，观察 Status

## 四、清理
kubectl delete pdis --all -n default
kubectl delete -k config/default
```

**验收**：执行人能按文档完成全部用例，无需查阅其他资料。

---

### T24 — 自动化测试执行文档

**文件**：`docs/test/automated-test.md`（新建）

**目标**：说明三层自动化测试体系的执行方法，覆盖 WSL 本地单元/集成测试和 a30 集群冒烟测试。

**文档内容结构**：

```
# pd-manager 自动化测试指南

## 测试分层

| 层次 | 类型 | 执行环境 | 命令 | 耗时 |
|------|------|---------|------|------|
| L1 单元测试 | 纯函数，无 I/O | WSL | go test ./api/... ./internal/config/... ./internal/translator/... | <10s |
| L2 集成测试 | envtest（内置 API Server）| WSL | go test ./internal/... ./cmd/... | 1~3min |
| L3 冒烟测试 | 真实 k8s（a30）| a30 | make test-e2e KUBECONFIG=... | 5~10min |

## L1/L2：WSL 本地执行

### 环境准备
# 安装 envtest 二进制（Makefile 已包含）
make envtest
export KUBEBUILDER_ASSETS=$(make -s envtest)

### 执行命令
# 全量（L1 + L2）
go test ./... -v -timeout 5m

# 仅 L1（纯函数，无 envtest 依赖）
go test ./api/... ./internal/config/... ./internal/translator/... -v

# 仅 webhook 集成测试
go test ./internal/webhook/... -v

# 仅 controller 集成测试
go test ./internal/controller/... -v

# 仅 API Server 测试
go test ./internal/apiserver/... -v

# 覆盖率报告
go test ./... -coverprofile=coverage.out -timeout 5m
go tool cover -html=coverage.out -o coverage.html

## L3：a30 集群冒烟测试

### 部署 pd-manager 到 a30
# 1. WSL 构建镜像
make docker-build IMG=<registry>/pd-manager:dev
make docker-push IMG=<registry>/pd-manager:dev

# 2. SSH 到 a30 部署
ssh a30@183.56.181.9 -p 34451 "kubectl apply -k /path/to/pd-manager/config/default"
ssh a30@183.56.181.9 -p 34451 "kubectl rollout status deploy/pd-manager-controller-manager -n pd-system"

### 执行冒烟测试
# 方式 1：WSL 通过 KUBECONFIG 直连 a30
export KUBECONFIG=/path/to/a30-kubeconfig
go test ./test/e2e/... -v -timeout 10m

# 方式 2：Makefile 目标
make test-e2e KUBECONFIG=/path/to/a30-kubeconfig

### 冒烟测试覆盖点
- PDInferenceService 创建 → RBG 创建（30s 超时）
- Phase 转换：Pending → Initializing（Pod 出现）
- Webhook 拒绝非法请求（无需 GPU 即可验证）
- REST API 5 个端点可访问（HTTP 状态码正确）
- 删除 PDInferenceService → RBG 级联消失（60s 超时）

## CI/CD 集成（GitHub Actions 示例）

# .github/workflows/test.yml
on: [push, pull_request]
jobs:
  unit-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
      - name: Install envtest
        run: make envtest
      - name: Run L1+L2 tests
        run: go test ./... -timeout 5m
      - name: Upload coverage
        run: |
          go test ./... -coverprofile=coverage.out -timeout 5m
          go tool cover -func=coverage.out
```

**验收**：新成员按文档能在 30 分钟内完成 L1+L2 测试并得到清晰的通过/失败反馈。

---

## 任务汇总表

| 编号 | 类型 | 文件 | 关键测试/功能 |
|------|------|------|--------------|
| T01 | 测试 | `api/v1alpha1/pdInferenceservice_types_test.go` | 类型字段、JSON 序列化、枚举常量 |
| T02 | 实现 | `api/v1alpha1/pdInferenceservice_types.go` | 完整类型定义 + kubebuilder markers |
| T03 | 测试 | `api/v1alpha1/pdengineprofile_types_test.go` | Profile 类型、EngineRuntimes 序列化 |
| T04 | 实现 | `api/v1alpha1/pdengineprofile_types.go` | Profile 完整类型定义 |
| T05 | 测试 | `internal/config/merger_test.go` | 三级合并逻辑、extraArgs 顺序 |
| T06 | 实现 | `internal/config/merger.go` | Profile + inline → MergedConfig |
| T07 | 测试 | `internal/translator/sglang/args_builder_test.go` | 各角色参数、auto-inject、backend |
| T08 | 实现 | `internal/translator/sglang/args_builder.go` | SGLang 参数拼装 |
| T09 | 测试 | `internal/translator/rbg_builder_test.go` | 三角色 RBG、Volume、ownerRef |
| T10 | 实现 | `internal/translator/rbg_builder.go` | PDInferenceService → RBG 翻译 |
| T11 | 测试 | `internal/webhook/pdInferenceservice_webhook_test.go` | 默认值注入、校验规则、不可变字段 |
| T12 | 实现 | `internal/webhook/pdInferenceservice_webhook.go` | Defaulting + Validating Webhook |
| T13 | 测试 | `internal/controller/pdInferenceservice_controller_test.go` | Reconcile 主流程、Finalizer、RBG CRUD |
| T14 | 实现 | `internal/controller/pdInferenceservice_controller.go` | Reconciler + Watches |
| T15 | 测试 | `internal/controller/status_test.go` | Phase 计算、状态机转换 |
| T16 | 实现 | `internal/controller/status.go` | Status 聚合 + Condition 更新 |
| T17 | 测试 | `internal/apiserver/handler/pdInferenceservice_test.go` | 5 个 Handler 的 HTTP 语义 |
| T18 | 实现 | `internal/apiserver/handler/pdInferenceservice.go` | 5 个 HTTP Handler |
| T19 | 测试 | `internal/apiserver/server_test.go` | 路由注册、优雅关闭 |
| T20 | 实现 | `internal/apiserver/server.go` | HTTP Server + manager.Runnable |
| T21 | 测试 | `cmd/main_test.go` | 集成冒烟：Manager 启动、Scheme 注册 |
| T22 | 实现 | `cmd/main.go` | 组装所有组件，启动 Manager |
| T23 | 文档 | `docs/test/manual-validation.md` | a30 端到端手动验收手册 |
| T24 | 文档 | `docs/test/automated-test.md` | WSL + a30 自动化测试分层指南 |

---

## 验收标准

每对任务（T(2n-1) + T(2n)）完成后：
- `go test ./...` 无 FAIL（新增的测试全部通过）
- `make build` 无编译错误
- 不破坏已有测试

全部 24 个任务完成后：
- **L1+L2 自动化测试（WSL）**：`go test ./... -timeout 5m` 全部通过
- **L3 冒烟测试（a30）**：按 [docs/test/automated-test.md](../test/automated-test.md) 执行通过
- **手动验收（a30）**：按 [docs/test/manual-validation.md](../test/manual-validation.md) 执行 US-01 至 US-04 全部通过
