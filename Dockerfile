# Build the manager binary
FROM docker.io/golang:1.24 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy module files
COPY go.mod go.mod
COPY go.sum go.sum

# ── 依赖获取（二选一，根据网络环境选择）────────────────────────────────────────
# 方式 A（默认）：联网下载，适用于能访问 proxy.golang.org 的环境
RUN go mod download
# 方式 B（离线/air-gap）：使用预先准备的 vendor 目录，需先在宿主机执行
#   go mod vendor  &&  # 确保 vendor/modules.txt 中 replace 路径与构建环境一致
# 取消注释以下两行并注释上方 RUN go mod download：
# COPY vendor/ vendor/
# （构建命令中同时加 -mod=vendor，见下方）
# ─────────────────────────────────────────────────────────────────────────────

# Copy the go source
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/

# Build
# 方式 A（联网）：
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go
# 方式 B（离线，需先 COPY vendor/ 并注释上行）：
# RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -mod=vendor -a -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
# 注：若 distroless 镜像无法拉取（如离线环境），可替换为 FROM alpine:3.19
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
