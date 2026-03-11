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

## REST API 访问方式

pd-manager REST API 在 pod 内监听 8080 端口。在 a30 等测试环境中，通过 `kubectl port-forward` 访问：

```bash
# 设置 pd-manager REST API 访问（18010 → pod:8080）
PD_POD=$(kubectl get pod -n pd-manager-system -l control-plane=controller-manager -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward -n pd-manager-system pod/${PD_POD} --address=0.0.0.0 18010:8080 &
```

服务创建完成后，还需要 port-forward 到 sgl-router 才能访问推理业务：

```bash
# 设置 sgl-router 业务访问（18001 → router pod:8000）
kubectl port-forward pod/<service-name>-router-0 --address=0.0.0.0 18001:8000 &

# 验证 worker 注册情况
curl http://127.0.0.1:18001/workers | jq '{prefill: .stats.prefill_count, decode: .stats.decode_count}'

# 发送推理请求
curl http://127.0.0.1:18001/v1/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "Qwen/Qwen3-14B", "prompt": "hello", "max_tokens": 10}'
```

---

## REST API 接口

基础路径：`http://localhost:18010`（pd-manager port-forward 后）

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/v1/pd-inference-services` | 创建推理服务 |
| `GET` | `/api/v1/pd-inference-services` | 列出所有服务 |
| `GET` | `/api/v1/pd-inference-services/{name}` | 查询服务状态 |
| `PUT` | `/api/v1/pd-inference-services/{name}` | 更新（仅 prefill/decode replicas 可变） |
| `DELETE` | `/api/v1/pd-inference-services/{name}` | 删除服务 |

### POST 创建示例

> **注意**：prefill/decode 必须使用 `command` + `args` 分离写法，以及 `engineRuntimes`（patio sidecar）。
> sglang 镜像的 ENTRYPOINT 是 `/opt/nvidia/nvidia_entrypoint.sh`，k8s 的 `args` 会作为参数传给它，因此启动命令必须放在 `command` 字段中。

```bash
curl -X POST http://localhost:18010/api/v1/pd-inference-services \
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
      "resources": {"requests": {"memory": "4Gi", "cpu": "4"}, "limits": {"memory": "4Gi", "cpu": "4"}},
      "volumeMounts": [{"name": "model-storage", "mountPath": "/models"}],
      "args": ["--log-level","info","--pd-disaggregation","--host","0.0.0.0","--port","8000",
               "--model-path","/models","--policy","random",
               "--prometheus-host","0.0.0.0","--prometheus-port","9090"],
      "readinessProbe": {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 30,
                         "periodSeconds": 10, "timeoutSeconds": 5, "failureThreshold": 3},
      "livenessProbe":  {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 120,
                         "periodSeconds": 30, "timeoutSeconds": 5, "failureThreshold": 3}
    },
    "prefill": {
      "image": "lmsysorg/sglang:v0.5.8-cu130-amd64-runtime",
      "replicas": 1,
      "gpu": "2",
      "gpuType": "a30",
      "resources": {"requests": {"memory": "96Gi", "cpu": "16"}, "limits": {"memory": "128Gi", "cpu": "32"}},
      "volumeMounts": [{"name": "model-storage", "mountPath": "/models"}, {"name": "dshm", "mountPath": "/dev/shm"}],
      "command": ["python3", "-m", "sglang.launch_server"],
      "args": ["--model-path","/models","--trust-remote-code","--disable-radix-cache",
               "--tp-size","2","--host","$(POD_IP)","--port","8000",
               "--disaggregation-mode","prefill","--disaggregation-transfer-backend","nixl",
               "--mem-fraction-static","0.88","--chunked-prefill-size","8192",
               "--page-size","128","--cuda-graph-max-bs","256"],
      "readinessProbe": {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 30,
                         "periodSeconds": 10, "timeoutSeconds": 5, "failureThreshold": 10},
      "livenessProbe":  {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 300,
                         "periodSeconds": 30, "timeoutSeconds": 5, "failureThreshold": 3},
      "engineRuntimes": [{
        "profileName": "sglang-pd-runtime",
        "containers": [{
          "name": "patio-runtime",
          "args": ["--instance-info={\"data\":{\"port\":8000,\"worker_type\":\"prefill\",\"bootstrap_port\":8998},\"topo_type\":\"sglang\"}"],
          "env": [{"name": "SGL_ROUTER_PORT", "value": "8000"}, {"name": "ROLE_NAME", "value": "prefill"}]
        }]
      }]
    },
    "decode": {
      "image": "lmsysorg/sglang:v0.5.8-cu130-amd64-runtime",
      "replicas": 1,
      "gpu": "2",
      "gpuType": "a30",
      "resources": {"requests": {"memory": "96Gi", "cpu": "16"}, "limits": {"memory": "128Gi", "cpu": "32"}},
      "volumeMounts": [{"name": "model-storage", "mountPath": "/models"}, {"name": "dshm", "mountPath": "/dev/shm"}],
      "command": ["python3", "-m", "sglang.launch_server"],
      "args": ["--model-path","/models","--trust-remote-code","--disable-radix-cache",
               "--tp-size","2","--host","$(POD_IP)","--port","8000",
               "--disaggregation-mode","decode","--disaggregation-transfer-backend","nixl",
               "--mem-fraction-static","0.88","--chunked-prefill-size","8192",
               "--page-size","128","--cuda-graph-max-bs","256"],
      "readinessProbe": {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 30,
                         "periodSeconds": 10, "timeoutSeconds": 5, "failureThreshold": 10},
      "livenessProbe":  {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 480,
                         "periodSeconds": 30, "timeoutSeconds": 5, "failureThreshold": 3},
      "engineRuntimes": [{
        "profileName": "sglang-pd-runtime",
        "containers": [{
          "name": "patio-runtime",
          "args": ["--instance-info={\"data\":{\"port\":8000,\"worker_type\":\"decode\"},\"topo_type\":\"sglang\"}"],
          "env": [{"name": "SGL_ROUTER_PORT", "value": "8000"}, {"name": "ROLE_NAME", "value": "decode"}]
        }]
      }]
    }
  }
}
EOF
```

### PUT 更新副本数示例

```bash
curl -X PUT http://localhost:18010/api/v1/pd-inference-services/qwen3-14b \
  -H "Content-Type: application/json" \
  -d '{"spec": {"decode": {"replicas": 2}}}'
