# llm-inspector

Language: **English** | [简体中文](README.zh-CN.md)

A monorepo of independent Go command-line tools for testing and evaluating LLM (Large Language Model) API endpoints — covering benchmarking, multi-layer availability/capability evaluation, and
load/stress testing.

## Modules

Each tool lives in its own directory under `cmd/` as an independent Go module. See each module's own README for full usage details.

| Module        | Path                                 | Description                                                                                                                                                                                               |
|---------------|--------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `benchmark`   | [`cmd/benchmark`](cmd/benchmark)     | OpenAI-compatible benchmark tool that runs a fixed problem set (2025 AIME I/II math problems) against a model, measuring TTFT/TPS/TPM and verifying answers extracted from `\boxed{}`.                    |
| `evaluation`  | [`cmd/evaluation`](cmd/evaluation)   | Five-layer (L1-L5) LLM availability and capability evaluator. Supports OpenAI-compatible, Anthropic Messages API, and Gemini `generateContent` API targets; produces a pass/fail verdict suitable for CI. |
| `performance` | [`cmd/performance`](cmd/performance) | Concurrent load-testing tool with a terminal UI, multi-model / multi-token / multi-concurrency benchmarking, and Excel report export.                                                                     |

## Repository layout

```
llm-inspector/
├── cmd/
│   ├── benchmark/     # AIME benchmark tool
│   ├── evaluation/    # 5-layer availability & capability evaluator
│   └── performance/   # Load-testing tool with TUI + Excel export
├── LICENSE
└── README.md
```

Each module keeps its own `go.mod`, `go.sum`, and `Makefile`, and can be built and run independently of the others.

## Requirements

- Go 1.26.5 or later

## Quick start

```bash
# benchmark: run the AIME-style benchmark
cd cmd/benchmark
export OPENAI_API_KEY="your-api-key"
go run main.go

# evaluation: run the 5-layer evaluation
cd cmd/evaluation
go build -o evaluation .
cp configs/eval.example.yml eval.yml   # edit target.base_url / api_key / model
./evaluation run --config eval.yml

# performance: run a load test
cd cmd/performance
cp configs/config.example.yaml config.yaml   # edit models / token_groups / base_url / concurrency
go run . -config config.yaml
```

See each module's README for configuration options, scoring rubrics, and report formats:

- [`cmd/benchmark/README.md`](cmd/benchmark/README.md)
- [`cmd/evaluation/README.md`](cmd/evaluation/README.md)
- The `performance` module has no standalone README yet — all runtime parameters live in the YAML config; see [
  `cmd/performance/configs/config.example.yaml`](cmd/performance/configs/config.example.yaml) for every option.

## License

[MIT](LICENSE) © 2026 Ayakura Yuki
