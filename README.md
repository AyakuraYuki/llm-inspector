# llm-inspector

Language: **English** | [简体中文](README.zh-CN.md)

A monorepo of independent Go command-line tools for testing and evaluating LLM (Large Language Model) API endpoints — covering benchmarking, multi-layer availability/capability evaluation, and
load/stress testing.

## Modules

Each tool lives in its own directory under `cmd/` as an independent command, sharing one repo-wide Go module. See each module's own README for full usage details.

| Module        | Path                                 | Description                                                                                                                                                                                                |
|---------------|--------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `benchmark`   | [`cmd/benchmark`](cmd/benchmark)     | OpenAI-compatible benchmark tool that runs built-in problem sets (AIME 2025/2026, MMLU-Pro) plus custom questions against a model, measuring TTFT/TPS/TPM and verifying answers extracted from `\boxed{}`. |
| `evaluation`  | [`cmd/evaluation`](cmd/evaluation)   | Six-layer (L1-L6) LLM availability and capability evaluator. Supports OpenAI-compatible, Anthropic Messages API, and Gemini `generateContent` API targets; produces a pass/fail verdict suitable for CI.   |
| `performance` | [`cmd/performance`](cmd/performance) | Concurrent load-testing tool for OpenAI / Anthropic / Gemini / Responses / image-generation endpoints, with a terminal UI, error-rate early-stop, cache-hit-ratio tracking, and Excel report export.       |

## Repository layout

```
llm-inspector/
├── cmd/
│   ├── benchmark/     # AIME/MMLU-Pro benchmark tool
│   ├── evaluation/    # 6-layer availability & capability evaluator
│   └── performance/   # Load-testing tool with TUI + Excel export
├── go.mod / go.sum    # single repo-wide Go module
├── Makefile           # shared build/setup/test targets for all three tools
├── LICENSE
└── README.md
```

All three tools live in a single Go module rooted at the repo root (one `go.mod` / `go.sum`), with one root `Makefile` that builds each tool independently via its own `build-<tool>` target.

## Requirements

- Go 1.27 or later

## Quick start

```bash
# from the repo root

# fetch benchmark's embedded huggingface datasets (one-time)
make setup

# build all three tools, or just one of them
make build
make build-benchmark
make build-evaluation
make build-performance

# benchmark: run the AIME/MMLU-Pro-style benchmark (config is mandatory)
cp cmd/benchmark/configs/config.example.yml benchmark.yml   # edit base_url / api_key / model
./build/benchmark/benchmark-darwin_amd64 -config benchmark.yml

# evaluation: run the 6-layer evaluation
cp cmd/evaluation/configs/config.example.yml eval.yml   # edit target.base_url / api_key / model
./build/evaluation/evaluation-darwin_amd64 run --config eval.yml

# performance: run a load test
cp cmd/performance/configs/config.example.yaml config.yaml   # edit models / token_groups / base_url / concurrency
./build/performance/performance-darwin_amd64 -config config.yaml
```

Cross-compile with `make build-<tool> GOOS=linux GOARCH=arm64` (defaults to `darwin/amd64`). You can also skip `make` and run any tool straight from source, e.g.
`go run ./cmd/benchmark -config benchmark.yml`.

See each module's README for configuration options, scoring rubrics, and report formats:

- [`cmd/benchmark/README.md`](cmd/benchmark/README.md)
- [`cmd/evaluation/README.md`](cmd/evaluation/README.md)
- [`cmd/performance/README.md`](cmd/performance/README.md)

## License

[MIT](LICENSE) © 2026 Ayakura Yuki
