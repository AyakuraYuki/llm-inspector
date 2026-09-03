# llm-inspector

语言: [English](README.md) | **简体中文**

这是一个 Go 语言的 monorepo，包含多个相互独立的命令行工具，用于测试和评估 LLM（大语言模型）API 端点——涵盖基准测试、多层级可用性/能力评测、以及负载压测。

## 模块

每个工具都在 `cmd/` 下以独立命令的形式存在，共享仓库统一的 Go module，详细用法见各模块自己的 README。

| 模块          | 路径                                 | 说明                                                                                                                                                                          |
|---------------|--------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `benchmark`   | [`cmd/benchmark`](cmd/benchmark)     | 基于 OpenAI-Compatible API 的基准测试工具，内置 AIME 2025/2026、MMLU-Pro 题库并支持自定义问题，对模型发起测试，统计 TTFT/TPS/TPM 并从 `\boxed{}` 中提取答案进行验证。         |
| `evaluation`  | [`cmd/evaluation`](cmd/evaluation)   | 六层（L1-L6）大语言模型可用性与能力评测工具，支持 OpenAI 兼容、Anthropic Messages API、Gemini `generateContent` API 三种协议的目标端点，输出可直接接入 CI 的 pass/fail 结论。 |
| `performance` | [`cmd/performance`](cmd/performance) | 面向 OpenAI / Anthropic / Gemini / Responses / 图片生成等端点的并发压测工具，带终端 TUI、错误率早停、缓存命中率统计，并可导出 Excel 报告。                                    |

## 仓库结构

```
llm-inspector/
├── cmd/
│   ├── benchmark/     # AIME/MMLU-Pro 基准测试工具
│   ├── evaluation/    # 六层可用性与能力评测工具
│   └── performance/   # 带 TUI 与 Excel 导出的压测工具
├── go.mod / go.sum    # 仓库统一的单一 Go module
├── Makefile           # 三个工具共用的 build/setup/test target
├── LICENSE
└── README.md
```

三个工具共享仓库根目录下 **单一**的 Go module（一份 `go.mod` / `go.sum`），以及一份根 `Makefile`，其中每个工具都有各自独立的 `build-<tool>` target。

## 环境要求

- Go 1.27 及以上

## 快速开始

```bash
# 以下命令均在仓库根目录执行

# 拉取 benchmark 内置数据集（go:embed 依赖，只需执行一次）
make setup

# 构建全部三个工具，或只构建其中一个
make build
make build-benchmark
make build-evaluation
make build-performance

# benchmark：运行 AIME/MMLU-Pro 风格基准测试（-config 为必填参数）
cp cmd/benchmark/configs/config.example.yml benchmark.yml   # 修改 base_url / api_key / model
./build/benchmark/benchmark-darwin_amd64 -config benchmark.yml

# evaluation：运行六层评测
cp cmd/evaluation/configs/config.example.yml eval.yml   # 修改 target 的 base_url / api_key / model
./build/evaluation/evaluation-darwin_amd64 run --config eval.yml

# performance：运行一次压测
cp cmd/performance/configs/config.example.yaml config.yaml   # 修改 models / token_groups / base_url / concurrency
./build/performance/performance-darwin_amd64 -config config.yaml
```

交叉编译使用 `make build-<tool> GOOS=linux GOARCH=arm64`（默认 `darwin/amd64`）。也可以不走 `make`，直接从源码运行任意工具，例如 `go run ./cmd/benchmark -config benchmark.yml`。

配置项、评分口径、报告格式等详见各模块 README：

- [`cmd/benchmark/README.md`](cmd/benchmark/README.md)
- [`cmd/evaluation/README.md`](cmd/evaluation/README.md)
- [`cmd/performance/README.md`](cmd/performance/README.md)

## 许可证

[MIT](LICENSE) © 2026 Ayakura Yuki
