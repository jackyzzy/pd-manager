# PDInferenceService API Reference

## CRD: PDInferenceService

**Group**: `pdai.pdai.io`  **Version**: `v1alpha1`  **Kind**: `PDInferenceService`  **Short name**: `pdis`

---

## spec 字段说明

| 字段 | 类型 | 必须 | 默认值 | 说明 |
|------|------|------|--------|------|
| `model` | string | ✅ | — | 模型标识符（如 `Qwen/Qwen3-14B`），用于标签和 served-model-name |
| `engine` | string | — | `sglang` | 推理引擎类型，目前仅支持 `sglang` |
| `volumes` | []VolumeSpec | — | — | 顶层共享卷，各角色通过 `volumeMounts` 按名引用 |
| `router` | RouterRoleSpec | ✅ | — | router 角色配置（原 scheduler） |
| `prefill` | InferenceRoleSpec | ✅ | — | prefill 角色配置 |
| `decode` | InferenceRoleSpec | ✅ | — | decode 角色配置 |
| `pdRatio` | string | — | — | prefill:decode 副本比（如 `"1:2"`），与 `scaling.prefill` 互斥 |
| `engineProfileRef` | string | — | — | 引用同命名空间的 PDEngineProfile 作为镜像和参数模板 |
| `scaling` | ScalingSpec | — | — | HPA 弹性扩缩容配置 |

---

## VolumeSpec

顶层卷定义，三选一（`hostPath` / `emptyDir` / `pvc`）。

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 卷名，供 `volumeMounts[].name` 引用 |
| `hostPath.path` | string | 宿主机目录路径 |
| `hostPath.type` | string | hostPath 类型（如 `Directory`），可选 |
| `emptyDir.medium` | string | 存储介质（如 `Memory`），空字符串表示磁盘 |
| `emptyDir.sizeLimit` | string | 容量上限（如 `20Gi`） |
| `pvc.claimName` | string | PersistentVolumeClaim 名称 |

---

## VolumeMountSpec

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 引用 `spec.volumes[].name` |
| `mountPath` | string | 容器内挂载路径 |
| `readOnly` | bool | 是否只读，默认 false |

---

## RouterRoleSpec

| 字段 | 类型 | 必须 | 默认值 | 说明 |
|------|------|------|--------|------|
| `image` | string | ✅* | — | 容器镜像，无 `engineProfileRef` 时必须 |
| `replicas` | int32 | — | 1 | 实例数 |
| `resources` | RoleResources | — | — | CPU / 内存 requests + limits |
| `volumeMounts` | []VolumeMountSpec | — | — | 引用 `spec.volumes` 中的卷 |
| `args` | []string | ✅* | — | 启动参数完整列表，无 profile 时必须 |
| `readinessProbe` | ProbeSpec | — | — | 就绪探针 |
| `livenessProbe` | ProbeSpec | — | — | 存活探针 |

> \* 无 `engineProfileRef` 时必须；有 profile 时可从 profile 继承。

---

## InferenceRoleSpec（prefill / decode）

| 字段 | 类型 | 必须 | 默认值 | 说明 |
|------|------|------|--------|------|
| `image` | string | ✅* | — | 容器镜像 |
| `replicas` | int32 | ✅ | — | 实例数，最小 1 |
| `gpu` | string | — | — | 每 pod 申请的 GPU 数量（如 `"2"`），写入 `nvidia.com/gpu` |
| `gpuType` | string | — | — | 调度约束：`accelerator=<gpuType>` nodeSelector |
| `resources` | RoleResources | — | — | CPU / 内存 requests + limits |
| `volumeMounts` | []VolumeMountSpec | — | — | 引用 `spec.volumes` 中的卷 |
| `args` | []string | ✅* | — | 启动参数完整列表；`$(POD_IP)` 由 Downward API 注入 |
| `readinessProbe` | ProbeSpec | — | — | 就绪探针 |
| `livenessProbe` | ProbeSpec | — | — | 存活探针 |
| `engineRuntimes` | []EngineRuntime | — | — | Patio sidecar 注入（转发给 RBG） |

---

## RoleResources

| 字段 | 类型 | 说明 |
|------|------|------|
| `requests` | map[string]string | 资源最低需求，键为 Kubernetes 标准资源名（`cpu`、`memory`） |
| `limits` | map[string]string | 资源上限 |

GPU 资源通过 `InferenceRoleSpec.gpu` 字段设置，不在 `RoleResources` 中。

---

