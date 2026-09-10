DIST   := build
GOOS   ?= darwin
GOARCH ?= amd64

LDFLAGS  := -s -w
CMDS     := benchmark evaluation performance performance-cluster imagespec

# GOOS -> 发布用 OS 名
ifeq ($(GOOS),darwin)
  RELEASE_OS := macOS
else ifeq ($(GOOS),linux)
  RELEASE_OS := linux
else ifeq ($(GOOS),windows)
  RELEASE_OS := windows
else
  RELEASE_OS := $(GOOS)
endif

# GOARCH -> 发布用架构名（macOS: aarch64/x64；windows: arm64/x64；linux: 保留原名）
ifeq ($(GOARCH),arm64)
  ifeq ($(GOOS),darwin)
    RELEASE_ARCH := aarch64
  else
    RELEASE_ARCH := arm64
  endif
else ifeq ($(GOARCH),amd64)
  ifeq ($(GOOS),linux)
    RELEASE_ARCH := amd64
  else
    RELEASE_ARCH := x64
  endif
else
  RELEASE_ARCH := $(GOARCH)
endif

SUFFIX   := $(RELEASE_OS)_$(RELEASE_ARCH)

PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

EXE_EXT  :=
ifeq ($(GOOS),windows)
EXE_EXT  := .exe
endif

# HuggingFace 数据集下载目录，benchmark 通过 //go:embed hf 打包
HF_DIR   := cmd/benchmark/internal/dataset/hf

.PHONY: all build release-all $(addprefix build-,$(CMDS)) setup test tidy fmt vet clean clean-dist help

all: build

## build: 构建全部子命令
build: $(addprefix build-,$(CMDS))

## release-all: 为 darwin/linux/windows 的 amd64/arm64 构建全部产物
release-all:
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		echo "==> building $$os/$$arch"; \
		$(MAKE) build GOOS=$$os GOARCH=$$arch; \
	done

## build-benchmark: 构建 benchmark（需先执行 make setup 拉取数据集）
build-benchmark:
	@mkdir -p $(DIST)/benchmark
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(LDFLAGS)" -o $(DIST)/benchmark/benchmark-$(SUFFIX)$(EXE_EXT) ./cmd/benchmark
	@cp cmd/benchmark/configs/config.example.yml $(DIST)/benchmark/config.yml
	@cp cmd/benchmark/使用教程-macOS.txt cmd/benchmark/使用教程-Windows.txt $(DIST)/benchmark/

## build-evaluation: 构建 evaluation
build-evaluation:
	@mkdir -p $(DIST)/evaluation
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(LDFLAGS)" -o $(DIST)/evaluation/evaluation-$(SUFFIX)$(EXE_EXT) ./cmd/evaluation
	@cp cmd/evaluation/configs/config.example.yml $(DIST)/evaluation/config.yml
	@cp cmd/evaluation/使用教程-macOS.txt cmd/evaluation/使用教程-Windows.txt $(DIST)/evaluation/

## build-performance: 构建 performance
build-performance:
	@mkdir -p $(DIST)/performance
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(LDFLAGS)" -o $(DIST)/performance/performance-$(SUFFIX)$(EXE_EXT) ./cmd/performance
	@cp cmd/performance/configs/config.example.yaml $(DIST)/performance/config.yaml
	@cp cmd/performance/使用教程-macOS.txt cmd/performance/使用教程-Windows.txt $(DIST)/performance/

## build-performance-cluster: 构建 performance-cluster（agent 与 coordinator 同一二进制）
build-performance-cluster:
	@mkdir -p $(DIST)/performance-cluster
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(LDFLAGS)" -o $(DIST)/performance-cluster/performance-cluster-$(SUFFIX)$(EXE_EXT) ./cmd/performance/cluster
	@cp cmd/performance/cluster/configs/config.example.yaml $(DIST)/performance-cluster/config.yaml
	@cp cmd/performance/cluster/使用教程-macOS.txt cmd/performance/cluster/使用教程-Windows.txt $(DIST)/performance-cluster/

## build-imagespec: 构建 imagespec
build-imagespec:
	@mkdir -p $(DIST)/imagespec
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(LDFLAGS)" -o $(DIST)/imagespec/imagespec-$(SUFFIX)$(EXE_EXT) ./cmd/imagespec
	@cp cmd/imagespec/configs/config.example.yaml $(DIST)/imagespec/config.yaml
	@cp cmd/imagespec/使用教程-macOS.txt cmd/imagespec/使用教程-Windows.txt $(DIST)/imagespec/

## setup:
##     1. 安装 staticcheck
##     2. 拉取 benchmark 依赖的 HuggingFace 数据集
setup:
	@go install honnef.co/go/tools/cmd/staticcheck@latest
	@mkdir -p $(HF_DIR)/math-ai/aime25
	@hf download math-ai/aime25 --repo-type dataset --local-dir $(HF_DIR)/math-ai/aime25/
	@mkdir -p $(HF_DIR)/math-ai/aime26
	@hf download math-ai/aime26 --repo-type dataset --local-dir $(HF_DIR)/math-ai/aime26/
	@mkdir -p $(HF_DIR)/TIGER-Lab/MMLU-Pro
	@hf download TIGER-Lab/MMLU-Pro --repo-type dataset --local-dir $(HF_DIR)/TIGER-Lab/MMLU-Pro/

## test: 运行全部单元测试
test:
	@go test ./...

## tidy: 整理模块依赖
tidy:
	@go mod tidy

## fmt: 格式化代码
fmt:
	@go fmt ./...

## vet: 静态检查
vet:
	@echo "== use staticcheck insteaded =="
	@staticcheck ./...
	@echo "== done =="

## clean-dist: 仅清理构建产物
clean-dist:
	@rm -rf $(DIST)/

## clean: 清理构建产物与 Go 构建缓存
clean: clean-dist
	@go clean -cache

## help: 显示可用目标
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
