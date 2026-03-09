# pd-manager 手动验收测试手册

## 环境信息

| 项目 | 值 |
|------|-----|
| 开发环境 | 本地 WSL (`/home/zzy/code/pd-manager`) |
| 测试环境 | a30（`ssh a30@183.56.181.9 -p 34451`，免密登录）|
| a30 代码路径 | `/home/a30/rbg-deployment/pd-manager` |
| RBG Operator | 已预装于 a30 |
| GPU 节点 | 已标记 `accelerator=a30` |
| 模型路径 | `/data/model/qwen3-14b`（节点本地） |
| pd-manager Namespace | `pd-manager-system` |

## 前置条件

1. a30 环境已安装 RBG Operator（提供 `RoleBasedGroup` CRD）
2. a30 上 `kubectl` 已配置指向本地集群
3. pd-manager 已部署（见下方部署步骤；若已部署可直接跳到验收用例）
4. `pd-manager-system` namespace 已创建

## 部署步骤

> **注意**：a30 无公网访问，使用 vendor 模式构建，通过 containerd 直接导入镜像（不走 Registry）。

### 1. 获取代码到 a30

**方式 A（推荐）：直接从 GitHub 克隆**

```bash
# 在 a30 上
mkdir -p /home/a30/rbg-deployment
cd /home/a30/rbg-deployment

# 克隆 pd-manager（含 vendor 目录，无需额外下载依赖）
git clone https://github.com/jackyzzy/pd-manager.git pd-manager

# 克隆 rbg 本地模块（go.mod replace 依赖）
# 将 <rbg-repo-url> 替换为实际地址
git clone <rbg-repo-url> rbg

# 后续更新时在各目录执行 git pull 即可
```

> **注意**：若 vendor/ 已提交到 git，克隆后无需再运行 `go mod vendor`。

**方式 B（备选）：从 WSL rsync**

```bash
# 在 WSL 中执行（a30 无法访问 proxy.golang.org 时，需同步 vendor 目录）
rsync -avz --exclude='.git' --exclude='bin/' \
  /home/zzy/code/pd-manager/ \
  a30@183.56.181.9:/home/a30/rbg-deployment/pd-manager/ \
  -e "ssh -p 34451"

# 同步 rbg 本地模块
rsync -avz --exclude='.git' \
  /home/zzy/code/rbg/ \
  a30@183.56.181.9:/home/a30/rbg-deployment/rbg/ \
  -e "ssh -p 34451"
```

### 2. 在 a30 上构建镜像并导入 containerd

```bash
# 在 a30 上
cd /home/a30/rbg-deployment/pd-manager

# ── 若使用方式 B（rsync）且有 vendor 目录，需修复本地路径 ──────────────────────
# sed -i 's|=> /home/zzy/code/rbg|=> ../rbg|g' vendor/modules.txt
# ── 若使用方式 A（git clone）且 Dockerfile 走联网模式，跳过此步 ──────────────

# 构建镜像（--network=host 绕过 iptables 问题）
# 若 a30 无法访问 proxy.golang.org，需在 Dockerfile 中切换为 vendor 模式（见 Dockerfile 注释）
docker build --network=host -t pd-manager:v0.0.1 .

# 导入到 containerd（k8s.io 命名空间）
docker save pd-manager:v0.0.1 | sudo ctr -n k8s.io images import -
```

### 3. 在 a30 上部署 CRD + RBAC + Manager

```bash
# 在 a30 上
cd /home/a30/rbg-deployment/pd-manager

# 安装 CRD（包括 RBG CRD）
make install

# 部署 Operator（使用本地镜像，imagePullPolicy: Never）
make deploy IMG=pd-manager:v0.0.1

# 验证 Manager Pod 就绪
kubectl rollout status deploy/pd-manager-controller-manager -n pd-manager-system --timeout=60s
```

> **Webhook TLS**：a30 无 cert-manager，需手动配置自签证书。若 Pod 因 TLS 报错无法启动，参考以下命令生成并挂载：
> ```bash
> openssl req -x509 -newkey rsa:4096 -keyout tls.key -out tls.crt -days 365 -nodes -subj "/CN=pd-manager-webhook-service.pd-manager-system.svc"
> kubectl create secret tls webhook-server-cert --cert=tls.crt --key=tls.key -n pd-manager-system
> ```