## ProbeSpec

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `httpPath` | string | `/health` | HTTP GET 路径 |
| `port` | int32 | `8000` | 探测端口 |
| `initialDelaySeconds` | int32 | 0 | 容器启动后延迟秒数 |
| `periodSeconds` | int32 | 0 | 探测间隔秒数 |
| `timeoutSeconds` | int32 | 0 | 探测超时秒数 |
| `failureThreshold` | int32 | 0 | 连续失败次数后标记失败 |

---

## REST API

基础路径：`http://<pd-manager-svc>:8080`

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/v1/pd-inference-services` | 创建推理服务 |
| `GET` | `/api/v1/pd-inference-services` | 列出所有服务 |
| `GET` | `/api/v1/pd-inference-services/{name}` | 查询服务状态 |
| `PUT` | `/api/v1/pd-inference-services/{name}` | 更新（仅 prefill/decode replicas 可变） |
| `DELETE` | `/api/v1/pd-inference-services/{name}` | 删除服务 |

### POST 创建示例

```bash
curl -X POST http://localhost:8080/api/v1/pd-inference-services \
  -H "Content-Type: application/json" \
  -d @- << 'EOF'
{
  "apiVersion": "pdai.pdai.io/v1alpha1",
  "kind": "PDInferenceService",
  "metadata": {"name": "qwen3-14b", "namespace": "default"},
  "spec": {
    "model": "Qwen/Qwen3-14B",
    "volumes": [
      {"name": "model-storage", "hostPath": {"path": "/data/model/qwen3-14b", "type": "Directory"}},
      {"name": "dshm", "emptyDir": {"medium": "Memory", "sizeLimit": "20Gi"}}
    ],
    "router": {
      "image": "lmsysorg/sgl-model-gateway:v0.3.1",
      "replicas": 1,
      "args": ["--pd-disaggregation", "--host", "0.0.0.0", "--port", "8000", "--model-path", "/models", "--policy", "round-robin"],
      "volumeMounts": [{"name": "model-storage", "mountPath": "/models"}]
    },
    "prefill": {
      "image": "lmsysorg/sglang:v0.5.8-cu130-amd64-runtime",
      "replicas": 1,
      "gpu": "2",
      "gpuType": "a30",
      "args": ["--model-path", "/models", "--tp-size", "2", "--host", "$(POD_IP)", "--port", "8000", "--disaggregation-mode", "prefill", "--disaggregation-transfer-backend", "nixl"],
      "volumeMounts": [{"name": "model-storage", "mountPath": "/models"}, {"name": "dshm", "mountPath": "/dev/shm"}]
    },
    "decode": {
      "image": "lmsysorg/sglang:v0.5.8-cu130-amd64-runtime",
      "replicas": 1,
      "gpu": "2",
      "gpuType": "a30",
      "args": ["--model-path", "/models", "--tp-size", "2", "--host", "$(POD_IP)", "--port", "8000", "--disaggregation-mode", "decode", "--disaggregation-transfer-backend", "nixl"],
      "volumeMounts": [{"name": "model-storage", "mountPath": "/models"}, {"name": "dshm", "mountPath": "/dev/shm"}]
    }
  }
}
EOF
```

### PUT 更新副本数示例

```bash
curl -X PUT http://localhost:8080/api/v1/pd-inference-services/qwen3-14b \
  -H "Content-Type: application/json" \
  -d '{"spec": {"decode": {"replicas": 2}}}'
```

---

## PDEngineProfile 模板

通过 `engineProfileRef` 引用同命名空间的 PDEngineProfile，可为缺失的 `image` 和 `args` 字段提供默认值。

**优先级**：CR 内联字段（非空）> Profile 默认值（不拼接，CR 非空时完全覆盖）。

```yaml
apiVersion: pdai.pdai.io/v1alpha1
kind: PDEngineProfile
metadata:
  name: a30-qwen3-14b
  namespace: default
spec:
  images:
    router: lmsysorg/sgl-model-gateway:v0.3.1
    prefill: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    decode:  lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
  roleArgs:
    router:
    - --pd-disaggregation
    - --host
    - 0.0.0.0
    - --port
    - "8000"
    - --model-path
    - /models
    - --policy
    - round-robin
    prefill:
    - --model-path
    - /models
    - --tp-size
    - "2"
    - --host
    - $(POD_IP)
    - --port
    - "8000"
    - --disaggregation-mode
    - prefill
    - --disaggregation-transfer-backend
    - nixl
    decode:
    - --model-path
    - /models
    - --tp-size
    - "2"
    - --host
    - $(POD_IP)
    - --port
    - "8000"
    - --disaggregation-mode
    - decode
    - --disaggregation-transfer-backend
    - nixl
```
