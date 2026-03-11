# pd-manager 部署指南

本文档面向零基础用户，完整介绍从代码到业务验证的全链路部署流程。

---

## 目录

1. [项目结构介绍](#1-项目结构介绍)
2. [前置条件](#2-前置条件)
3. [依赖组件部署：RBG Operator](#3-依赖组件部署rbg-operator)
4. [构建 pd-manager 镜像](#4-构建-pd-manager-镜像)
5. [镜像导入（离线 / containerd 集群）](#5-镜像导入离线--containerd-集群)
6. [安装 pd-manager CRD](#6-安装-pd-manager-crd)
7. [部署 pd-manager Operator](#7-部署-pd-manager-operator)
8. [发布推理服务（PDInferenceService）](#8-发布推理服务pdinferenceservice)
9. [访问 REST API](#9-访问-rest-api)
10. [业务功能验证](#10-业务功能验证)
11. [卸载](#11-卸载)

---

## 1. 项目结构介绍

### 1.1 目录结构

```
pd-manager/
├── api/v1alpha1/                  # CRD 类型定义（Go struct）
│   ├── pdinferenceservice_types.go    # PDInferenceService CRD 核心字段
│   └── pdengineprofile_types.go       # PDEngineProfile CRD（引擎配置模板）
├── cmd/main.go                    # Operator 主入口
├── config/
│   ├── crd/bases/                 # 生成的 CRD YAML 文件
│   ├── default/                   # kustomize 默认部署套件
│   ├── manager/                   # Deployment + Service 定义
│   ├── rbac/                      # RBAC 权限定义
│   └── webhook/                   # Webhook 配置
├── examples/
│   └── qwen3-14b.yaml             # 完整可用的 Qwen3-14B 推理服务样例
├── internal/
│   ├── apiserver/                 # REST API 服务（HTTP 代理 k8s API）
│   ├── config/                    # 引擎配置合并逻辑（3 层优先级）
│   ├── controller/                # Reconciler 主控制逻辑
│   ├── translator/                # PDInferenceService → RBG 转换
│   └── webhook/                   # Admission Webhook（校验 + 默认值注入）
├── docs/                          # 设计文档、API 参考、测试指南
├── Dockerfile                     # 两阶段构建，vendor 模式，distroless 基础镜像
└── Makefile                       # 常用操作快捷命令
```

### 1.2 核心 CRD 概念

pd-manager 提供两个自定义资源：

| CRD | 简称 | 说明 |
|-----|------|------|
| `PDInferenceService` | `pdis` | 用户面向的推理服务声明，包含 router/prefill/decode 三个角色的完整配置 |
| `PDEngineProfile` | `pdep` | 引擎配置模板，可被多个 `PDInferenceService` 引用，实现参数复用 |

**PDInferenceService 三角色架构：**

| 角色 | 说明 |
|------|------|
| `router` | 请求路由（sgl-model-gateway），负责将推理请求分发给 decode 节点 |
| `prefill` | 预填充节点，执行 prompt forward pass，通过 RDMA 将 KV Cache 传输给 decode |
| `decode` | 解码节点，接收 KV Cache 后执行 token 生成 |

### 1.3 四层架构

```
用户（kubectl / REST API）
        │
        ▼
pd-manager Operator（本项目）
  - 将 PDInferenceService → RoleBasedGroup（RBG）
  - 聚合状态，管理 HPA，维护 pdRatio
        │
        ▼
RBG Operator（底层依赖）
  - 将 RoleBasedGroup → StatefulSet + 服务发现
        │
        ▼
SGLang 推理引擎（router / prefill / decode Pods）
```

---

## 2. 前置条件

### 2.1 工具链

| 工具 | 版本要求 | 用途 |
|------|---------|------|
| Go | >= 1.24 | 编译 pd-manager（如需从源码构建） |
| Docker | >= 20.10 | 构建镜像 |
| kubectl | >= 1.28 | 操作 k8s 集群 |
| kustomize | >= 5.0 | 渲染部署 YAML（可选，见第 7 章） |

### 2.2 Kubernetes 集群

- 版本 >= 1.28
- 已安装 GPU 设备插件（如 NVIDIA Device Plugin）
- GPU 节点打好标签（pd-manager 通过 `gpuType` 字段调度 GPU 节点）：

```bash
# 示例：将节点 gpu-node-1 标记为 A30 GPU
kubectl label node gpu-node-1 accelerator=a30

# 验证
kubectl get nodes -l accelerator=a30
```

### 2.3 模型文件

将模型文件准备到 GPU 节点上的固定路径（后续在 YAML 中用 `hostPath` 挂载），
或挂载 PVC（推荐生产环境）。

---

## 3. 依赖组件部署：RBG Operator

**pd-manager 必须依赖 RBG Operator 才能正常工作**，请先安装 RBG。

RBG (RoleBasedGroup) 源码仓库：`https://github.com/sgl-project/rbg`

### 方式一：kubectl apply（推荐，最简单）

```bash
# 克隆或下载 RBG 仓库
git clone https://github.com/sgl-project/rbg.git
cd rbg

# 安装（--server-side 避免 annotation 过大）
kubectl apply --server-side -f deploy/kubectl/manifests.yaml

# 等待控制器就绪（约 1-2 分钟）
kubectl wait deploy/rbgs-controller-manager \
  -n rbgs-system \
  --for=condition=available \
  --timeout=5m

# 验证
kubectl get pods -n rbgs-system
```

预期输出（所有 Pod 为 Running）：
```
NAME                                       READY   STATUS    RESTARTS   AGE
rbgs-controller-manager-xxxxxxxxx-xxxxx    2/2     Running   0          2m
```

### 方式二：Helm（生产推荐）

```bash
cd rbg

helm upgrade --install rbgs deploy/helm/rbgs \
  --create-namespace \
  --namespace rbgs-system \
  --wait

# 验证
kubectl get pods -n rbgs-system
```

### 验证 RBG CRD 安装成功

```bash
kubectl get crd | grep workloads.x-k8s.io
```

应看到以下 CRD：
```
rolebasedgroups.workloads.x-k8s.io
rolebasedgroupscalingadapters.workloads.x-k8s.io
clusterengineruntimeprofiles.workloads.x-k8s.io
...
```

---

## 4. 构建 pd-manager 镜像

在 pd-manager 项目根目录执行以下命令。

### 方式一：使用 make（需要本地 Go 环境）

```bash
cd pd-manager

# <registry> 替换为你的镜像仓库地址，如 my-registry.example.com/pd-manager
make docker-build IMG=<registry>/pd-manager:v0.1.0
```

### 方式二：直接 docker build（离线，使用 vendor 目录）

项目已包含完整的 `vendor/` 目录，构建无需访问网络：

```bash
cd pd-manager

docker build \
  --no-cache \
  --provenance=false \
  --platform linux/amd64 \
  -t <registry>/pd-manager:v0.1.0 \
  .
```

> **注意**：如果目标集群是 amd64 架构，请务必加 `--platform linux/amd64`，
> 避免在 ARM 机器（如 MacBook M 系列）上构建出 arm64 镜像导致无法运行。

### 推送镜像到仓库（联网环境）

```bash
docker push <registry>/pd-manager:v0.1.0
```

---

## 5. 镜像导入（离线 / containerd 集群）

若集群节点无法访问镜像仓库（离线环境），需手动将镜像导入每个节点。

### 在构建机上导出

```bash
docker save <registry>/pd-manager:v0.1.0 | gzip > pd-manager-v0.1.0.tar.gz
```

### 在每个 k8s 节点上导入

**containerd 环境（k8s 1.24+ 默认）：**

```bash
# 拷贝到节点后执行
sudo ctr -n k8s.io images import pd-manager-v0.1.0.tar.gz

# 验证
sudo ctr -n k8s.io images ls | grep pd-manager
```

**Docker 环境（较旧集群）：**

```bash
docker load < pd-manager-v0.1.0.tar.gz
docker images | grep pd-manager
```

> **提示**：如果使用 imagePullPolicy: IfNotPresent（默认值），导入镜像后节点上的 Pod 会直接使用本地镜像，无需重新拉取。

---

## 6. 安装 pd-manager CRD

```bash
cd pd-manager

# 安装两个 CRD：PDInferenceService 和 PDEngineProfile
kubectl apply -f config/crd/bases/

# 验证
kubectl get crd | grep pdai
```

预期输出：
```
pdengineprofiles.pdai.pdai.io    2024-xx-xx
pdinferenceservices.pdai.pdai.io 2024-xx-xx
```

---

## 7. 部署 pd-manager Operator

### 方式一：make deploy（推荐，需要 kustomize）

```bash
cd pd-manager

make deploy IMG=<registry>/pd-manager:v0.1.0
```

此命令会：
1. 自动下载 kustomize 到 `bin/`
2. 更新 `config/manager/kustomization.yaml` 中的镜像地址
3. 渲染并 apply 所有资源（Namespace、RBAC、Deployment、Service、Webhook 等）

### 方式二：kubectl apply（只需 kubectl，无需本地 kustomize）

先在有 make/kustomize 的机器上生成安装包，再拷贝到目标环境：

```bash
# 在有 make 的机器上生成 dist/install.yaml
make build-installer IMG=<registry>/pd-manager:v0.1.0

# 拷贝 dist/install.yaml 到目标环境后执行
kubectl apply -f dist/install.yaml
```

### 方式三：手工 kustomize build（已安装 kustomize 但无 make）

```bash
cd pd-manager/config/manager
kustomize edit set image controller=<registry>/pd-manager:v0.1.0

cd ../../
kustomize build config/default | kubectl apply -f -
```

### 验证部署成功

```bash
kubectl get pods -n pd-manager-system
```

预期输出（Pod 为 Running，READY 为 1/1 或 2/2）：
```
NAME                                              READY   STATUS    RESTARTS   AGE
pd-manager-controller-manager-xxxxxxxxx-xxxxx     1/1     Running   0          1m
```

查看日志：
```bash
kubectl logs -n pd-manager-system \
  deployment/pd-manager-controller-manager \
  -f
```

---

## 8. 发布推理服务（PDInferenceService）

### 8.1 准备服务配置文件

以下为最小化配置示例（适配有 A30 GPU、本地模型文件的环境）：

```yaml
# my-inference-service.yaml
apiVersion: pdai.pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: my-llm-service
  namespace: default
spec:
  model: Qwen/Qwen3-14B          # 模型标识符（用于标签和 API 路由）

  volumes:
  - name: model-storage
    hostPath:
      path: /data/model/qwen3-14b  # 节点上的模型路径，按实际情况修改
      type: Directory
  - name: dshm
    emptyDir:
      medium: Memory
      sizeLimit: 20Gi

  router:
    image: lmsysorg/sgl-model-gateway:v0.3.1
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
    args:
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
    image: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    replicas: 1
    gpu: "2"                      # 每个 Pod 占用 2 块 GPU
    gpuType: a30                  # 调度到打有 accelerator=a30 标签的节点
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
    args:
    - --model-path
    - /models
    - --trust-remote-code
    - --tp-size
    - "2"
    - --host
    - $(POD_IP)                   # pd-manager 自动注入 POD_IP 环境变量
    - --port
    - "8000"
    - --disaggregation-mode
    - prefill
    - --disaggregation-transfer-backend
    - nixl
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
    image: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
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
    args:
    - --model-path
    - /models
    - --trust-remote-code
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

> 完整样例（含 Patio sidecar）见 [examples/qwen3-14b.yaml](../examples/qwen3-14b.yaml)

### 8.2 方式一：kubectl 部署

```bash
kubectl apply -f my-inference-service.yaml

# 查看部署状态
kubectl get pdis                   # pdis 是 PDInferenceService 的简称
kubectl describe pdis my-llm-service
```

监控 Pod 启动（prefill/decode Pod 拉模型时间较长，耐心等待）：
```bash
kubectl get pods -w
```

### 8.3 方式二：REST API 部署

先确保已按第 9 章建立 port-forward，然后：

```bash
curl -X POST http://localhost:18010/api/v1/pd-inference-services \
  -H "Content-Type: application/json" \
  -d @my-inference-service.yaml    # YAML 文件需先转为 JSON

# 或直接用 JSON：
curl -X POST http://localhost:18010/api/v1/pd-inference-services \
  -H "Content-Type: application/json" \
  -d '{
    "apiVersion": "pdai.pdai.io/v1alpha1",
    "kind": "PDInferenceService",
    "metadata": {"name": "my-llm-service", "namespace": "default"},
    "spec": { ... }
  }'
```

### 8.4 查看服务状态

```bash
# kubectl 方式
kubectl get pdis my-llm-service -o wide
kubectl get pdis my-llm-service -o jsonpath='{.status.phase}'

# REST API 方式
curl http://localhost:18010/api/v1/pd-inference-services/my-llm-service
```

`status.phase` 字段含义：

| Phase | 说明 |
|-------|------|
| `Pending` | 资源刚创建，等待调度 |
| `Initializing` | Pod 已调度，正在拉镜像/启动引擎 |
| `Running` | 所有角色就绪，可接收推理请求 |
| `Failed` | 有 Pod 异常退出 |
| `Terminating` | 正在删除中 |

等待 `Running` 状态（首次启动因需加载模型，可能需要 5-15 分钟）：

```bash
kubectl wait pdis my-llm-service --for=jsonpath='{.status.phase}'=Running --timeout=30m
```

---

## 9. 访问 REST API

pd-manager REST API 监听在 Pod 内部的 8080 端口。在部分 CNI 环境下 hostPort 不生效，推荐通过 `kubectl port-forward` 访问。

### 建立 port-forward

```bash
# 获取 manager Pod 名称
MANAGER_POD=$(kubectl get pods -n pd-manager-system \
  -l control-plane=controller-manager \
  -o jsonpath='{.items[0].metadata.name}')

echo "Manager Pod: $MANAGER_POD"

# 建立端口转发（本机 18010 → Pod 8080）
kubectl port-forward -n pd-manager-system pod/${MANAGER_POD} \
  --address=0.0.0.0 18010:8080
```

> `--address=0.0.0.0` 允许其他机器访问。如果只在本机使用，可省略该参数。

保持此终端运行，在另一个终端执行后续 API 调用。

### REST API 端点一览

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/pd-inference-services` | 列出所有服务 |
| `POST` | `/api/v1/pd-inference-services` | 创建服务 |
| `GET` | `/api/v1/pd-inference-services/{name}` | 查询单个服务状态 |
| `PUT` | `/api/v1/pd-inference-services/{name}` | 更新（扩缩容） |
| `DELETE` | `/api/v1/pd-inference-services/{name}` | 删除服务 |

> **注意**：REST API 固定操作 `default` namespace。

---

## 10. 业务功能验证

以下命令假设已建立 port-forward（见第 9 章），BASE_URL 为 REST API 地址。

```bash
BASE_URL=http://localhost:18010
SERVICE_NAME=my-llm-service
```

### 10.1 查询服务列表

```bash
curl -s ${BASE_URL}/api/v1/pd-inference-services | python3 -m json.tool
```

### 10.2 查询单个服务状态

```bash
curl -s ${BASE_URL}/api/v1/pd-inference-services/${SERVICE_NAME} | python3 -m json.tool

# 只看 phase 和 endpoint
curl -s ${BASE_URL}/api/v1/pd-inference-services/${SERVICE_NAME} | \
  python3 -c "import sys,json; d=json.load(sys.stdin); print('phase:', d['status'].get('phase'), '| endpoint:', d['status'].get('endpoint'))"
```

### 10.3 扩缩容（调整副本数）

```bash
# 将 decode 扩容到 2 个副本
curl -X PUT ${BASE_URL}/api/v1/pd-inference-services/${SERVICE_NAME} \
  -H "Content-Type: application/json" \
  -d '{"spec": {"decode": {"replicas": 2}}}'

# 将 prefill 扩容到 2 个副本
curl -X PUT ${BASE_URL}/api/v1/pd-inference-services/${SERVICE_NAME} \
  -H "Content-Type: application/json" \
  -d '{"spec": {"prefill": {"replicas": 2}}}'

# 同时调整两者
curl -X PUT ${BASE_URL}/api/v1/pd-inference-services/${SERVICE_NAME} \
  -H "Content-Type: application/json" \
  -d '{"spec": {"prefill": {"replicas": 2}, "decode": {"replicas": 2}}}'
```

用 kubectl 验证副本数变化：
```bash
kubectl get pods -w
```

### 10.4 推理请求验证

首先获取 router Pod 的地址：

```bash
# 方法1：通过 port-forward 访问 router（推荐测试用）
ROUTER_POD=$(kubectl get pods -l rolebasedgroup.workloads.x-k8s.io/role=router \
  -o jsonpath='{.items[0].metadata.name}')

kubectl port-forward pod/${ROUTER_POD} --address=0.0.0.0 18001:8000 &

ROUTER_URL=http://localhost:18001
```

发送推理请求（OpenAI 兼容接口）：

```bash
# Chat Completions API
curl -s ${ROUTER_URL}/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "/models",
    "messages": [
      {"role": "user", "content": "你好，请介绍一下自己"}
    ],
    "max_tokens": 128,
    "temperature": 0.7
  }' | python3 -m json.tool
```

预期返回类似（choices 中有 message.content）：
```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "你好！我是一个大型语言模型..."
      },
      "finish_reason": "stop"
    }
  ],
  "usage": { "prompt_tokens": 10, "completion_tokens": 50 }
}
```

流式输出验证：
```bash
curl -s ${ROUTER_URL}/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "/models",
    "messages": [{"role": "user", "content": "写一首短诗"}],
    "max_tokens": 100,
    "stream": true
  }'
```

### 10.5 删除服务

```bash
# kubectl 方式
kubectl delete pdis ${SERVICE_NAME}

# REST API 方式
curl -X DELETE ${BASE_URL}/api/v1/pd-inference-services/${SERVICE_NAME}

# 等待资源清理完成
kubectl get pdis
kubectl get pods
```

---

## 11. 卸载

### 删除推理服务

```bash
kubectl delete pdis --all -n default
```

### 卸载 pd-manager Operator

```bash
cd pd-manager

# kustomize 方式
kustomize build config/default | kubectl delete -f -

# 或使用 make
make undeploy
```

### 卸载 pd-manager CRD

```bash
kubectl delete -f config/crd/bases/
```

> **警告**：删除 CRD 会同时删除所有 PDInferenceService 和 PDEngineProfile 资源。

### 卸载 RBG Operator

```bash
# kubectl 方式
kubectl delete -f deploy/kubectl/manifests.yaml

# Helm 方式
helm uninstall rbgs --namespace rbgs-system
```

---

## 附录：常见问题

### Pod 一直处于 Pending 状态

检查节点是否有足够 GPU 资源和正确的标签：
```bash
kubectl describe pod <pod-name>         # 查看 Events 中的调度失败原因
kubectl get nodes -l accelerator=a30   # 确认节点标签存在
kubectl describe node <gpu-node>        # 查看节点资源是否充足
```

### PDInferenceService 状态长时间停留在 Initializing

查看 controller 日志和 RBG 状态：
```bash
kubectl logs -n pd-manager-system deployment/pd-manager-controller-manager
kubectl get rolebasedgroup -n default
kubectl describe rolebasedgroup <rbg-name>
```

### router Pod 就绪但推理请求超时

确认 prefill/decode Pod 均已就绪（router 需等待 worker 注册后才真正可用）：
```bash
kubectl get pods -l app.kubernetes.io/name=pd-manager
# 查看 router 日志确认 worker 是否已注册
kubectl logs <router-pod> | grep -i "worker\|register"
```

### REST API 返回 404

确认 port-forward 正常运行，且请求的服务名称在 `default` namespace 中存在：
```bash
kubectl get pdis -n default
```
