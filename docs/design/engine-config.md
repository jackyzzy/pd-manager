# SGLang Engine Configuration Design

## 背景

PD 分离推理中，prefill 和 decode 实例的 SGLang 启动参数来自三个来源：
1. pd-manager 根据架构自动注入的参数（不可覆盖）
2. 用户在 `PDInferenceService` 中显式指定的参数（有结构语义）
3. 平台或用户定义的 Profile 模板（可复用的参数预设）

## CRD 设计

### PDInferenceService（用户核心资源）

```yaml
apiVersion: pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: qwen2-72b-service
spec:
  # 模型
  model: Qwen/Qwen2-72B

  # 副本配置
  prefillReplicas: 1
  decodeReplicas: 2
  pdRatio: "1:2"          # 开启 pdRatio 后 prefillReplicas 由此推导，不可同时配置 prefill HPA

  # 引用 Profile（可选）
  engineProfileRef: h100-mooncake-72b

  # 结构化引擎配置（覆盖 Profile 中同名字段）
  engineConfig:
    tensorParallelSize: 8

    kvTransfer:
      backend: mooncake     # 影响 --kv-transfer-backend
      config:               # 原样序列化为 --kv-transfer-config JSON，pd-manager 不解析
        transport: rdma
        device: erdma0

    memFractionStatic: 0.9  # 影响 --mem-fraction-static，prefill/decode 共用

    # 透传参数，pd-manager 不解析语义，直接追加到启动命令
    # prefill/decode 可分别配置
    extraArgs:
      prefill:
        - "--chunked-prefill-size=8192"
      decode:
        - "--max-running-requests=256"

  # 弹性配置（可选，与 pdRatio 对 prefill 互斥）
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
  name: h100-mooncake-72b
  namespace: pd-system       # 平台统一管理，跨 namespace 引用
spec:
  description: "H100 + mooncake RDMA，适配 70B 级模型"

  engineConfig:
    tensorParallelSize: 8
    memFractionStatic: 0.9
    kvTransfer:
      backend: mooncake
      config:
        transport: rdma
        device: erdma0
    extraArgs:
      prefill:
        - "--chunked-prefill-size=8192"
        - "--enable-flashinfer-prefill"
      decode:
        - "--max-running-requests=256"
        - "--enable-flashinfer-sampling"
```

## 参数合并与优先级

```
pd-manager 托管参数（最高优先级，用户不可覆盖）
    ↑ 覆盖
PDInferenceService.engineConfig（用户 inline 配置）
    ↑ 覆盖
PDEngineProfile.engineConfig（模板默认值）
```

合并规则：
- 结构化字段（如 `tensorParallelSize`、`kvTransfer`）：inline 值覆盖 Profile 值
- `extraArgs`：**追加合并**，inline 的 args 追加在 Profile args 之后（不覆盖）

## pd-manager 翻译逻辑

pd-manager 将合并后的配置翻译为 SGLang 启动命令，分 prefill/decode 分别生成：

**pd-manager 托管参数（自动注入，角色相关）：**

| 角色 | 注入参数 |
|------|---------|
| prefill | `--disaggregation-mode prefill` |
| decode  | `--disaggregation-mode decode` |
| 共用    | `--model <spec.model>` `--served-model-name <metadata.name>` |

**用户配置翻译示例（prefill 角色）：**

```bash
python -m sglang.launch_server \
  # pd-manager 托管
  --model Qwen/Qwen2-72B \
  --served-model-name qwen2-72b-service \
  --disaggregation-mode prefill \
  # 结构化字段翻译
  --tensor-parallel-size 8 \
  --mem-fraction-static 0.9 \
  --kv-transfer-backend mooncake \
  --kv-transfer-config '{"transport":"rdma","device":"erdma0"}' \
  # extraArgs 透传（Profile + inline 追加）
  --chunked-prefill-size=8192 \
  --enable-flashinfer-prefill
```

## 校验规则（Admission Webhook）

| 规则 | 说明 |
|------|------|
| `pdRatio` 与 `scaling.prefill` 互斥 | 同时配置则拒绝，避免冲突 |
| `engineProfileRef` 存在性校验 | Profile 不存在则拒绝创建 |
| `kvTransfer.backend` 枚举校验 | 只允许 `mooncake` / `nccl` |
| `tensorParallelSize` 合法性 | 必须是 2 的幂次 |

## 扩展性

- **新增推理引擎**（如 vLLM）：实现新的翻译适配器，`engineConfig` 字段复用，`extraArgs` 语义不变
- **新增 KV 传输后端**：只需扩展 `kvTransfer.backend` 枚举，pd-manager 翻译层增加对应 flag
- **Profile 版本管理**：通过 label 标注适用的硬件/模型范围，由平台运维维护
