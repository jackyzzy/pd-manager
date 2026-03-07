# pd-manager 自动化测试指南

## 测试分层

| 层次 | 类型 | 执行环境 | 耗时 |
|------|------|---------|------|
| L1 单元测试 | 纯函数，无 I/O | WSL | <10s |
| L2 集成测试 | envtest（内置 API Server） | WSL | 1~3min |
| L3 集群冒烟测试 | 真实 k8s（a30） | a30 | 5~10min |

---

## L1/L2：WSL 本地执行

### 环境准备

```bash
# 安装 envtest 二进制（Makefile 已包含此目标）
make envtest

# 设置 envtest 二进制路径（或由 suite_test.go 自动发现 bin/k8s/）
export KUBEBUILDER_ASSETS=$(make -s envtest)
```

### 执行命令

```bash
# 全量测试（L1 + L2，含 envtest）
go test ./... -v -timeout 5m

# 仅 L1 单元测试（跳过 envtest，速度最快）
go test ./api/... ./internal/config/... ./internal/translator/... ./internal/apiserver/... -v

# 仅 Controller 集成测试（需要 envtest）
go test ./internal/controller/... -v -timeout 2m

# 仅 Webhook 集成测试（需要 envtest）
go test ./internal/webhook/... -v -timeout 2m

# 仅 cmd 集成冒烟测试（需要 envtest）
go test ./cmd/... -v -timeout 2m

# 覆盖率报告
go test ./... -coverprofile=coverage.out -timeout 5m
go tool cover -html=coverage.out -o coverage.html
open coverage.html
```

### 测试覆盖范围

| 包 | 测试文件 | 类型 | 主要覆盖点 |
|----|---------|------|-----------|
| `api/v1alpha1` | `pdinferenceservice_types_test.go` | L1 | 类型字段、JSON 序列化、枚举常量 |
| `api/v1alpha1` | `pdengineprofile_types_test.go` | L1 | Profile 类型、EngineRuntimes 序列化 |
| `internal/config` | `merger_test.go` | L1 | 三级合并逻辑（7 个用例） |
| `internal/translator/sglang` | `args_builder_test.go` | L1 | 各角色参数、auto-inject、backend（7 个用例）|
| `internal/translator` | `rbg_builder_test.go` | L1 | 三角色 RBG、Volume、ownerRef（8 个用例）|
| `internal/controller` | `status_test.go` | L1 | Phase 计算、状态机、conditions（9 个用例）|
| `internal/apiserver/handler` | `pdinferenceservice_test.go` | L1 | 5 个 HTTP Handler（10 个用例）|
| `internal/apiserver` | `server_test.go` | L1 | 路由注册、优雅关闭、健康检查 |
| `internal/controller` | `pdinferenceservice_controller_test.go` | L2 | Reconcile 主流程、Finalizer、RBG CRUD |
| `internal/webhook/v1alpha1` | `pdinferenceservice_webhook_test.go` | L2 | 默认值注入、校验规则、不可变字段 |
| `cmd` | `main_test.go` | L2 | Scheme 注册、Manager 启动冒烟 |

---

## L3：a30 集群冒烟测试

### 部署 pd-manager 到 a30

```bash
# 1. WSL 构建镜像
make docker-build IMG=<registry>/pd-manager:dev

# 2. 推送到 a30 可访问的 Registry
make docker-push IMG=<registry>/pd-manager:dev

# 3. 在 a30 上部署（通过 KUBECONFIG 指向 a30）
export KUBECONFIG=/path/to/a30-kubeconfig
kubectl apply -k config/default

# 4. 等待 pd-manager 就绪
kubectl rollout status deploy/pd-manager-controller-manager -n pd-system -timeout=60s
```

### 冒烟验证脚本