```

---

## PDEngineProfile 配置模板

### 概念说明

PDEngineProfile 是**推理引擎配置模板**，用于固化特定硬件 + 模型组合下已优化的启动参数（镜像版本、engine args），
供平台团队统一维护、业务用户快速复用。

> **注意**：PDEngineProfile 与 `engineRuntimes`（patio sidecar）是正交的两个概念：
> - **PDEngineProfile**：存放推理引擎的启动参数和镜像（`roleArgs`、`images`），是面向用户的配置复用机制
> - **engineRuntimes（patio）**：基础设施层的 sidecar，负责将 prefill/decode worker 动态注册到 router，与 engine 参数无关；可以内联在 PDIS 中，也可以放在 Profile 的 `engineRuntimes` 字段中统一管理

### PDEngineProfile REST API

基础路径：`http://localhost:18010`（pd-manager port-forward 后）

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/pd-engine-profiles` | 列出所有配置模板 |
| `POST` | `/api/v1/pd-engine-profiles` | 创建配置模板 |
| `GET` | `/api/v1/pd-engine-profiles/{name}` | 查询单个模板 |
| `PUT` | `/api/v1/pd-engine-profiles/{name}` | 更新模板（全量替换 spec，所有字段可变） |
| `DELETE` | `/api/v1/pd-engine-profiles/{name}` | 删除模板（幂等） |

> **注意**：REST API 固定操作 `default` namespace。

### PDEngineProfile 字段参考

| 字段 | 类型 | 说明 |
|------|------|------|
| `spec.description` | string | 人可读描述，说明该模板适用的硬件/模型场景 |
| `spec.applicability` | ApplicabilitySpec | 适用条件（GPU 类型、内存要求等），仅作参考，pd-manager 不自动匹配 |
| `spec.images` | RoleImages | 三角色容器镜像（`router`、`prefill`、`decode`）；PDIS 内联 image 为空时使用模板值 |
| `spec.roleArgs` | RoleArgs | 三角色启动参数（`router`、`prefill`、`decode`）；PDIS 内联 args 为空时使用模板值 |
| `spec.engineRuntimes` | RoleEngineRuntimes | patio sidecar 配置（`prefill`、`decode`）；PDIS 内联 engineRuntimes 为空时使用模板值 |

**覆盖优先级**：PDIS 内联字段（非空）完全覆盖 Profile 默认值，不进行合并拼接。

### 配置模板内容（A30 GPU Qwen3-14B 场景）

通过 `engineProfileRef` 引用同命名空间的 PDEngineProfile，可为缺失的 `image`、`args` 和 `engineRuntimes` 字段提供默认值。

**优先级**：CR 内联字段（非空）> Profile 默认值（不拼接，CR 非空时完全覆盖）。

```yaml
apiVersion: pdai.pdai.io/v1alpha1
kind: PDEngineProfile
metadata:
  name: sglang-a30-qwen3-14b
  namespace: default
