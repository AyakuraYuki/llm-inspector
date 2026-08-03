# llm-inspector

语言: [English](README.md) | **简体中文**

这是一个 Go 语言的 monorepo，包含多个相互独立的命令行工具，用于测试和评估 LLM（大语言模型）API 端点——涵盖基准测试、多层级可用性/能力评测、以及负载压测。

## 模块

每个工具都在 `cmd/` 下以独立 Go module 的形式存在，详细用法见各模块自己的 README。

| 模块                       | 路径                                 | 说明                                                                                                                                                                          |
|----------------------------|--------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `benchmark`                | [`cmd/benchmark`](cmd/benchmark)     | 基于 OpenAI-Compatible API 的基准测试工具，使用固定题库（2025 AIME I/II 数学题）对模型发起测试，统计 TTFT/TPS/TPM 并从 `\boxed{}` 中提取答案进行验证。                        |
| `evaluation`（`llm-eval`） | [`cmd/evaluation`](cmd/evaluation)   | 五层（L1-L5）大语言模型可用性与能力评测工具，支持 OpenAI 兼容、Anthropic Messages API、Gemini `generateContent` API 三种协议的目标端点，输出可直接接入 CI 的 pass/fail 结论。 |
| `performance`              | [`cmd/performance`](cmd/performance) | 带终端 TUI 界面的并发压测工具，支持多模型/多 token/多并发档位组合测试，并可导出 Excel 报告。                                                                                  |

## 仓库结构

```
llm-inspector/
├── cmd/
│   ├── benchmark/     # AIME 基准测试工具
│   ├── evaluation/    # llm-eval：五层可用性与能力评测工具
│   └── performance/   # 带 TUI 与 Excel 导出的压测工具
├── LICENSE
└── README.md
```

各模块拥有各自独立的 `go.mod`、`go.sum` 与 `Makefile`，可以互不依赖地单独构建和运行。

## 环境要求

- Go 1.26.5 及以上

## 快速开始

```bash
# benchmark：运行 AIME 风格基准测试
cd cmd/benchmark
export OPENAI_API_KEY="你的 API key"
go run main.go

# evaluation：运行五层评测
cd cmd/evaluation
go build -o llm-eval .
cp configs/eval.example.yml eval.yml   # 修改 target 的 base_url / api_key / model
./llm-eval run --config eval.yml

# performance：运行一次压测
cd cmd/performance
cp configs/config.example.yaml config.yaml   # 修改 models / tokens / base_url / concurrency
go run . -config config.yaml
```

配置项、评分口径、报告格式等详见各模块 README：

- [`cmd/benchmark/README.md`](cmd/benchmark/README.md)
- [`cmd/evaluation/README.md`](cmd/evaluation/README.md)
- `performance` 模块目前暂无独立 README，全部运行参数都在 YAML 配置里，可参见 [`cmd/performance/configs/config.example.yaml`](cmd/performance/configs/config.example.yaml) 了解每一项。

## 许可证

[MIT](LICENSE) © 2026 Ayakura Yuki
