# pd-manager 手动验收测试手册

## 环境信息

| 项目 | 值 |
|------|-----|
| 开发环境 | 本地 WSL (`/home/zzy/code/pd-manager`) |
| 测试环境 | a30（`ssh a30@183.56.181.9 -p 34451`，免密登录）|
| RBG Operator | 已预装于 a30 |
| GPU 节点 | 已标记 `accelerator=a30` |

## 前置条件

1. a30 环境已安装 RBG Operator（提供 `RoleBasedGroup` CRD）
2. `kubectl` 已配置指向 a30 集群
3. 镜像已推送到 a30 可访问的 Registry
4. pd-system namespace 已创建：`kubectl create namespace pd-system`

## 部署步骤

### 1. WSL 构建并推送镜像

```bash
# 在 WSL 中
cd /home/zzy/code/pd-manager
make docker-build docker-push IMG=<registry>/pd-manager:latest
```

### 2. 在 a30 上部署 CRD + RBAC + Manager

```bash
# 在 a30 上（或 WSL 通过 KUBECONFIG 指向 a30）
kubectl apply -k config/default

# 验证 Manager Pod 就绪
kubectl rollout status deploy/pd-manager-controller-manager -n pd-system
```

---

## 验收用例

### US-01：创建推理服务

**目标**：创建一个完整的 PD 推理服务，三个角色（scheduler/prefill/decode）Pod 就绪。

```bash
# 1. 创建 PDInferenceService
cat <<EOF | kubectl apply -f -
apiVersion: pdai.pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: qwen3-14b
  namespace: default
spec:
  model: qwen3-14b
  modelStorage:
    type: hostPath
    hostPath: /data/models
    mountPath: /models
  images:
    scheduler: lmsysorg/sgl-model-gateway:v0.3.1
    prefill: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    decode: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
  prefill:
    replicas: 1
    resources:
      gpu: "1"
      gpuType: a30
  decode:
    replicas: 1
    resources:
      gpu: "1"
      gpuType: a30
EOF

# 2. 观察状态变化（期望：Pending → Initializing）
kubectl get pdis qwen3-14b -w

# 3. 等待就绪（期望：Running）
kubectl wait pdis qwen3-14b --for=jsonpath='.status.phase'=Running --timeout=10m
```

**预期结果**：
- PDInferenceService 创建成功，Phase 在 30s 内变为 Initializing
- RBG 被自动创建：`kubectl get rbg qwen3-14b`
- GPU Pod 启动后 Phase 变为 Running
- 三个角色的 Pod 均处于 Running 状态

---

### US-02：查询服务状态

**目标**：通过 kubectl 和 REST API 查询服务详细状态。

```bash
# 方式一：kubectl
kubectl get pdis qwen3-14b -o yaml

# 期望输出中包含：
# status:
#   phase: Running
#   endpoint: <scheduler-service-ip>:8000
#   roleStatuses:
#   - name: scheduler
#     ready: 1
#     total: 1
#   - name: prefill
#     ready: 1
#     total: 1
#   - name: decode
#     ready: 1
#     total: 1

# 方式二：REST API（通过 pd-manager Service）
PD_MANAGER_SVC=$(kubectl get svc -n pd-system -l app.kubernetes.io/name=pd-manager -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}')
curl http://${PD_MANAGER_SVC}:8080/api/v1/pd-inference-services/qwen3-14b

# 方式三：列表查询
curl http://${PD_MANAGER_SVC}:8080/api/v1/pd-inference-services
```

**预期结果**：
- `status.phase = Running`
- `status.endpoint` 有值（格式：`<IP>:8000`）
- `roleStatuses` 包含 scheduler/prefill/decode，每个 ready=total

---

### US-03：手动扩容

**目标**：通过 REST API 将 decode 副本数从 1 扩容到 2。

```bash
# REST API 方式（推荐）
curl -X PUT http://${PD_MANAGER_SVC}:8080/api/v1/pd-inference-services/qwen3-14b \
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
curl -X PUT http://${PD_MANAGER_SVC}:8080/api/v1/pd-inference-services/qwen3-14b \
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
- 所有 scheduler/prefill/decode Pod 消失
- 不留残余资源

---

## 异常场景验证

### 场景 1：不存在的 Profile 引用

```bash
# 创建引用不存在 Profile 的 PDIS
cat <<EOF | kubectl apply -f -
apiVersion: pdai.pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: test-bad-profile
  namespace: default
spec:
  model: qwen3-14b
  engineProfileRef: nonexistent-profile
  modelStorage:
    type: hostPath
    hostPath: /data/models
  prefill:
    replicas: 1
    resources:
      gpu: "1"
  decode:
    replicas: 1
    resources:
      gpu: "1"
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
kubectl delete pdis --all -n pd-system

# 验证清理完成
kubectl get pdis,rbg --all-namespaces
```

---

## 验收标准检查清单

| 用例 | 验收标准 | 通过 |
|------|---------|------|
| US-01 | PDInferenceService 创建后 RBG 自动创建，Phase 变为 Running | ☐ |
| US-01 | 三个角色（scheduler/prefill/decode）Pod 均 Running | ☐ |
| US-02 | kubectl 查询 phase=Running，endpoint 有值 | ☐ |
| US-02 | REST API GET 返回 200，包含完整 status | ☐ |
| US-03 | REST API PUT 修改 replicas 成功，RBG 同步更新 | ☐ |
| US-03 | 修改 model 被拒绝（400），错误含 "immutable" | ☐ |
| US-04 | 删除后 RBG 级联删除，所有 Pod 消失 | ☐ |
| 异常 | 无效 Profile 引用 → Status.Phase = Failed | ☐ |
| 异常 | 不可变字段修改被 Webhook 拒绝 | ☐ |