spec:
  description: "SGLang PD disaggregated inference on A30 GPU (2-GPU TP) for Qwen3-14B"
  images:
    router: lmsysorg/sgl-model-gateway:v0.3.1
    prefill: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    decode:  lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
  roleArgs:
    router:
    - --log-level
    - info
    - --pd-disaggregation
    - --host
    - 0.0.0.0
    - --port
    - "8000"
    - --model-path
    - /models
    - --policy
    - random
    - --prometheus-host
    - 0.0.0.0
    - --prometheus-port
    - "9090"
    prefill:
    - --model-path
    - /models
    - --trust-remote-code
    - --disable-radix-cache
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
    - --mem-fraction-static
    - "0.88"
    - --chunked-prefill-size
    - "8192"
    - --page-size
    - "128"
    - --cuda-graph-max-bs
    - "256"
    decode:
    - --model-path
    - /models
    - --trust-remote-code
    - --disable-radix-cache
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
    - --mem-fraction-static
    - "0.88"
    - --chunked-prefill-size
    - "8192"
    - --page-size
    - "128"
    - --cuda-graph-max-bs
    - "256"
  engineRuntimes:
    prefill:
    - profileName: sglang-pd-runtime
      containers:
      - name: patio-runtime
        args:
        - '--instance-info={"data":{"port":8000,"worker_type":"prefill","bootstrap_port":8998},"topo_type":"sglang"}'
        env:
        - name: SGL_ROUTER_PORT
          value: "8000"
        - name: ROLE_NAME
          value: prefill
    decode:
    - profileName: sglang-pd-runtime
      containers:
      - name: patio-runtime
        args:
        - '--instance-info={"data":{"port":8000,"worker_type":"decode"},"topo_type":"sglang"}'
        env:
        - name: SGL_ROUTER_PORT
          value: "8000"
        - name: ROLE_NAME
          value: decode
```

### 使用配置模板创建推理服务（kubectl）

引用模板的 PDIS 只需提供硬件相关字段（replicas、gpu、gpuType、resources、volumeMounts、command、probes），镜像、args、engineRuntimes 全部由模板提供：

```yaml
apiVersion: pdai.pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: qwen3-14b
  namespace: default
spec:
  model: Qwen/Qwen3-14B
  engineProfileRef: sglang-a30-qwen3-14b   # 引用上面的配置模板

  volumes:
  - name: model-storage
    hostPath:
      path: /data/model/qwen3-14b
      type: Directory
  - name: dshm
    emptyDir:
      medium: Memory
      sizeLimit: 20Gi

  router:
    replicas: 1
    resources:
      requests:
        memory: 4Gi
        cpu: "4"
      limits:
        memory: 4Gi
        cpu: "4"
    volumeMounts:
    - name: model-storage
      mountPath: /models
    readinessProbe:
      httpPath: /health
      port: 8000
      initialDelaySeconds: 30
      periodSeconds: 10
      timeoutSeconds: 5
      failureThreshold: 3
    livenessProbe:
      httpPath: /health
      port: 8000
      initialDelaySeconds: 120
      periodSeconds: 30
      timeoutSeconds: 5
      failureThreshold: 3

  prefill:
    replicas: 1
    gpu: "2"
    gpuType: a30
    resources:
      requests:
        memory: 96Gi
        cpu: "16"
      limits:
        memory: 128Gi
        cpu: "32"
    volumeMounts:
    - name: model-storage
      mountPath: /models
    - name: dshm
      mountPath: /dev/shm
    command:
    - python3
    - -m
    - sglang.launch_server
    readinessProbe:
      httpPath: /health
      port: 8000
      initialDelaySeconds: 30
      periodSeconds: 10
      timeoutSeconds: 5
      failureThreshold: 10
    livenessProbe:
      httpPath: /health
      port: 8000
      initialDelaySeconds: 300
      periodSeconds: 30
      timeoutSeconds: 5
      failureThreshold: 3

  decode:
    replicas: 1
    gpu: "2"
    gpuType: a30
    resources:
      requests:
        memory: 96Gi
        cpu: "16"
      limits:
        memory: 128Gi
        cpu: "32"
    volumeMounts:
    - name: model-storage
      mountPath: /models
    - name: dshm
      mountPath: /dev/shm
    command:
    - python3
    - -m
    - sglang.launch_server
    readinessProbe:
      httpPath: /health
      port: 8000
      initialDelaySeconds: 30
      periodSeconds: 10
      timeoutSeconds: 5
      failureThreshold: 10
    livenessProbe:
      httpPath: /health
      port: 8000
      initialDelaySeconds: 480
      periodSeconds: 30
      timeoutSeconds: 5
      failureThreshold: 3