```bash
#!/bin/bash
# smoke-test.sh — a30 集群冒烟测试

set -e

echo "=== 1. 验证 CRD 注册 ==="
kubectl get crd pdinferenceservices.pdai.pdai.io
kubectl get crd pdengineprofiles.pdai.pdai.io
kubectl get crd rolebasedgroups.workloads.x-k8s.io

echo "=== 2. 创建 PDInferenceService ==="
kubectl apply -f - <<EOF
apiVersion: pdai.pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: smoke-test
  namespace: default
spec:
  model: test-model
  modelStorage:
    type: hostPath
    hostPath: /tmp/models
    mountPath: /models
  images:
    scheduler: lmsysorg/sgl-model-gateway:v0.3.1
    prefill: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
    decode: lmsysorg/sglang:v0.5.8-cu130-amd64-runtime
  prefill:
    replicas: 1
    resources:
      gpu: "1"
  decode:
    replicas: 1
    resources:
      gpu: "1"
EOF

echo "=== 3. 验证 RBG 被创建 ==="
kubectl wait rbg smoke-test --for=condition=Ready=Unknown --timeout=30s || true
kubectl get rbg smoke-test -o jsonpath='{.spec.roles[*].name}'

echo "=== 4. 验证 Finalizer 被注入 ==="
kubectl get pdis smoke-test -o jsonpath='{.metadata.finalizers}'
# 期望包含 pdai.io/finalizer

echo "=== 5. 验证 REST API ==="
PD_SVC=$(kubectl get svc -n pd-system -o jsonpath='{.items[0].spec.clusterIP}')
curl -s http://${PD_SVC}:8080/healthz
curl -s http://${PD_SVC}:8080/api/v1/pd-inference-services | python3 -m json.tool

echo "=== 6. 测试 Webhook 拒绝无效请求 ==="
kubectl apply -f - <<EOF && echo "ERROR: should have been rejected" || echo "OK: rejected as expected"
apiVersion: pdai.pdai.io/v1alpha1
kind: PDInferenceService
metadata:
  name: invalid-test
  namespace: default
spec:
  model: test
  modelStorage:
    type: hostPath
    hostPath: /tmp/models
  prefill:
    replicas: 1
    resources:
      gpu: "1"
  decode:
    replicas: 1
    resources:
      gpu: "1"
EOF
# 期望：被 Webhook 拒绝（无 images 字段）

echo "=== 7. 清理 ==="
kubectl delete pdis smoke-test --ignore-not-found
echo "=== 冒烟测试完成 ==="
```

### 冒烟测试覆盖点

| 验证点 | 命令 | 期望结果 |
|--------|------|---------|
| CRD 注册 | `kubectl get crd pdinferenceservices...` | 资源存在 |
| RBG 创建 | `kubectl get rbg <name>` | 在 30s 内创建 |
| Finalizer 注入 | `kubectl get pdis <name> -o jsonpath=...` | 含 `pdai.io/finalizer` |
| REST API 健康 | `curl /healthz` | 200 OK |
| REST API 列表 | `curl /api/v1/pd-inference-services` | 200，含 items |
| Webhook 拒绝 | 无 images 的 PDIS | 创建被拒绝 |
| 级联删除 | `kubectl delete pdis` | RBG 自动删除 |

---

## CI/CD 集成

### GitHub Actions 示例

```yaml
# .github/workflows/test.yml
name: Test

on: [push, pull_request]

jobs:
  unit-test:
    name: L1/L2 Tests
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'

      - name: Setup envtest
        run: make envtest

      - name: Run tests
        run: go test ./... -v -timeout 5m

      - name: Generate coverage report
        run: |
          go test ./... -coverprofile=coverage.out -timeout 5m
          go tool cover -func=coverage.out | tail -1

      - name: Upload coverage
        uses: codecov/codecov-action@v4
        with:
          files: coverage.out
```

---

## 新成员快速上手（30 分钟 L1+L2 验证）

```bash
# 1. 克隆仓库并进入目录
git clone <repo> pd-manager && cd pd-manager

# 2. 安装 Go 依赖
go mod download

# 3. 安装 envtest
make envtest

# 4. 运行 L1 单元测试（约 30 秒）
go test ./api/... ./internal/config/... ./internal/translator/... ./internal/apiserver/... -v
# 期望：全部 PASS

# 5. 运行 L2 集成测试（约 2~3 分钟）
go test ./internal/controller/... ./internal/webhook/... ./cmd/... -v -timeout 5m
# 期望：全部 PASS

# 6. 查看覆盖率摘要
go test ./... -coverprofile=coverage.out -timeout 5m
go tool cover -func=coverage.out | grep total
# 期望：total coverage > 70%
```

如有问题，请检查：
- `bin/k8s/` 目录是否存在 envtest 二进制（运行 `make envtest` 安装）
- Go 版本是否 >= 1.24（`go version`）
- RBG 本地模块路径是否正确（`go.mod` 中 `replace sigs.k8s.io/rbgs => /home/zzy/code/rbg`）
