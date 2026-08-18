DIST   := build
GOOS   ?= darwin
GOARCH ?= amd64

LDFLAGS  := -s -w
SUFFIX   := $(GOOS)_$(GOARCH)
CMDS     := benchmark evaluation performance

# HuggingFace 数据集下载目录，benchmark 通过 //go:embed hf 打包
HF_DIR   := cmd/benchmark/internal/dataset/hf

.PHONY: all build $(addprefix build-,$(CMDS)) setup test tidy fmt vet clean clean-dist help

all: build

## build: 构建全部子命令
build: $(addprefix build-,$(CMDS))

## build-benchmark: 构建 benchmark（需先执行 make setup 拉取数据集）
build-benchmark:
	@mkdir -p $(DIST)/benchmark
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(LDFLAGS)" -o $(DIST)/benchmark/benchmark-$(SUFFIX) ./cmd/benchmark
	@cp cmd/benchmark/configs/config.example.yml $(DIST)/benchmark/config.yml

## build-evaluation: 构建 evaluation
build-evaluation:
	@mkdir -p $(DIST)/evaluation
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(LDFLAGS)" -o $(DIST)/evaluation/evaluation-$(SUFFIX) ./cmd/evaluation
	@cp cmd/evaluation/configs/config.example.yml $(DIST)/evaluation/config.yml

## build-performance: 构建 performance
build-performance:
	@mkdir -p $(DIST)/performance
	@GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(LDFLAGS)" -o $(DIST)/performance/performance-$(SUFFIX) ./cmd/performance
	@cp cmd/performance/configs/config.example.yaml $(DIST)/performance/config.yaml

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