```

### 使用配置模板创建推理服务（REST API）

```bash
curl -X POST http://localhost:18010/api/v1/pd-inference-services \
  -H "Content-Type: application/json" \
  -d @- << 'EOF'
{
  "apiVersion": "pdai.pdai.io/v1alpha1",
  "kind": "PDInferenceService",
  "metadata": {"name": "qwen3-14b", "namespace": "default"},
  "spec": {
    "model": "Qwen/Qwen3-14B",
    "engineProfileRef": "sglang-a30-qwen3-14b",
    "volumes": [
      {"name": "model-storage", "hostPath": {"path": "/data/model/qwen3-14b", "type": "Directory"}},
      {"name": "dshm", "emptyDir": {"medium": "Memory", "sizeLimit": "20Gi"}}
    ],
    "router": {
      "replicas": 1,
      "resources": {"requests": {"memory": "4Gi", "cpu": "4"}, "limits": {"memory": "4Gi", "cpu": "4"}},
      "volumeMounts": [{"name": "model-storage", "mountPath": "/models"}],
      "readinessProbe": {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 30,
                         "periodSeconds": 10, "timeoutSeconds": 5, "failureThreshold": 3},
      "livenessProbe":  {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 120,
                         "periodSeconds": 30, "timeoutSeconds": 5, "failureThreshold": 3}
    },
    "prefill": {
      "replicas": 1,
      "gpu": "2",
      "gpuType": "a30",
      "resources": {"requests": {"memory": "96Gi", "cpu": "16"}, "limits": {"memory": "128Gi", "cpu": "32"}},
      "volumeMounts": [{"name": "model-storage", "mountPath": "/models"}, {"name": "dshm", "mountPath": "/dev/shm"}],
      "command": ["python3", "-m", "sglang.launch_server"],
      "readinessProbe": {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 30,
                         "periodSeconds": 10, "timeoutSeconds": 5, "failureThreshold": 10},
      "livenessProbe":  {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 300,
                         "periodSeconds": 30, "timeoutSeconds": 5, "failureThreshold": 3}
    },
    "decode": {
      "replicas": 1,
      "gpu": "2",
      "gpuType": "a30",
      "resources": {"requests": {"memory": "96Gi", "cpu": "16"}, "limits": {"memory": "128Gi", "cpu": "32"}},
      "volumeMounts": [{"name": "model-storage", "mountPath": "/models"}, {"name": "dshm", "mountPath": "/dev/shm"}],
      "command": ["python3", "-m", "sglang.launch_server"],
      "readinessProbe": {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 30,
                         "periodSeconds": 10, "timeoutSeconds": 5, "failureThreshold": 10},
      "livenessProbe":  {"httpPath": "/health", "port": 8000, "initialDelaySeconds": 480,
                         "periodSeconds": 30, "timeoutSeconds": 5, "failureThreshold": 3}
    }
  }
}
EOF
```

与直接内联配置相比，引用配置模板后 PDIS 的 JSON body 减少了约 60% 的内容（无需填写 image、args、engineRuntimes）。