---

## 验收用例

### US-01：创建推理服务

**目标**：创建一个完整的 PD 推理服务，三个角色（router/prefill/decode）Pod 就绪。支持两种创建方式。

---

#### 方式 A：kubectl apply（运维人员）

```bash
# 前置条件：确认 ClusterEngineRuntimeProfile sglang-pd-runtime 存在
kubectl get clusterengineruntimeprofile sglang-pd-runtime

# 1. 创建 PDInferenceService（含 patio sidecar，command/args 分离）
cat <<'EOF' | kubectl apply -f -
apiVersion: pdai.pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: qwen3-14b
  namespace: default
spec:
  model: Qwen/Qwen3-14B

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
    - --prometheus-host
    - 0.0.0.0
    - --prometheus-port
    - "9090"
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
    engineRuntimes:
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
    engineRuntimes:
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
EOF

# 2. 观察状态变化（期望：Pending → Initializing）
kubectl get pdis qwen3-14b -w

# 3. 等待就绪（期望：Running，Qwen3-14B GPU 加载约 15~25 分钟）
kubectl wait pdis qwen3-14b --for=jsonpath='.status.phase'=Running --timeout=30m
```

---

#### 方式 B：REST API POST（前端页面 / 程序调用）

> 前置：先 port-forward pd-manager 到本地（见 US-02）。

```bash
# port-forward pd-manager API 服务
PD_POD=$(kubectl get pod -n pd-manager-system -l control-plane=controller-manager -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward -n pd-manager-system pod/${PD_POD} 8080:8080 &
sleep 2

# POST 创建推理服务（JSON 格式）
curl -s -X POST http://localhost:8080/api/v1/pd-inference-services \
  -H 'Content-Type: application/json' \
  -d '{
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
        "args": ["--pd-disaggregation","--host","0.0.0.0","--port","8000","--model-path","/models","--policy","round-robin"],
        "volumeMounts": [{"name": "model-storage", "mountPath": "/models"}]
      },
      "prefill": {
        "image": "lmsysorg/sglang:v0.5.8-cu130-amd64-runtime",
        "replicas": 1,
        "gpu": "2",
        "gpuType": "a30",
        "args": ["--model-path","/models","--trust-remote-code","--tp-size","2","--host","$(POD_IP)","--port","8000","--disaggregation-mode","prefill","--disaggregation-transfer-backend","nixl","--mem-fraction-static","0.88"],
        "volumeMounts": [{"name":"model-storage","mountPath":"/models"},{"name":"dshm","mountPath":"/dev/shm"}]
      },
      "decode": {
        "image": "lmsysorg/sglang:v0.5.8-cu130-amd64-runtime",
        "replicas": 1,
        "gpu": "2",
        "gpuType": "a30",
        "args": ["--model-path","/models","--trust-remote-code","--tp-size","2","--host","$(POD_IP)","--port","8000","--disaggregation-mode","decode","--disaggregation-transfer-backend","nixl","--mem-fraction-static","0.88"],
        "volumeMounts": [{"name":"model-storage","mountPath":"/models"},{"name":"dshm","mountPath":"/dev/shm"}]
      }
    }
  }' | python3 -m json.tool

# 期望：HTTP 201，响应体包含创建后的资源（含 metadata.name）

# 查询创建结果
curl -s http://localhost:8080/api/v1/pd-inference-services/qwen3-14b | python3 -m json.tool
```

---

**预期结果（两种方式相同）**：
- PDInferenceService 创建成功，Phase 在 30s 内变为 Initializing
- RBG 被自动创建：`kubectl get rbg qwen3-14b`
- 3 个 Pod（router×1、prefill×1、decode×1）启动后 Phase 变为 Running
- prefill/decode Pod 各包含 `patio-runtime` sidecar 容器
- router Pod 日志出现启动信息，patio sidecar 日志显示注册成功

