# SGLang Engine Configuration Design

## 设计哲学

PDEngineProfile 的本质是**针对特定硬件 + 模型规格场景的最优 SGLang 启动参数集合**。

由于 SGLang 的启动参数随版本迭代快速变化，PDEngineProfile 不应试图为每个参数建立结构化字段——这会导致 Profile CRD 跟不上 SGLang 的迭代速度。

**设计原则**：
- **结构化字段**：只用于少数有明确语义、需要 pd-manager 理解并处理的参数（如 `tensorParallelSize` 影响 RBG 资源计算，`kvTransfer.backend` 影响参数名选择）
- **`extraArgs`（角色级）**：是 Profile 的**主体**，收录针对该场景优化好的 SGLang 启动参数组合，对 pd-manager 透明
- Profile 的价值 = **运维专家把最优参数组合固化下来，供用户直接引用，避免用户逐条研究 SGLang 文档**

---

## 参数来源三层级

```
pd-manager 托管参数（最高优先级，用户不可覆盖）
    ↑ 覆盖
PDInferenceService.engineConfig（用户 inline 配置）
    ↑ 覆盖
PDEngineProfile.engineConfig（平台 Profile 默认值）
```

合并规则：
- 结构化字段（`tensorParallelSize`、`kvTransfer` 等）：inline 值覆盖 Profile 值
- `extraArgs`：**追加合并**，inline 的 args 追加在 Profile args 之后（不覆盖整个列表）
  - 若 inline 的某个参数与 Profile 中同名，以 inline 为准（依赖 SGLang 最后出现的参数生效的解析行为）

---

## CRD 设计

### PDInferenceService（用户核心资源）

```yaml
apiVersion: pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: qwen3-14b
spec:
  # 模型逻辑名（必填）→ --served-model-name
  model: Qwen/Qwen3-14B

  # 模型文件存储（必填）
  modelStorage:
    type: hostPath              # v1 支持 hostPath，v2 规划 pvc
    hostPath: /data/model/qwen3-14b  # 节点本地路径
    # mountPath: /models        # 容器内挂载路径，默认 /models

  # 角色镜像（与 engineProfileRef 二选一，或覆盖 Profile 中的镜像）
  # 未引用 Profile 时，三个字段均为必填
  images:
    scheduler: lmsysorg/sgl-model-gateway:v0.3.1
    prefill: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    decode: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime

  # 副本配置
  prefill:
    replicas: 1
    resources:
      gpu: "2"
      gpuType: A30
  decode:
    replicas: 1
    resources:
      gpu: "2"
      gpuType: A30

  # P:D 比例（可选，开启后 prefill 副本数由 decode × ratio 推导）
  pdRatio: "1:2"

  # 引用平台预定义 Profile（可选）
  engineProfileRef: a30-nixl-14b

  # 结构化引擎配置（覆盖 Profile 中同名字段）
  engineConfig:
    tensorParallelSize: 2

    kvTransfer:
      backend: nixl             # mooncake | nixl | nccl

    # 透传参数，pd-manager 不解析语义，直接追加到启动命令
    # prefill / decode / scheduler 可分别配置
    extraArgs:
      prefill:
        - "--mem-fraction-static=0.88"
        - "--chunked-prefill-size=8192"
      decode:
        - "--mem-fraction-static=0.88"
      scheduler:
        - "--policy=cache-aware"

  # HPA 弹性配置（可选，v2 生效）
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

### PDEngineProfile（平台预定义模板）

```yaml
apiVersion: pdai.io/v1alpha1
kind: PDEngineProfile
metadata:
  name: a30-nixl-qwen3-14b
  namespace: pd-system           # 平台统一管理，跨 namespace 引用
