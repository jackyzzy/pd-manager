# pd-manager 运维 API 速查

本文档记录常用的 API 调用方式，用于操作 PDInferenceService 资源（无需 kubectl edit）。

## pd-manager REST API（推荐使用）

pd-manager 内置 REST API，无需 token、不依赖 K8s RBAC，是最简便的操作方式。

### 设置访问（port-forward）

```bash
# 一次性设置，建议放到后台
PD_POD=$(kubectl get pod -n pd-manager-system -l control-plane=controller-manager -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward -n pd-manager-system pod/${PD_POD} --address=0.0.0.0 18010:8080 &
```

### 列出所有服务

```bash
curl -s http://127.0.0.1:18010/api/v1/pd-inference-services | jq .
```

### 查询单个服务状态

```bash
curl -s http://127.0.0.1:18010/api/v1/pd-inference-services/qwen3-14b | jq .status
```

### 修改副本数（PUT）

```bash
# 修改 decode 副本数
curl -s -X PUT http://127.0.0.1:18010/api/v1/pd-inference-services/qwen3-14b \
  -H "Content-Type: application/json" \
  -d '{"spec": {"decode": {"replicas": 2}}}'

# 同时修改 prefill 和 decode 副本数
curl -s -X PUT http://127.0.0.1:18010/api/v1/pd-inference-services/qwen3-14b \
  -H "Content-Type: application/json" \
  -d '{"spec": {"prefill": {"replicas": 1}, "decode": {"replicas": 2}}}'
```

### 删除服务

```bash
curl -s -X DELETE http://127.0.0.1:18010/api/v1/pd-inference-services/qwen3-14b
```

---

## K8s 原生 API（备用）

## K8s 原生 API - 环境变量（每次新 shell 需重新设置）

```bash
APISERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
TOKEN=$(kubectl create token default)
NS=default
NAME=qwen3-14b
BASE="$APISERVER/apis/pdai.pdai.io/v1alpha1/namespaces/$NS/pdinferenceservices/$NAME"
```

> **注意**：API group 是 `pdai.pdai.io`（非 `pdai.io`），写错会导致 404。
> `kubectl create token default` 生成的 token 默认有效期 1 小时，过期后需重新生成。

## 前提：授予 default SA 权限（仅需执行一次）

```bash
kubectl create clusterrolebinding default-pdis-admin \
  --clusterrole=cluster-admin \
  --serviceaccount=default:default
```

---

## 常用操作

### 查看当前配置

```bash
curl -s -H "Authorization: Bearer $TOKEN" --insecure $BASE | jq .spec
```

### 修改 decode 副本数

```bash
curl -s -X PATCH $BASE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/merge-patch+json" \
  --insecure \
  -d '{"spec": {"decode": {"replicas": 2}}}'
```

### 修改 prefill 副本数

```bash
curl -s -X PATCH $BASE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/merge-patch+json" \
  --insecure \
  -d '{"spec": {"prefill": {"replicas": 1}}}'
```

### 同时修改多个字段

```bash
curl -s -X PATCH $BASE \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/merge-patch+json" \
  --insecure \
  -d '{"spec": {"decode": {"replicas": 2}, "prefill": {"replicas": 1}}}'
```

### 查看状态

```bash
curl -s -H "Authorization: Bearer $TOKEN" --insecure $BASE | jq .status
```

---

## kubectl 等效命令（更简便）

```bash
# 修改 decode 副本数
kubectl patch pdis qwen3-14b --type=merge -p '{"spec":{"decode":{"replicas":2}}}'

# 修改 prefill 副本数
kubectl patch pdis qwen3-14b --type=merge -p '{"spec":{"prefill":{"replicas":1}}}'

# 查看状态
kubectl get pdis qwen3-14b -o yaml
```

---

## 已知问题

| 错误 | 原因 | 解决 |
|------|------|------|
| `404 page not found` | API group 写成 `pdai.io` | 改为 `pdai.pdai.io` |
| `404 page not found` | 漏写 `-X PATCH`，curl 默认用 POST | 加上 `-X PATCH` |
| `403 Forbidden` | default SA 没有 RBAC 权限 | 执行上方授权命令 |
| token 失效 | `kubectl create token` 默认 1h 过期 | 重新执行 `TOKEN=$(kubectl create token default)` |