---

### US-02：查询服务状态

**目标**：通过 kubectl 和 REST API 查询服务详细状态。

```bash
# 方式一：kubectl
kubectl get pdis qwen3-14b -o yaml

# 期望输出中包含：
# status:
#   phase: Running
#   endpoint: <router-service-ip>:8000
#   roleStatuses:
#   - name: router
#     ready: 1
#     total: 1
#   - name: prefill
#     ready: 1
#     total: 1
#   - name: decode
#     ready: 1
#     total: 1

# 方式二：REST API（通过 port-forward 访问 pd-manager，a30 无 LoadBalancer）
PD_POD=$(kubectl get pod -n pd-manager-system -l control-plane=controller-manager -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward -n pd-manager-system pod/${PD_POD} 8080:8080 &
sleep 2

curl http://localhost:8080/api/v1/pd-inference-services/qwen3-14b

# 方式三：列表查询
curl http://localhost:8080/api/v1/pd-inference-services
```

**预期结果**：
- `status.phase = Running`
- `status.endpoint` 有值（格式：`<IP>:8000`）
- `roleStatuses` 包含 router/prefill/decode，每个 ready=total

---

### US-03：手动扩容

**目标**：通过 REST API 将 decode 副本数从 1 扩容到 2。

```bash
# REST API 方式（推荐；先 port-forward 见 US-02）
curl -X PUT http://localhost:8080/api/v1/pd-inference-services/qwen3-14b \
  -H 'Content-Type: application/json' \
  -d '{"spec":{"decode":{"replicas":2}}}'

# 期望：200，响应体中 spec.decode.replicas=2

# 验证 RBG 已更新
kubectl get rbg qwen3-14b -o jsonpath='{.spec.roles[?(@.name=="decode")].replicas}'
# 期望输出：2

# 等待新 Pod 就绪
kubectl wait pdis qwen3-14b --for=jsonpath='.status.roleStatuses[?(@.name=="decode")].ready'=2 --timeout=5m
```

**不可变字段测试（期望被拒绝）**：
```bash
# 尝试修改 model（不可变字段）→ 应返回 400
curl -X PUT http://localhost:8080/api/v1/pd-inference-services/qwen3-14b \
  -H 'Content-Type: application/json' \
  -d '{"spec":{"model":"new-model"}}'
# 期望：400 Bad Request，错误信息含 "immutable"
```

---

### US-04：删除服务

**目标**：删除推理服务后，所有下层资源（RBG、Pod）被级联清理。

```bash
# 1. 删除 PDInferenceService
kubectl delete pdis qwen3-14b

# 2. 观察 RBG 级联删除
kubectl get rbg -w

# 3. 确认 Pod 已清理
kubectl get pods -l app.kubernetes.io/part-of=qwen3-14b
# 期望：No resources found.

# 4. 确认命名空间干净
kubectl get pdis,rbg -n default
# 期望：No resources found.
```

**预期结果**：
- PDInferenceService 删除后，Finalizer 被移除
- RBG 通过 ownerReference 级联删除
- 所有 router/prefill/decode Pod 消失
- 不留残余资源

---

### US-05：Patio 路由注册验证 + 推理接口验证

**目标**：确认 patio sidecar 成功将 prefill/decode 注册到 router，并通过推理接口验证端到端服务正常。

> 前置：US-01 已完成，所有 Pod Running。

#### 1. 确认 patio sidecar 注入

```bash
# 确认 prefill Pod 包含 patio-runtime 容器
kubectl get pod qwen3-14b-prefill-0 -o jsonpath='{.spec.containers[*].name}'
# 期望输出包含：prefill patio-runtime

kubectl get pod qwen3-14b-decode-0 -o jsonpath='{.spec.containers[*].name}'
# 期望输出包含：decode patio-runtime
```

#### 2. 查看 patio sidecar 日志

```bash
# 查看 prefill patio-runtime 日志（应显示注册到 router 的信息）
kubectl logs qwen3-14b-prefill-0 -c patio-runtime --tail=50

# 查看 decode patio-runtime 日志
kubectl logs qwen3-14b-decode-0 -c patio-runtime --tail=50

# 期望日志包含类似：registered / add worker / connected to router 等字样
```