spec:
  description: "A30 双卡 + nixl 传输，适配 Qwen3-14B，均衡吞吐/延迟"

  # 适用范围元数据（帮助用户判断是否适用，不参与翻译逻辑）
  applicability:
    gpuTypes: [A30]
    minGpuMemoryGiB: 24          # 单卡显存要求
    tensorParallelSize: 2
    modelSizeRange:
      min: "7B"
      max: "20B"
    optimizedFor: balanced        # high-throughput | low-latency | balanced
    sglangVersionRequired: ">=0.5.8"

  # 角色镜像（必填）
  images:
    scheduler: lmsysorg/sgl-model-gateway:v0.3.1
    prefill: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    decode: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime

  # 平台 Sidecar 注入（透传到 RBG engineRuntimes，平台集成用，用户不感知）
  # 可选字段，无 Patio 的环境不填
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
    # 结构化字段（pd-manager 需要理解其语义）
    tensorParallelSize: 2

    kvTransfer:
      backend: nixl              # mooncake | nixl | nccl
      # pd-manager 将此字段翻译为 --disaggregation-transfer-backend nixl

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

## pd-manager 自动注入参数

以下参数由 pd-manager 根据上下文自动注入，**用户和运维都不需要手动填写**：

| 自动注入参数 | 角色 | 来源 |
|------------|------|------|
| `--model-path /models` | prefill / decode | modelStorage.mountPath（默认 /models） |
| `--served-model-name <name>` | prefill / decode | spec.model |
| `--host $(POD_IP)` | prefill / decode | K8s Downward API |
| `--port 8000` | 全部 | 固定值 |
| `--disaggregation-mode prefill` | prefill | 角色确定 |
| `--disaggregation-mode decode` | decode | 角色确定 |
| `--disaggregation-transfer-backend <x>` | prefill / decode | 从 kvTransfer.backend 翻译 |

---

## 翻译示例

最终 prefill 启动命令（A30 + nixl + Qwen3-14B）：

```bash
python -m sglang.launch_server \
  # pd-manager 自动注入
  --model-path /models \
  --served-model-name qwen3-14b \
  --host $(POD_IP) \
  --port 8000 \
  --disaggregation-mode prefill \
  --disaggregation-transfer-backend nixl \
  # Profile extraArgs（主体参数）
  --trust-remote-code \
  --disable-radix-cache \
  --tp-size=2 \
  --disaggregation-bootstrap-port=34000 \
  --mem-fraction-static=0.88 \
  --chunked-prefill-size=8192 \
  --page-size=128 \
  --cuda-graph-max-bs=256 \
  # inline extraArgs（追加，覆盖 Profile 中同名参数）
  --chunked-prefill-size=16384
```

---

## 镜像配置规则

镜像有两个入口，使用者根据场景选择：

| 场景 | 方式 |
|------|------|
| 无 Profile，独立配置 | `PDInferenceService.spec.images`（三个角色均为**必填**） |
| 引用 Profile | 镜像来自 Profile；`PDInferenceService.spec.images` 可省略，也可部分覆盖对应角色镜像 |

**Webhook 校验**：
- 未引用 Profile 时，`images.scheduler`、`images.prefill`、`images.decode` 均为必填
- 引用了 Profile 时，`images` 字段可选（从 Profile 继承）

---

## 校验规则（Admission Webhook）

| 规则 | 说明 |
|------|------|
| `images` 字段校验 | 未引用 Profile 时，三个镜像均为必填 |
| `modelStorage` 必填 | `type` 和对应的存储路径必须提供 |
| `pdRatio` 与 `scaling.prefill` 互斥 | 同时配置则拒绝，避免冲突 |
| `engineProfileRef` 存在性校验 | Profile 不存在则拒绝创建 |
| `kvTransfer.backend` 枚举校验 | 只允许 `mooncake` / `nixl` / `nccl` |
| `tensorParallelSize` 合法性 | 必须是正整数（建议 2 的幂次） |

---

## 扩展性

- **新增推理引擎**（如 vLLM）：实现新的翻译适配器，`engineConfig` 字段复用，`extraArgs` 语义不变
- **新增 KV 传输后端**：扩展 `kvTransfer.backend` 枚举，pd-manager 翻译层增加对应 `--disaggregation-transfer-backend` 映射
- **Profile 版本管理**：通过 `applicability` 元数据标注适用范围，由平台运维维护，用户按需选择