#### 3. Port-forward router，验证 worker 注册

```bash
# port-forward router Pod
kubectl port-forward pod/qwen3-14b-router-0 8000:8000 &
sleep 3

# 查询 router 已注册的 worker（prefill + decode）
curl -s http://localhost:8000/get_server_info | python3 -m json.tool
# 期望：包含 prefill_server_urls 和 decode_server_urls，各有 1 个 worker URL
```

#### 4. 推理接口端到端验证

```bash
# 发送推理请求（需等待所有 Pod Ready）
curl -s http://localhost:8000/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "Qwen/Qwen3-14B",
    "messages": [{"role": "user", "content": "Hello, what is 1+1?"}],
    "max_tokens": 50,
    "temperature": 0
  }' | python3 -m json.tool

# 期望：返回 200，choices[0].message.content 包含有意义的回答

# 验证健康检查
curl -s http://localhost:8000/health
# 期望：{"status": "ok"} 或 200 OK
```

**预期结果**：
- prefill/decode Pod 各含 2 个容器（主容器 + patio-runtime sidecar）
- patio 日志显示向 router 注册成功
- `get_server_info` 返回的 prefill/decode URL 列表非空
- 推理请求返回正常回答

---

## 异常场景验证

### 场景 1：不存在的 Profile 引用

```bash
# 创建引用不存在 Profile 的 PDIS（仅设置 engineProfileRef，不设置内联镜像/参数）
cat <<EOF | kubectl apply -f -
apiVersion: pdai.pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: test-bad-profile
  namespace: default
spec:
  model: qwen3-14b
  engineProfileRef: nonexistent-profile
  prefill:
    replicas: 1
  decode:
    replicas: 1
EOF

# 期望：Status.Phase 变为 Failed，reason = ProfileResolveFailed
kubectl get pdis test-bad-profile -o jsonpath='{.status.phase}'
# 输出：Failed

kubectl delete pdis test-bad-profile
```

### 场景 2：修改不可变字段被 Webhook 拒绝

```bash
# 先创建一个合法的 PDIS
kubectl apply -f examples/qwen3-14b.yaml

# 尝试修改 model（Webhook 应拒绝）
kubectl patch pdis qwen3-14b --type=merge -p '{"spec":{"model":"different-model"}}'
# 期望：Error from server (Forbidden): model is immutable

kubectl delete pdis qwen3-14b
```

---

## 清理

```bash
# 清理所有测试资源
kubectl delete pdis --all -n default
kubectl delete pdis --all -n pd-manager-system

# 验证清理完成
kubectl get pdis,rbg --all-namespaces
```

---

## 验收标准检查清单

| 用例 | 验收标准 | 通过 |
|------|---------|------|
| US-01 | kubectl apply 创建 PDIS，RBG 自动创建，Phase 变为 Running | ☐ |
| US-01 | REST API POST 创建 PDIS，返回 201，资源正常创建 | ☐ |
| US-01 | 三个角色（router/prefill/decode）Pod 均 Running | ☐ |
| US-01 | prefill/decode Pod 各含 patio-runtime sidecar 容器 | ☐ |
| US-02 | kubectl 查询 phase=Running，endpoint 有值 | ☐ |
| US-02 | REST API GET 返回 200，包含完整 status | ☐ |
| US-03 | REST API PUT 修改 replicas 成功，RBG 同步更新 | ☐ |
| US-03 | 修改 model 被拒绝（400），错误含 "immutable" | ☐ |
| US-04 | 删除后 RBG 级联删除，所有 Pod 消失 | ☐ |
| US-05 | patio 日志显示注册成功 | ☐ |
| US-05 | router get_server_info 包含 prefill/decode worker URL | ☐ |
| US-05 | 推理接口返回正常回答 | ☐ |
| 异常 | 无效 Profile 引用 → Status.Phase = Failed | ☐ |
| 异常 | 不可变字段修改被 Webhook 拒绝 | ☐ |
