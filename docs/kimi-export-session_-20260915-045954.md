---
session_id: session_7e257b67-7440-47b6-9314-6b9a0bc76af1
exported_at: 2026-09-15T04:59:54.612Z
work_dir: /Users/ayakurayuki/go/src/github.com/AyakuraYuki/llm-inspector
message_count: 23
token_count: 51302
---

# Kimi Session Export

## Overview

- **Topic**: 对比一下 @cmd/performance/ 和 `evalscope`，看看我的压测程序跟 `evalscope` 的指标有什么差距？谁的测试结果更可信？
- **Conversation**: 2 turns | 11 tool calls

---

## Turn 1

### User

对比一下 @cmd/performance/ 和 `evalscope`，看看我的压测程序跟 `evalscope` 的指标有什么差距？谁的测试结果更可信？

### Assistant

<details><summary>Thinking</summary>

The user wants me to compare their benchmark program in `cmd/performance/` with `evalscope` (a benchmarking tool from ModelScope for LLM performance testing). They want to know:
1. What metrics gaps exist between their tool and evalscope
2. Whose test results are more credible

First, I need to explore the `cmd/performance/` directory to understand what their benchmark program does. This is a substantial investigation task — I should explore the code first myself, then possibly look up evalscope's documentation/metrics.

Let me start by looking at the project structure and the performance command code. I could delegate exploration, but let me first get a quick look at what's there.

Let me explore the cmd/performance directory and also research evalscope's metrics. I can do both in parallel:
1. Explore the local code (I can do this myself or via an explore agent)
2. Fetch evalscope docs from the web

Let me first look at the local code structure quickly.

</details>

我先看看你本地压测程序的实现，同时查一下 evalscope 的指标定义。

#### Tool Call: Bash (`find cmd/performance -type f | head -50 && echo "---" && wc …`)
<!-- call_id: Bash_0_ae7e8023 -->
```json
{
  "command": "find cmd/performance -type f | head -50 && echo \"---\" && wc -l cmd/performance/*.go 2>/dev/null; find cmd/performance -name \"*.go\" | xargs wc -l | tail -5"
}
```

#### Tool Call: FetchURL (`https://evalscope.readthedocs.io/en/latest/user_guides/stres…`)
<!-- call_id: FetchURL_1_d68eadc3 -->
```json
{
  "url": "https://evalscope.readthedocs.io/en/latest/user_guides/stress_test/index.html"
}
```

<details><summary>Tool Result: Bash (`find cmd/performance -type f | head -50 && echo "---" && wc …`)</summary>

<!-- call_id: Bash_0_ae7e8023 -->
cmd/performance/cluster/internal/coord/merge.go
cmd/performance/cluster/internal/coord/run.go
cmd/performance/cluster/internal/coord/merge_test.go
cmd/performance/cluster/internal/coord/split_test.go
cmd/performance/cluster/internal/coord/client.go
cmd/performance/cluster/internal/coord/split.go
cmd/performance/cluster/internal/coord/integration_test.go
cmd/performance/cluster/internal/coord/progress.go
cmd/performance/cluster/internal/proto/proto.go
cmd/performance/cluster/internal/agentd/server.go
cmd/performance/cluster/internal/agentd/task.go
cmd/performance/cluster/internal/agentd/reporter.go
cmd/performance/cluster/README.md
cmd/performance/cluster/使用教程-macOS.txt
cmd/performance/cluster/configs/config.example.yaml
cmd/performance/cluster/使用教程-Windows.txt
cmd/performance/cluster/main.go
cmd/performance/.DS_Store
cmd/performance/internal/metrics/metrics.go
cmd/performance/internal/metrics/metrics_test.go
cmd/performance/internal/types/types.go
cmd/performance/internal/config/config.go
cmd/performance/internal/config/config_test.go
cmd/performance/internal/reporter/console.go
cmd/performance/internal/reporter/reporter.go
cmd/performance/internal/reporter/tui.go
cmd/performance/internal/runner/runner.go
cmd/performance/internal/runner/api.go
cmd/performance/internal/runner/runner_test.go
cmd/performance/internal/prompts/prompts.go
cmd/performance/internal/report/excel.go
cmd/performance/internal/report/structured.go
cmd/performance/internal/report/report.go
cmd/performance/README.md
cmd/performance/使用教程-macOS.txt
cmd/performance/configs/config.example.yaml
cmd/performance/使用教程-Windows.txt
cmd/performance/main.go
---
     217 cmd/performance/main.go
     633 cmd/performance/internal/report/excel.go
     210 cmd/performance/internal/report/structured.go
     247 cmd/performance/internal/report/report.go
     217 cmd/performance/main.go
    6452 total


</details>

<details><summary>Tool Result: FetchURL (`https://evalscope.readthedocs.io/en/latest/user_guides/stres…`)</summary>

<!-- call_id: FetchURL_1_d68eadc3 -->
The returned content is the main text extracted from the page. If you use it in your answer, cite this page as a markdown link, e.g. [title](url).

# Model Inference Stress Testing | EvalScope

© 2022-2024, Alibaba ModelScope Built with Sphinx 9.1.0

</details>

<details><summary>Thinking</summary>

The evalscope docs page didn't render well (JS-heavy Sphinx). Let me explore the local code in depth with an explore agent, and separately fetch evalscope's metrics documentation. The evalscope stress test metrics docs — let me try the parameters page or examples page. Actually the useful page is: https://evalscope.readthedocs.io/en/latest/user_guides/stress_test/parameters.html and also the "examples" page with metrics descriptions. Let me also check their GitHub README for metric definitions.

Let me delegate the local code exploration to an explore agent (thorough), and meanwhile fetch evalscope docs myself.

</details>

#### Tool Call: Agent (`explore`)
<!-- call_id: Agent_2_513df288 -->
```json
{
  "subagent_type": "explore",
  "prompt": "Thoroughness: thorough. Explore the Go benchmark/stress-test program located at cmd/performance/ in the repo at /Users/ayakurayuki/go/src/github.com/AyakuraYuki/llm-inspector (note: cmd/performance/cluster/ is a distributed variant — cover it too, but focus on the core single-node tool).\n\nI need a complete technical picture of WHAT metrics it measures and HOW it measures them, to compare against the evalscope stress-test tool. Answer these questions with concrete file/line references:\n\n1. Load generation model: open-loop vs closed-loop? Does it use a fixed number of workers (concurrency) with request queues, a fixed request rate (RPS), or both? How is concurrency controlled? Does it support ramp-up?\n2. Request sampling: how are prompts generated (random? fixed? dataset?)? Does it control input length / output length (max_tokens)? Does it randomize prompt content (e.g., random prefixes/suffixes) to defeat prefix caching? Does it support streaming?\n3. Metrics collected: full list. Specifically look for: TTFT (time to first token), TPOT/ITL (time per output token / inter-token latency), end-to-end latency (E2E), throughput (output tokens/s, total tokens/s, request/s/QPS), percentiles (P50/P90/P99 etc.), success rate, token counts (prompt tokens, completion tokens — from API usage field or local tokenizer estimate?).\n4. How each metric is computed: e.g., is TTFT measured from request send to first SSE chunk? Does it include connection setup/queue time? Is throughput computed per-request (mean of per-request rates) or aggregate (total tokens / wall time)? How are percentiles computed (exact sort? histogram?)? Does it count tokens locally with a tokenizer (check internal/tokenizers) or trust the API's usage field?\n5. Does it measure/strip client-side overhead? Timeout handling, retries, error classification?\n6. Warmup, cooldown, duration-based vs request-count-based runs?\n7. Statistical treatment: standard deviation, min/max, median, percentile method?\n8. What does the cluster/ variant add (multi-agent coordination, result merging — how does merge combine percentiles/metrics across agents)?\n9. API compatibility: OpenAI-compatible chat/completions only? Other endpoints?\n\nReport a structured summary. Include exact metric names as printed in its reports (check internal/report, internal/reporter, internal/metrics). Quote key formulas/code snippets (short) where they matter for correctness assessment.",
  "description": "Explore local benchmark tool"
}
```

#### Tool Call: FetchURL (`https://raw.githubusercontent.com/modelscope/evalscope/main/…`)
<!-- call_id: FetchURL_3_589dfc14 -->
```json
{
  "url": "https://raw.githubusercontent.com/modelscope/evalscope/main/docs/en/user_guides/stress_test/index.md"
}
```

<details><summary>Tool Result: FetchURL (`https://raw.githubusercontent.com/modelscope/evalscope/main/…`)</summary>

<!-- call_id: FetchURL_3_589dfc14 -->
The returned content is the full response body, returned verbatim. If you use it in your answer, cite this page as a markdown link, e.g. [title](url).

# Model Inference Stress Testing
A stress testing tool for large language models that can be customized to support various dataset formats and different API protocol formats, with default support for the OpenAI API format.

:::{toctree}
:maxdepth: 2

quick_start.md
agentx.md
parameters.md
examples.md
multi_turn.md
sla_auto_tune.md
speed_benchmark.md
vs_vllm_bench.md
custom.md
:::


</details>

<details><summary>Tool Result: Agent (`explore`)</summary>

<!-- call_id: Agent_2_513df288 -->
agent_id: agent-0
actual_subagent_type: explore
status: completed
stop_reason: completed

[summary]
I now have a complete picture. Here's the full technical analysis.

# `cmd/performance` (llm-inspector) — technical breakdown

Core engine: `cmd/performance/internal/{runner,metrics,report,reporter,prompts,config,types}` + shared protocol parsing in `internal/llm/{sse,tokstats}`. The cluster variant reuses the same engine and aggregator verbatim.

## 1. Load generation model — both closed-loop AND open-loop, level-stepped

Mode is per-run config `load_mode: closed|open` (`cmd/performance/internal/config/config.go:33-40`), default **closed**. The run iterates `models × concurrency` levels sequentially (`runner/runner.go:42-80`), each level duration-based (default 60s, `config.go:45`), default concurrency ladder `[10,20,30,40,50,75,100,120,150]` (`config.go:60`).

- **Closed-loop** (`runner.go:253-283`): exactly `concurrency` goroutines; each loops `request → wait full response → sleep cooldownPerRequest (300ms fixed think time, runner.go:19) → next` until the deadline.
- **Open-loop** (`runner.go:293-327`): Poisson arrivals at a target RPS. Inter-arrival by inverse-transform sampling: `dt = -ln(1-U)/rate` (runner.go:317). `request_rate[i]` pairs 1:1 with `concurrency[i]` per level (validated `config.go:279-292`); `concurrency[i]` becomes a **max in-flight cap** via channel semaphore — when full, the next arrival blocks (back-pressure), so it degrades to rate-capped rather than unbounded open-loop (comment at runner.go:290-292 explicitly compares this to AIPerf's request-rate + max-concurrency).
- **Ramp-up**: staggered worker start, ~1ms/worker, capped at 5s and ≤ duration/6 — `RampDuration` (runner.go:151-153); closed-loop only (Poisson arrivals are already spread). No intra-level rate ramp; levels step.

The header prints the mode and even warns about coordinated omission in closed mode (`main.go:183-187`).

## 2. Request sampling

Three mutually exclusive prompt modes (`config.go:18-28`, `types.go:146-155`):
- `text` — one fixed prompt for every request (default; a ~120-word essay question, `config.go:50`).
- `dynamic` — per-request random long prompt targeting `prompt.tokens` (default 2000): shuffled paragraphs from a 19-paragraph corpus + a `[bench-nonce <unixnano>-<rand>]` prefix to defeat content/prefix caching (`prompts.go:43-63`). Length control is approximate: `targetChars = tokens * 4`.
- `codex` — fixed long system prompt + random short question, deliberately high-similarity traffic to *exercise* prefix caching (`prompts.go:70-131`).

Output length is **fixed**: `max_tokens = 8192` for all protocols (`runner/api.go:137-139`). No dataset support, no input-length distribution beyond the two knobs above. All text providers are **streaming-only** (SSE); image generation is a synchronous POST.

## 3 & 4. Metrics collected and how they're computed

Per-request raw record `RequestMetrics` (`types.go:61-75`), per-level aggregate `AggregatedMetrics` (`types.go:194-248`). Exact printed names (console `report/report.go:43-62`, JSON `report/structured.go:69-83`, CSV headers `structured.go:157-165`, Excel sheets `report/excel.go`):

| Printed/exported name | Definition |
|---|---|
| `TTFT` (`ttft_ms`) | t0 → first SSE event containing output **or reasoning** content (`sse.HasOutputContent`, `internal/llm/sse/sse.go:119-177`). t0 is taken before payload marshaling and `http.Client.Do` (`api.go:248/289/…`), so TTFT **includes TCP/TLS connect (if not reused), send, server queue, prefill**. Fallback: if no content event but usage reports completion tokens, TTFT = first-byte time (`api.go:546-557`) |
| `TPOT` (`tpot_ms`) | `(TotalLatency − TTFT) / OutputTokens` (`metrics.go:97-101`) — generation window ÷ token count |
| `ITL` (`itl_ms`) | gaps between consecutive output-content SSE events, per-event granularity (`stream.go:39-48`); samples pooled across requests |
| `E2E Latency` (`e2e_ms`) | t0 → stream fully consumed (EOF/[DONE]) |
| `TpsPr/TpmPr` (Excel: `tokens/s P50…`, `TPM P50…`) | per-request `OutputTokens / genWindowSeconds` (metrics.go:104) → percentile distribution (i.e., mean-of-rates, not aggregate) |
| `TPS/TPM/QPS/QPM` (`tps,qps,…`) | **aggregate**: tokens/requests of successes *completed within* the throughput window ÷ window seconds (metrics.go:154-161) |
| `I/O Ratio`, `IOR` | per-request output/input token ratio percentiles + system-level totals (metrics.go:120, 165) |
| `Cache Hit Rate` (`cache_hit_ratio_pct`) | per-request `cached_input_tokens / input_tokens * 100` (only when provider reports cache fields) + system totals `TotalInputTokens/TotalCachedTokens` |
| `Goodput` (`goodput_pct`) | % of requests (failures count as not-good) satisfying all configured SLOs `TTFT/TPOT/E2E` (`metrics.go:263-287`) |
| success/failed + `error_counts` | success rate and 15-class error taxonomy (`types.go:13-38`) |

Key correctness details:

- **Token counts come from the API usage field**, not a local tokenizer: OpenAI requests set `stream_options.include_usage: true` (`api.go:298`); parsing handles OpenAI/Anthropic/Gemini/Responses usage dialects and normalizes Anthropic cache-read/creation into the input count and Gemini `thoughtsTokenCount` into completion tokens (`sse.go:184-246`). Only when usage is absent does it fall back to a char heuristic (`ascii/4 + other/1.5`, `tokstats.go:52-68`) and flags the sample `OutputEstimated`/`EstimatedOutputs`. **`internal/tokenizers` is NOT used by this tool** (grep confirms zero references under `cmd/performance`).
- **TPOT/TPS/ITL samples are validity-filtered**: `genWindow ≥ 2ms AND ≥ 5% of E2E AND implied rate ≤ 4096 tok/s` ("physical ceiling") (`tokstats.go:16-45`), dropping buffered/drained pseudo-streaming samples; dropped count reported as `GenSpeedExcluded`.
- **Throughput window** = `min(nominal duration, actual elapsed)`: requests finishing in the post-deadline drain period count toward latency percentiles but NOT toward QPS/TPS (`metrics.go:38-46, 87-90`). A warning is printed when Avg E2E ≥ 20% of window (systematic underestimate, `report.go:174-183`).
- **Latency percentiles are success-only** (`metrics.go:82-83`); failure latencies kept separately in `FailedDetails`.
- **Percentile method**: exact full sort + nearest-rank `idx = ceil(n*p) − 1` (`metrics.go:212-215`). Levels: **P50/P95/P99/P99.5/P99.9 + Avg + N**. No P90, no min/max, no stddev, no median-other-than-P50 anywhere (verified by grep). Optional log-scale 10-bucket histograms for E2E/TTFT (`metrics.go:295-336`).

## 5. Client-side overhead, timeouts, errors

- No client overhead is subtracted from measurements, but the transport is tuned to minimize it: `MaxIdleConnsPerHost = maxConcurrency`, `MaxIdleConns = 2×`, HTTP/2, 60s idle timeout (`api.go:36-48`), pool rebuilt once per run sized to max level (`runner.go:31`), plus per-model warmup to pre-build connections (`runner.go:128-145`).
- **No retries** — one attempt per request. Timeouts: 30 min streaming (covers long thinking), 3 min image, 2 min preflight (`api.go:124-134`, `runner.go:25`).
- Errors classified into 15 types (`types.go:13-30`): `timeout, net_timeout, canceled, dns_error, conn_refused, conn_reset, tls_error, connect, rate_limited, server_error, http_error, upstream_error` (in-stream error event after HTTP 200), `stream_broken` (mid-stream read error), `stream_truncated` (clean EOF with no terminal marker — silent truncation), `no_content`. Each failure logged to a JSONL errlog with request/response snippets and extracted request IDs (`api.go:166-206`, `internal/errlog`). Aborted in-flight failures at level cancel are discarded to avoid polluting results (`runner.go:177-180`).

## 6. Warmup / cooldown / run shape

**Duration-based only**; no request-count mode. Preflight: one full streaming request per model, any failure aborts the entire run (`runner.go:97-115`). Warmup: default on, 10s, per-model immediately before its first level at first-level concurrency, results discarded (`runner.go:136-145`). Cooldown 5s between levels (default). Optional error-rate early stop per level (`failed/total > max_error_rate` after `min_samples`, defaults 0.5/20, `runner.go:157-162`) with optional skip of higher levels for that model.

## 7. Statistical treatment

Exact-sort nearest-rank percentiles (above); arithmetic mean; N always reported; `[WARN] low sample count (N<20): P95/P99 may be inaccurate` (`report.go:114-116`). No stddev/min/max.

## 8. Cluster variant (`cmd/performance/cluster/`)

One binary, two roles: `agent` (HTTP/JSON daemon, protocol in `cluster/internal/proto/proto.go`: ping/session/preflight/task start/progress/cancel/result, optional `X-Cluster-Token` auth) and `run` (coordinator, `cluster/main.go`).

- **Split**: global concurrency split as `base=n/n, first r agents +1` (`coord/split.go`); open-loop global RPS divided equally per agent (`coord/run.go:151-154`). Ramp computed once globally by the coordinator and pushed down (`run.go:155`).
- **Agents run the identical `runner.RunLevel`** with local early-stop disabled (`run.go:301-307`); coordinator polls each agent's counters every 1s, applies `ShouldStopEarly` on **globally aggregated** error rate, broadcasts cancel (`coord/progress.go:128-140`). Agents have a 60s orphan watchdog (`agentd/task.go:18,78-95`).
- **Merge is exact, not approximated**: agents return **raw `RequestMetrics` samples**; coordinator rebases each agent's timestamps onto its own t0 via relative offsets (no NTP needed — `coord/merge.go:10-21`), takes the **union of all samples** sorted by timestamp, `Elapsed = max(parts)`, then runs the **same `metrics.AggregateMetrics`** as single-node (`run.go:208-213`). Percentiles are therefore computed on the pooled sample set — numerically identical in method to single-node, with the caveat that throughput windowing uses coordinator's t0.

## 9. API compatibility

Six providers (`api.go:210-217`): `openai` (`/v1/chat/completions`, SSE + `include_usage`), `anthropic` (`/v1/messages`), `gemini` (`:streamGenerateContent?alt=sse`), `openai-response` (`/v1/responses`), `openai-image` (`/v1/images/generations`, sync, E2E/QPS only), and internal `__baseline__` (static GET `/api/user-agreement` to measure gateway overhead, not exposed as a normal choice). Bearer auth with per-request random key from per-model token groups (`types.go:134-140`). No non-chat completions, embeddings, or audio endpoints.

## Quick comparison hooks vs evalscope

- Both open- and closed-loop; open-loop is Poisson with an in-flight cap (≈ AIPerf semantics), whereas evalscope typically offers fixed-RPS or parallel-workers.
- Percentiles: exact sort, nearest-rank; P50/95/99/99.5/99.9 — no stddev/min/max/P90 (evalscope reports avg/min/max/stddev too).
- TTFT includes connect+queue time from client t0; reasoning tokens count as first content.
- Dual throughput: per-request rate percentiles (mean-of-rates) AND windowed aggregate tokens/s (with a documented completion-window bias and a printed warning at ≥20% Avg-E2E/window).
- TPOT/TPS/ITL exclude buffered pseudo-streaming samples via the 2ms/5%/4096-tok-s filter — a correctness guard evalscope lacks.
- Token counts trust the API usage field; local tokenizer package exists in the repo but is unused here; fallback is a char heuristic, flagged.
- Cluster merge pools raw samples (exact), not percentile merging.

resume_hint: Continue with Agent(resume="agent-0", prompt="..."). Use agent_id only; do not set subagent_type. The subagent retains its prior context; redo any unfinished tool call if its result was lost.

</details>

<details><summary>Thinking</summary>

I have a thorough picture of the local tool. Now I need evalscope's metrics documentation. Let me fetch the parameters.md and examples.md (which usually shows the metrics output table with definitions), and maybe the vs_vllm_bench.md for their methodology. Also their speed_benchmark.md.

</details>

本地工具的细节拿到了。再抓 evalscope 的指标定义文档做对比：

#### Tool Call: FetchURL (`https://raw.githubusercontent.com/modelscope/evalscope/main/…`)
<!-- call_id: FetchURL_4_31bbe201 -->
```json
{
  "url": "https://raw.githubusercontent.com/modelscope/evalscope/main/docs/en/user_guides/stress_test/examples.md"
}
```

#### Tool Call: FetchURL (`https://raw.githubusercontent.com/modelscope/evalscope/main/…`)
<!-- call_id: FetchURL_5_fe84dcc2 -->
```json
{
  "url": "https://raw.githubusercontent.com/modelscope/evalscope/main/docs/en/user_guides/stress_test/parameters.md"
}
```

<details><summary>Tool Result: FetchURL (`https://raw.githubusercontent.com/modelscope/evalscope/main/…`)</summary>

<!-- call_id: FetchURL_5_fe84dcc2 -->
The returned content is the full response body, returned verbatim. If you use it in your answer, cite this page as a markdown link, e.g. [title](url).

# Parameter

Execute `evalscope perf --help` to get a full parameter description.

## Basic Settings

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `--model` | `str` | Name or path of the test model | - |
| `--url` | `str` | API address, supporting `/chat/completions`, `/completions`, and `/responses` endpoints | - |
| `--name` | `str` | Name for wandb/swanlab database result and result database | `{model_name}_{current_time}` |
| `--api` | `str` | Service API type<br>• `openai`: OpenAI-compatible Chat Completions API (requires `--url`)<br>• `openai_responses`: OpenAI official Responses API<br>• `openai_embedding`: OpenAI-compatible Embedding API<br>• `openai_rerank`: OpenAI/Cohere-compatible Rerank API<br>• `local`: Start local transformers inference<br>• `local_vllm`: Start local vLLM inference service<br>• Custom: See [Custom API Guide](./custom.md#custom-api-requests) | - |
| `--port` | `int` | Port for local inference service<br>Only applicable to `local` and `local_vllm` | `8877` |
| `--attn-implementation` | `str` | Attention implementation method<br>Only effective when `api=local` | `None`<br>(Optional: `flash_attention_2`, `eager`, `sdpa`) |
| `--api-key` | `str` | API key | `None` |
| `--debug` | `bool` | Whether to output debug information | `False` |
| `--scenario` | `str` | External workload scenario. `agentx` runs AIPerf AgentX MVP; also accepts AgentX JSON. See [AgentX MVP](./agentx.md). | `None` |

## Network Configuration

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `--total-timeout` | `int` | Total timeout for each request (seconds) | `21600` (6 hours) |
| `--connect-timeout` | `int` | Network connection timeout (seconds) | `None` |
| `--read-timeout` | `int` | Network read timeout (seconds) | `None` |
| `--headers` | `str` | Additional HTTP headers<br>Format: `key1=value1 key2=value2`<br>Will be used for each query | - |
| `--no-test-connection` | `bool` | Do not send connection test, start stress test directly | `False` |

## Request Control

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `--parallel` | `list[int]` | Number of concurrent requests<br>Can input multiple values separated by spaces | `1` |
| `--number` | `list[int]` | Total number of requests to be sent<br>Can input multiple values (must correspond one-to-one with `parallel`) | `1000` |
| `--rate` | `float` | Request scheduling rate (requests/second)<br>• `-1`: No rate pacing; in the default closed-loop mode, requests are scheduled as fast as possible, but the number of in-flight HTTP requests is still capped by `--parallel`, so requests are not all sent to the server at once<br>• `> 0`: Requests are scheduled following a Poisson arrival model — the inter-arrival interval follows an exponential distribution with mean `1/rate`, resulting in an **average** of `rate` scheduled requests per second | `-1` |
| `--log-every-n-query` | `int` | Log every N queries | `100` |
| `--stream` | `bool` | Whether to use SSE stream output<br>Must be enabled to measure TTFT (Time to First Token) metric | `True` |
| `--sleep-interval` | `int` | Sleep time between each performance test (seconds)<br>Helps avoid overloading the server | `5` |
| `--open-loop` | `bool` | Enable open-loop mode: dispatch requests following a Poisson arrival schedule without semaphore backpressure.<br>Requests are fired at the rate set by `--rate` regardless of whether the server has finished processing previous requests.<br>• `--rate` becomes the sweep variable (accepts multiple values), replacing `--parallel` to drive multi-run iterations<br>• `--number` must have the same length as `--rate`; each pair `(rate, number)` corresponds to one independent run<br>• `--parallel` is ignored in this mode (internally set to -1 / INF)<br>See [Usage Example](./examples.md#open-loop-mode) | `False` |
| `--warmup-num` | `float` | Number or ratio of warmup requests:<br>• `0`: disabled (default)<br>• `>= 1`: absolute count, e.g. `--warmup-num 10` sends 10 warmup requests<br>• `0 < value < 1`: ratio mode, e.g. `--warmup-num 0.1` = 10% of `--number`<br>Warmup requests are sent with the same concurrency/rate as the benchmark but **excluded from performance metrics**<br>Useful for eliminating cold-start effects (KV-cache filling, JIT compilation, etc.)<br>In closed-loop mode, set it to `--parallel` or more, otherwise the first few requests inflate `p99`<br>See [Usage Example](./examples.md#warmup) | `0` |
| `--duration` | `float` | Wall-clock budget for one benchmark run (seconds)<br>Soft-exit semantics: once the deadline elapses **no new requests are dispatched**, but **already in-flight requests are allowed to finish** before exit<br>In multi-turn mode "in-flight" means **already-claimed traces run every remaining turn** (trace-level soft exit, aligned with upstream trie)<br>When combined with `--number`, **whichever cap is hit first** ends the run | `None` |

```{tip}
**Closed-loop (default)** vs **Open-loop** (`--open-loop`) — parameter behaviour comparison:

| | Closed-loop (default) | Open-loop (`--open-loop`) |
|---|---|---|
| **`--rate`** | Controls request scheduling rate (`-1` = no pacing, but still bounded by the `--parallel` concurrency cap; `R` = Poisson-arrival mean) | Controls dispatch rate; **must be > 0**; accepts multiple values (e.g. `5 10 20`), each driving one independent run |
| **`--number`** | Total requests per run; must match `--parallel` in length | Total requests per run; must match `--rate` in **length** |
| **`--parallel`** | Max in-flight requests; each worker waits for a response before sending the next (**backpressure**) | **Ignored**; concurrency is unbounded (INF); requests are fired on schedule without waiting for responses |
| **Use case** | Measure latency and throughput under controlled concurrency | Simulate realistic traffic (arrivals independent of service time); sweep throughput-latency curve across multiple rates |
```

## SLA Settings

| Parameter | Type | Description | Default |
|------|------|------|--------|
| `--sla-auto-tune` | `bool` | Whether to enable SLA auto-tuning mode | `False` |
| `--sla-variable` | `str` | Variable for auto-tuning<br>Options: `parallel` (concurrency), `rate` (request rate) | `parallel` |
| `--sla-params` | `str` | SLA constraint conditions<br>JSON string<br>Supported metrics: `avg_latency`, `p99_latency`, `avg_ttft`, `p99_ttft`, `avg_tpot`, `p99_tpot`, `rps`, `tps`<br>Supported operators: `<=`, `<`, `min` (for latency metrics); `>=`, `>`, `max` (for throughput metrics)<br>Example: `'[{"p99_latency": "<=2"}]'` | `None` |
| `--sla-upper-bound` | `int` | Upper bound of the tuned SLA variable search range | `65536` |
| `--sla-lower-bound` | `int` | Lower bound of the tuned SLA variable search range | `1` |
| `--sla-fixed-parallel` | `int` | Fixed parallel workers used when `--sla-variable=rate`; defaults to `--sla-upper-bound` for backward compatibility | `None` |
| `--sla-num-runs` | `int` | Number of runs per concurrency level (average taken) | `3` |
| `--sla-number-multiplier` | `float` | Multiplier of total requests relative to the tuned variable (concurrency or rate), i.e. `number = round(variable × N)`; defaults to `2` when not set | `None` |

```{seealso}
For details on using the SLA auto-tuning feature, see the [Auto-tuning Guide](./sla_auto_tune.md).
```

## Dataset Configuration

| Parameter | Type | Description | Default |
|-----------|------|-------------|--------|
| `--dataset` | `str` | Dataset mode, see [Dataset Modes](#dataset-modes) below | - |
| `--dataset-path` | `str` | Dataset file or directory path<br>Points to a file: read directly; points to a directory: looks for the corresponding data file inside (for offline use with pre-downloaded dataset cache) | - |
| `--data-source` | `str` | Data source for dataset loading: `modelscope`, `huggingface`, or `local`<br>Defaults to `modelscope` when not specified; automatically treated as `local` when `--dataset-path` is a local directory | `modelscope` |
| `--dataset-args` | `str` | Per-dataset arguments (JSON string), validated against the schema of the selected `--dataset` (unknown keys raise an error) | - |

The keys carried by `--dataset-args` are documented in the sections where they apply:

| Key | Purpose | Section |
|-----|---------|---------|
| `target_input_len` / `input_len_mode` | Truncate real data to a fixed input length | [Length Control](#length-control) |
| `prefix_file` / `prefix_role` | Long-context prefix injection for very long fixed-length inputs | [Long-context Prefix Injection](#long-context-prefix-injection) |
| `speed` / `model_override` / `model_mapping` / `match_output_length` | Replay behaviour for production traffic | [Production Traffic Replay](#production-traffic-replay) |
| Token-length arguments | Multi-turn datasets | [Multi-turn Conversation](#multi-turn-conversation) |

```{note}
`--multi-turn-args` is deprecated; use `--dataset-args` instead (the key names are unchanged). The old flag still works and is automatically merged into `--dataset-args` (on a key conflict, `--dataset-args` takes precedence).
```

### Dataset Modes

**Text / Chat**

| Mode | Description | Supports dataset-path |
|------|-------------|----------------------|
| `openqa` | Automatically downloads [OpenQA](https://www.modelscope.cn/datasets/AI-ModelScope/HC3-Chinese/summary) from ModelScope<br>Prompts are relatively short (usually <100 tokens)<br>Uses `question` field from jsonl file when `dataset_path` is specified | ✓ |
| `longalpaca` | Automatically downloads [LongAlpaca-12k](https://www.modelscope.cn/datasets/AI-ModelScope/LongAlpaca-12k/dataPeview) from ModelScope<br>Prompts are much longer (generally >6000 tokens)<br>Uses `instruction` field from jsonl file when `dataset_path` is specified | ✓ |
| `line_by_line` | Each line in txt file is used as a separate prompt<br>**Requires `dataset_path`** | ✓ (Required) |
| `random` | Randomly generates prompts based on `prefix-length`, `max-prompt-length`, and `min-prompt-length`<br>**Requires `tokenizer-path`**<br>[Usage example](./examples.md#random-dataset) | ✗ |
| `custom` | Custom dataset parser<br>See [Custom Dataset Guide](custom.md#custom-dataset) | ✓ |

**Multimodal**

| Mode | Description | Supports dataset-path |
|------|-------------|----------------------|
| `flickr8k` | Automatically downloads [Flick8k](https://www.modelscope.cn/datasets/clip-benchmark/wds_flickr8k/dataPeview) from ModelScope<br>Builds image-text inputs; large dataset suitable for evaluating multimodal models<br>Supports `--dataset-path` pointing to a local dataset directory (offline) | ✓ (directory) |
| `kontext_bench` | Automatically downloads [Kontext-Bench](https://modelscope.cn/datasets/black-forest-labs/kontext-bench/dataPeview) from ModelScope<br>Builds image-text inputs; approximately 1,000 samples, suitable for quick evaluation of multimodal models<br>Supports `--dataset-path` pointing to a local dataset directory (offline) | ✓ (directory) |
| `random_vl` | Randomly generates both image and text inputs<br>Based on `random`, with additional image-related parameters<br>[Usage example](./examples.md#random-multimodal-dataset) | ✗ |

**Embedding**

| Mode | Description | Supports dataset-path |
|------|-------------|----------------------|
| `embedding` | Load text data from file to evaluate Embedding model<br>Supports Line-by-line (TXT) or JSONL format (with `text` field) | ✓ (Required) |
| `random_embedding` | Randomly generate queries based on `max-prompt-length` and `min-prompt-length` to evaluate Embedding model<br>**Must specify `tokenizer-path`** | ✗ |
| `embedding_batch` | Batch send text data to evaluate Embedding model<br>Load data from file<br>Supports `--extra-args '{"batch_size": 8}'` to set batch size | ✓ (Required) |
| `random_embedding_batch` | Batch send randomly generated query data to evaluate Embedding model<br>**Must specify `tokenizer-path`**<br>Supports `--extra-args '{"batch_size": 8}'` to set batch size | ✗ |

**Rerank**

| Mode | Description | Supports dataset-path |
|------|-------------|----------------------|
| `rerank` | Load Query-Document pairs from file to evaluate Rerank model<br>Supports JSONL format (with `query` and `documents` fields) | ✓ (Required) |
| `random_rerank` | Randomly generate query data to evaluate Rerank model<br>**Must specify `tokenizer-path`**<br>Supports `--extra-args '{"num_documents": 10, "document_length_ratio": 5}'` to set number of documents and length ratio | ✗ |

**Multi-turn Conversation**

Must be used with `--multi-turn`; see [Multi-turn Conversation](#multi-turn-conversation) for the parameters and the [Multi-turn Benchmark Guide](./multi_turn.md) for details.

| Mode | Description | Supports dataset-path |
|------|-------------|----------------------|
| `random_multi_turn` | Synthetic multi-turn conversations; each turn randomly generates a token sequence<br>**Requires `--tokenizer-path` and `--max-turns`**<br>[Usage example](./multi_turn.md#random_multi_turn) | ✗ |
| `share_gpt_zh_multi_turn` | Automatically downloads the Chinese [ShareGPT](https://www.modelscope.cn/datasets/swift/sharegpt) dataset (~70k conversations) from ModelScope, preserving full multi-turn conversations<br>[Usage example](./multi_turn.md#share_gpt_multi_turn) | ✓ |
| `share_gpt_en_multi_turn` | Automatically downloads the English [ShareGPT](https://www.modelscope.cn/datasets/swift/sharegpt) dataset (~70k conversations) from ModelScope, preserving full multi-turn conversations | ✓ |
| `custom_multi_turn` | Uses a local JSONL file as a custom multi-turn dataset<br>Each line must be a JSON array of OpenAI message dicts; ideal for benchmarking with your own conversation data<br>**Requires `--dataset-path`**<br>[Usage example](./multi_turn.md#custom_multi_turn) | ✓ (Required) |

**Production Traffic Replay**

Must be used with `--open-loop`; see [Production Traffic Replay](#production-traffic-replay) for the trace file format and replay arguments.

| Mode | Description | Supports dataset-path |
|------|-------------|----------------------|
| `workload_trace` | Replays a recorded production-traffic JSONL verbatim — original timestamps, request bodies, and headers — for benchmarking against real-world load (bursty arrivals, heterogeneous requests, multi-model routing)<br>Each request carries its own `model`, preserving multi-model routing<br>**Requires `--open-loop` and `--dataset-path`**; `--model`/`--number` are optional (the trace carries its own model and count) | ✓ (Required) |

## Input Construction

Controls the content and length of the input sent to the model. Below, `--xxx` entries are command-line flags while bare keys are passed through the `--dataset-args` JSON.

### Length Control

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `--max-prompt-length` | `int` | Maximum input prompt length<br>Prompts exceeding this length will be discarded | `131072` |
| `--min-prompt-length` | `int` | Minimum input prompt length<br>Prompts shorter than this will be discarded | `0` |

To benchmark a **fixed input length** with real data (instead of `random`), use the following `--dataset-args` keys. Supported on `openqa`, `longalpaca`, `line_by_line` (plain-text lines only), and ShareGPT (`share_gpt_zh` / `share_gpt_en`). **Requires `--tokenizer-path`**.

| Key | Description | Default |
|-----|-------------|--------|
| `target_input_len` | Target input length in tokens. When set, every prompt is truncated to this length | disabled |
| `input_len_mode` | What to do with prompts **shorter than** the target: `cap` (keep as-is; that prompt may be shorter than the target); `drop` (discard it, so every emitted prompt is exactly the target). `drop` cannot be combined with `prefix_file` (see [Long-context Prefix Injection](#long-context-prefix-injection)) | `cap` |

```bash
# Truncate every input to 2048 tokens
evalscope perf \
  --model qwen2.5 --url http://127.0.0.1:8000/v1/completions \
  --dataset share_gpt_zh --tokenizer-path /path/to/tokenizer \
  --dataset-args '{"target_input_len": 2048}'
```

Lengths are counted as bare content tokens, without chat-template overhead. Multi-turn datasets like ShareGPT fit/filter only the **last user turn** by default (same as single-turn); the whole-conversation budget (sum of all message contents, over-long dropped, short padded by the prefix) applies only when a `prefix_file` is configured (see [Long-context Prefix Injection](#long-context-prefix-injection)). JSON lines in `line_by_line` (messages array / full request body) do not go through length control, so combining them with these keys raises an error.

How it differs from `--max/min-prompt-length`:
- `--max/min-prompt-length` only **filters** and never changes content — samples outside the range are dropped, so you get real samples of varying lengths;
- `target_input_len` **rewrites content** — it truncates every prompt to the given length, ideal for controlled fixed-length benchmarking.

> To get "every prompt exactly N tokens", you must use `target_input_len`; setting `--min-prompt-length` equal to `--max-prompt-length` cannot achieve it (real data almost never has prompts of exactly N tokens, so they get filtered out). The `random` dataset is the exception — it is generated on the fly, so min=max already yields a fixed length and this arg is not needed.

### Long-context Prefix Injection

Real instruction datasets are mostly 4K-8K tokens, so setting `target_input_len` to 128K filters everything out in `drop` mode and leaves varying lengths in `cap` mode. `prefix_file` lets you point at a long text (e.g. a book or document corpus); the framework slices it to exactly `target_input_len − prompt length` tokens and prepends it to each short prompt, so **every request's total input hits the target length** while keeping the low-entropy character of real human language (ideal for measuring prefix-cache hit rates and MTP acceptance rates). Supported on the same datasets as [Length Control](#length-control).

| Key | Description | Default |
|-----|-------------|--------|
| `prefix_file` | Path to the long prefix text file (UTF-8 plain text). **Requires `target_input_len`** and cannot be combined with `input_len_mode="drop"` | disabled |
| `prefix_role` | Injection role for the prefix: `system` (injected as a leading system message, matching real RAG traffic; inference engines usually have dedicated prefix-cache handling for system prompts); `user` (prepended directly to the user message content) | `system` |

```bash
# Align every request to exactly 131072 tokens using a long text prefix injected as the system role
evalscope perf \
  --model qwen2.5 --url http://127.0.0.1:8000/v1/chat/completions \
  --dataset openqa --tokenizer-path /path/to/tokenizer \
  --dataset-args '{"target_input_len": 131072, "prefix_file": "/path/to/long_text.txt", "prefix_role": "system"}'
```

Behavior notes:
- **Budget split**: the prompt is kept as-is (over-length prompts are truncated per `input_len_mode`); the prefix is sliced to exactly `target_input_len − tokens of all message contents`, so the total equals the target. Multi-turn history counts towards the budget (see the length convention in [Length Control](#length-control)).
- **Incompatible with `drop`**: `drop` only keeps prompts that already fill `target_input_len`, leaving a zero prefix budget, so the injection could never take effect — the combination is rejected at config time. Use `cap` plus prefix filling for fixed lengths.
- **Short prefix**: when the prefix file has fewer tokens than the remaining budget, it is repeated (tiled) to cover it and then truncated precisely, with a warning.
- **Fallback**: when `apply_chat_template` is off (e.g. the `/v1/completions` endpoint), a system message cannot be injected, so the prefix falls back to plain-text concatenation with a warning.
- **Join boundary**: with `prefix_role="user"` and in the plain-text fallback the prefix sits directly next to the prompt. The prefix and prompt are counted independently, so when the tokenizer merges or splits characters across the join the measured total can differ from the target by about ±1 token. `prefix_role="system"` (chat-template mode) is separated by message markers and is always exact.
- **Cache friendly**: all requests share the same prefix head (lengths differ slightly per prompt), which naturally suits prefix-cache hit testing.

```{note}
The `prefix_file` in this section injects a **real text** prefix for real datasets, whereas `--prefix-length` injects a **random token** prefix and only applies to the `random` dataset (see [Random Data Generation](#random-data-generation) below). They serve different purposes and should not be confused.
```

### Random Data Generation

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `--prefix-length` | `int` | Length of the random token prefix<br>Only effective for the `random` dataset; all requests share the same prefix, which can be used to induce prefix-cache hits | `0` |
| `--image-width` | `int` | Image width for random VL dataset | `224` |
| `--image-height` | `int` | Image height for random VL dataset | `224` |
| `--image-format` | `str` | Image format for random VL dataset | `RGB` |
| `--image-num` | `int` | Number of images for random VL dataset | `1` |
| `--image-patch-size` | `int` | Patch size for the image<br>Only used for local image token calculation | `28` |

The length of `random` prompts is set by `--min-prompt-length` / `--max-prompt-length` (equal values yield a fixed length); `target_input_len` is not needed.

### Prompt and Template

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `--prompt` | `str` | Specify request prompt<br>String or local file (specify via `@/path/to/file`)<br>Higher priority than `dataset`<br>Example: `@./prompt.txt` | - |
| `--query-template` | `str` | Specify query template<br>JSON string or local file (specify via `@/path/to/file`)<br>Example: `@./query_template.json` | - |
| `--apply-chat-template` | `bool` | Whether to apply chat template | `None` (automatically determined based on URL suffix) |
| `--tokenize-prompt` | `bool` | Tokenize the prompt client-side into a token-ID list and send it directly via `/v1/completions`, bypassing server-side re-tokenization | `False` |

## Multi-turn Conversation

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `--multi-turn` | `bool` | Enable multi-turn conversation benchmark mode; `--number` is the total number of turns to send and `--parallel` is the number of concurrent turn-level requests | `False` |
| `--min-turns` | `int` | Minimum number of user turns per conversation; used by `random_multi_turn` and `swe_smith` | `1` |
| `--max-turns` | `int` | Maximum number of user turns per conversation; required for `random_multi_turn`; optional for ShareGPT / `custom_multi_turn` (truncates long conversations); for `swe_smith` it's the upper bound for per-conversation turn sampling, falling back to `--min-turns` when unset | `None` |
| `--num-workers` | `int` | Worker processes for CPU-bound dataset/request generation.<br>`0` = auto-detect from CPU affinity; `1` = serial (no multiprocessing); `>1` = explicit worker count.<br>Used by `random` (long-prompt parallel generation) and `swe_smith` (live construction). Supersedes the deprecated `multi_turn_args.num_workers`. | `0` |

Token-length arguments for multi-turn datasets such as `swe_smith` are passed via `--dataset-args`.

```{seealso}
Available multi-turn datasets are listed in [Dataset Modes](#dataset-modes); for full usage see the [Multi-turn Benchmark Guide](./multi_turn.md).
```

## Production Traffic Replay

Use `--dataset workload_trace` to replay recorded production traffic verbatim following its **original arrival timing**, closely matching real-world load. **Requires `--open-loop`**; no `--rate` is needed (arrival times come from the trace timestamps). See the full example at [Production Traffic Replay](./examples.md#production-traffic-replay).

The trace file is JSONL, one request record per line:

```json
{"body": {"model": "qwen-plus", "messages": [{"role": "user", "content": "hi"}]}, "timestamp": 1700000000.0}
{"body": {"model": "qwen-max", "messages": [{"role": "user", "content": "hello"}]}, "timestamp": 1700000001.5, "headers": {"X-Tag": "exp"}, "request_id": "req-42", "completion_tokens": 256}
```

| Field | Required | Description |
|-------|----------|-------------|
| `body` | ✓ | Complete request body (dict or JSON string), sent as-is |
| `timestamp` | ✓ | Arrival time (number or ISO-8601 string); only relative deltas matter; must be monotonically non-decreasing |
| `headers` | | Per-request HTTP headers (merged with CLI headers, CLI wins; hop-by-hop headers are stripped) |
| `request_id` | | Propagated to results for correlation with the original request |
| `completion_tokens` | | Used together with `match_output_length` |

Replay behaviour is tuned via `--dataset-args`:

| Key | Type | Description | Default |
|-----|------|-------------|--------|
| `speed` | float | Replay speed multiplier (2.0 = 2× faster, 0.5 = 2× slower) | `1.0` |
| `model_override` | str | Replace the `model` of every request with this value | disabled |
| `model_mapping` | dict | Remap `model` by name (a match takes priority; unmatched keeps the original) | disabled |
| `match_output_length` | bool | Set `max_tokens` from the recorded `completion_tokens` and enable `ignore_eos` (requires vLLM or compatible; `ignore_eos` is auto-skipped for constrained-decoding requests) | `false` |

```{note}
`--model` **does not rewrite** the trace body for `workload_trace` — each request keeps its own `model`, preserving multi-model routing. To rewrite models, use `model_override` / `model_mapping`.
```

## Model and Generation

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `--tokenizer-path` | `str` | Tokenizer weights path<br>Used to calculate the number of tokens in input and output<br>Usually located in the same directory as model weights<br>When benchmarking a chat endpoint, the tokenizer must ship a chat template (see the note below) | `None` |
| `--frequency-penalty` | `float` | frequency_penalty value | - |
| `--logprobs` | `bool` | Whether to return logarithmic probabilities | - |
| `--max-tokens` | `int` or `int int` | Maximum number of tokens that can be generated<br>• A single integer: fixed value, e.g. `--max-tokens 2048`<br>• Two integers: `min max`, sampled uniformly at random per request, e.g. `--max-tokens 512 2048` | `2048` |
| `--min-tokens` | `int` | Minimum number of tokens to generate<br>Note: Not all model services support this parameter<br>For `vLLM>=0.8.1`, you need to additionally set<br>`--extra-args '{"ignore_eos": true}'`<br>In closed-loop mode, setting this equal to `--max-tokens` gives every request the same duration, so they finish together and are re-released together, showing up as a TTFT ramp that repeats every `--parallel` requests; use the `--max-tokens <min> <max>` range form to avoid it, or `--open-loop` (arrivals are decoupled from completions) | - |
| `--n-choices` | `int` | Number of completion choices to generate | - |
| `--seed` | `int` | Random seed | `None` |
| `--stop` | `str` | Tokens that stop the generation | - |
| `--stop-token-ids` | `list[int]` | IDs of tokens that stop the generation | - |
| `--temperature` | `float` | Sampling temperature | `0` |
| `--top-p` | `float` | Top-p sampling | - |
| `--top-k` | `int` | Top-k sampling | - |
| `--extra-args` | `str` | Additional parameters to be passed in the request body<br>JSON string format<br>Example: `'{"ignore_eos": true}'` | - |

### Tokenizer and chat template

When benchmarking a `chat/completions` endpoint, `--apply-chat-template` is on by default and the client applies the chat template before counting tokens, so client-side lengths line up with the `usage.prompt_tokens` reported by the service; the tokenizer given to `--tokenizer-path` must therefore ship a Jinja chat template. DeepSeek-V3.2 / V4 provide `encoding` scripts instead, and base / pretrain checkpoints have no template either — those fail with an error that lists the available options.

Two things to watch out for: without `--tokenizer-path`, `--min-prompt-length` / `--max-prompt-length` filter by characters instead of tokens; and borrowing another model's tokenizer does not fail, but a mismatched vocabulary silently distorts token counts.

## Output

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `--visualizer` | `str` | Visualizer to use<br>Options: `wandb`, `swanlab`, `clearml`<br>If set, metrics will be saved to the specified visualizer | `None` |
| `--enable-progress-tracker` | `bool` | Whether to enable progress tracking, writing hierarchical stress-test progress to `progress.json` in real time, queryable via the service API | `False` |
| `--wandb-api-key` | `str` | wandb API key for logging metrics to wandb<br>**Deprecated**, please use `--visualizer wandb` instead | - |
| `--swanlab-api-key` | `str` | swanlab API key for logging metrics to swanlab<br>**Deprecated**, please use `--visualizer swanlab` instead | - |
| `--outputs-dir` | `str` | Output file path | `./outputs` |
| `--no-timestamp` | `bool` | Exclude timestamp from output directory name | `False` |

## Other Parameters

| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `--db-commit-interval` | `int` | Number of rows buffered before writing results to SQLite database | `1000` |
| `--queue-size-multiplier` | `int` | Maximum size of the request queue<br>Calculated as: `parallel * multiplier` | `5` |
| `--in-flight-task-multiplier` | `int` | Maximum number of in-flight tasks<br>Calculated as: `parallel * multiplier` | `2` |


</details>

<details><summary>Tool Result: FetchURL (`https://raw.githubusercontent.com/modelscope/evalscope/main/…`)</summary>

<!-- call_id: FetchURL_4_31bbe201 -->
The returned content is the full response body, returned verbatim. If you use it in your answer, cite this page as a markdown link, e.g. [title](url).

# Examples

This page walks through copy-pasteable commands in the order of *target → input data → request parameters → load pattern → observing results*. For the full parameter reference see [Parameters](./parameters.md); for multi-turn scenarios see [Multi-turn Conversation Benchmark](./multi_turn.md).

## Local Model Inference

Both local transformers inference and vLLM inference (vllm must be installed first) are supported, and neither needs `--url`. `--model` accepts a ModelScope model name such as `Qwen/Qwen2.5-0.5B-Instruct`, or a direct path to model weights such as `/path/to/model_weights`.

**transformers inference**: pass `--api local`.

```bash
evalscope perf \
  --model 'Qwen/Qwen2.5-0.5B-Instruct' \
  --number 20 \
  --parallel 2 \
  --api local \
  --dataset openqa
```

Optionally add `--attn-implementation`, choosing from `flash_attention_2`, `eager`, or `sdpa`.

**vLLM inference**: pass `--api local_vllm`.

```bash
evalscope perf \
  --model 'Qwen/Qwen2.5-0.5B-Instruct' \
  --number 20 \
  --parallel 2 \
  --api local_vllm \
  --dataset openqa
```

## Input Construction

### Fixed Prompt

Use `--prompt` to send one fixed prompt for every request, with no dataset involved.

```bash
evalscope perf \
  --url 'http://127.0.0.1:8000/v1/chat/completions' \
  --parallel 2 \
  --model 'qwen2.5' \
  --log-every-n-query 10 \
  --number 20 \
  --api openai \
  --temperature 0.9 \
  --max-tokens 1024 \
  --prompt 'Write a science fiction story, please begin your performance'
```

You can also read it from a local file with the `@` prefix:

```bash
evalscope perf \
  --url 'http://127.0.0.1:8000/v1/chat/completions' \
  --parallel 2 \
  --model 'qwen2.5' \
  --log-every-n-query 10 \
  --number 20 \
  --api openai \
  --temperature 0.9 \
  --max-tokens 1024 \
  --prompt @prompt.txt
```

### Random Dataset

Randomly generate prompts based on `prefix-length`, `max-prompt-length`, and `min-prompt-length`. It is necessary to specify `tokenizer-path`. The number of tokens in the generated prompt is uniformly distributed between `prefix_length + min-prompt-length` and `prefix_length + max-prompt-length`. In a single test, all requests have the same prefix portion.

```{note}
Due to the influence of chat_template and tokenization algorithms, there may be some discrepancies in the number of tokens in the generated prompts, and it is not an exact specified token count.
```

```bash
evalscope perf \
  --parallel 20 \
  --model Qwen2.5-0.5B-Instruct \
  --url http://127.0.0.1:8801/v1/chat/completions \
  --api openai \
  --dataset random \
  --min-tokens 128 \
  --max-tokens 128 \
  --prefix-length 64 \
  --min-prompt-length 1024 \
  --max-prompt-length 2048 \
  --number 100 \
  --tokenizer-path Qwen/Qwen2.5-0.5B-Instruct \
  --debug
```

```{note}
To ensure the server receives exactly the configured number of tokens, add `--tokenize-prompt`. This flag tokenizes the prompt into a token-ID list on the client side and sends it directly via the `prompt` field of `/v1/completions`, bypassing server-side re-tokenization.

The server will receive exactly `prefix_length + inner_seq_length` tokens, which falls within `[min-prompt-length, max-prompt-length]`. Compatible with vLLM, SGLang, LMDeploy, and other frameworks that accept token-ID input; not supported for the `random_vl` dataset.
```

### Random Multimodal Dataset

Use the `random_vl` dataset to randomly generate image and text inputs. Based on the `random` dataset, it adds image-related parameters (`image-width`, `image-height`, `image-format`, `image-num`).

```bash
evalscope perf \
  --parallel 20 \
  --model Qwen2.5-VL-3B-Instruct \
  --url http://127.0.0.1:8801/v1/chat/completions \
  --api openai \
  --dataset random_vl \
  --min-tokens 128 \
  --max-tokens 128 \
  --prefix-length 0 \
  --min-prompt-length 100 \
  --max-prompt-length 100 \
  --image-width 512 \
  --image-height 512 \
  --image-format RGB \
  --image-num 1 \
  --number 100 \
  --tokenizer-path Qwen/Qwen2.5-VL-3B-Instruct \
  --debug
```

### Long-context Prefix Injection

To benchmark 128K/256K-scale long contexts with **real text**, the `random` dataset gives you a fixed length but only high-entropy meaningless tokens, which cannot reproduce real characteristics such as prefix-cache hit rates or MTP acceptance rates. Real instruction datasets, on the other hand, are mostly 4K-8K tokens, so setting `target_input_len` to 128K either filters everything out or leaves lengths uneven.

`prefix_file` solves this: point it at a long text corpus (a book, documentation, a code base, etc.) and the framework slices it to exactly `target_input_len − prompt length` tokens and prepends it to each short prompt, so **every request's input hits the target length** while keeping the low-entropy character of real human language. See [Long-context Prefix Injection](./parameters.md#long-context-prefix-injection) for the parameter reference.

**Inject the prefix as the system role** (recommended, matching real RAG traffic):

```bash
evalscope perf \
  --parallel 4 \
  --model Qwen2.5-0.5B-Instruct \
  --url http://127.0.0.1:8801/v1/chat/completions \
  --api openai \
  --dataset openqa \
  --tokenizer-path Qwen/Qwen2.5-0.5B-Instruct \
  --dataset-args '{"target_input_len": 8192, "prefix_file": "long_text.txt", "prefix_role": "system"}' \
  --max-tokens 128 \
  --number 20
```

Each request is built as `[{"role": "system", "content": "<long prefix>"}, {"role": "user", "content": "<original question>"}]`, and the `Input Tokens` reported by the server are identical across requests (`avg` = `p50` = `p99` = `max`), which makes it easy to benchmark a single length point.

**Prepend the prefix to the user message**: set `prefix_role` to `user` and the prefix is prepended directly to the user question, producing a single user message. Useful when you do not want to introduce a system role.

```bash
evalscope perf \
  --parallel 2 \
  --model Qwen2.5-0.5B-Instruct \
  --url http://127.0.0.1:8801/v1/chat/completions \
  --api openai \
  --dataset openqa \
  --tokenizer-path Qwen/Qwen2.5-0.5B-Instruct \
  --dataset-args '{"target_input_len": 131072, "prefix_file": "long_text.txt", "prefix_role": "user"}' \
  --number 10
```

```{note}
**Practical notes**

- **Preparing the prefix corpus**: any UTF-8 plain-text file works. When the corpus has fewer tokens than the budget it is automatically repeated (tiled) with a warning — to avoid repetition entirely, use a corpus with more tokens than `target_input_len`.
- **Stay under the server limit**: `target_input_len` must be smaller than the server's `max_model_len` (adjustable via `--max-model-len` in vLLM), otherwise requests are rejected. Note that the chat template itself also consumes a dozen or so extra tokens.
- **Supported datasets**: `openqa`, `longalpaca`, `line_by_line` (plain-text lines only), and ShareGPT (`share_gpt_zh` / `share_gpt_en`); all require `--tokenizer-path`. ShareGPT budgets against the whole conversation, so a conversation already longer than `target_input_len` is skipped entirely.
- **Not compatible with `drop`**: `prefix_file` and `input_len_mode="drop"` are mutually exclusive (`drop` only keeps prompts that already fill the target, leaving a zero prefix budget) and the combination is rejected at config time; use the default `cap` and let the prefix fill the rest.
- **Prefix-cache effect**: all requests share the same prefix head, so re-running the same configuration shows a clear TTFT drop (the engine hits its prefix cache). Conversely, to measure **cache-free** cold-start performance, use the `random` dataset instead (its `--dataset-offset` mechanism keeps prompts distinct across runs).
- **`/v1/completions` endpoint**: this endpoint does not apply a chat template, so `prefix_role="system"` automatically falls back to plain-text concatenation with a warning; the prefix and prompt then sit directly adjacent and tokens may merge or split across the join, making the measured total differ from the target by about ±1 token (the same applies to `prefix_role="user"`; see "Join boundary" in the [parameter reference](./parameters.md#long-context-prefix-injection)).
```

## Request Configuration

### Generation Parameters and Timeouts

Combine `stop`, `stream`, `temperature`, and read/connect timeouts:

```bash
evalscope perf \
  --url 'http://127.0.0.1:8000/v1/chat/completions' \
  --parallel 2 \
  --model 'qwen2.5' \
  --log-every-n-query 10 \
  --read-timeout 120 \
  --connect-timeout 120 \
  --number 20 \
  --max-prompt-length 128000 \
  --min-prompt-length 128 \
  --api openai \
  --temperature 0.7 \
  --max-tokens 1024 \
  --stop '<|im_end|>' \
  --dataset openqa \
  --stream
```

### Custom Request Body

Use `--query-template` to define the complete request-body JSON, where `%m` and `%p` are replaced by the model name and the prompt:

```bash
evalscope perf \
  --url 'http://127.0.0.1:8000/v1/chat/completions' \
  --parallel 2 \
  --model 'qwen2.5' \
  --log-every-n-query 10 \
  --read-timeout 120 \
  --connect-timeout 120 \
  --number 20 \
  --max-prompt-length 128000 \
  --min-prompt-length 128 \
  --api openai \
  --query-template '{"model": "%m", "messages": [{"role": "user","content": "%p"}], "stream": true, "skip_special_tokens": false, "stop": ["<|im_end|>"], "temperature": 0.7, "max_tokens": 1024}' \
  --dataset openqa
```

For longer templates, write the JSON to a local file and reference it with the `@` prefix:

```{code-block} json
:caption: template.json

{
   "model":"%m",
   "messages":[
      {
         "role":"user",
         "content":"%p"
      }
   ],
   "stream":true,
   "skip_special_tokens":false,
   "stop":[
      "<|im_end|>"
   ],
   "temperature":0.7,
   "max_tokens":1024
}
```

```bash
evalscope perf \
  --url 'http://127.0.0.1:8000/v1/chat/completions' \
  --parallel 2 \
  --model 'qwen2.5' \
  --log-every-n-query 10 \
  --read-timeout 120 \
  --connect-timeout 120 \
  --number 20 \
  --max-prompt-length 128000 \
  --min-prompt-length 128 \
  --api openai \
  --query-template @template.json \
  --dataset openqa
```

## Load Patterns

### Warmup

Send a batch of warmup requests before the formal benchmark to eliminate cold-start effects (e.g. KV-cache filling, JIT compilation, connection pool initialization) and produce more accurate performance metrics.

Warmup requests are sent with the same concurrency and rate as the benchmark but **excluded from performance metrics** (latency, throughput, percentiles, etc.).

In closed-loop mode warmup has a second, equally important job: it absorbs the burst that occurs when the run starts. Without warmup the first `--parallel` requests are all released at the same instant against an idle server, so they queue behind one another's prefill and report a TTFT that never recurs later in the run. Warmup requests take that hit instead, and the dispatcher hands the concurrency slots over to the measured requests **without draining them first** — each warmup completion releases exactly one measured request into a server that is already busy. The first measured request therefore already faces a fully loaded server. That is what decides whether the percentiles can be trusted: without warmup, the inflated TTFT of those first `--parallel` requests enters the same percentile calculation as the normal ones, so whenever `--parallel / --number` exceeds 1% the reported `p99` is decided by that opening batch alone.

That hand-over only works while every concurrency slot is held by a warmup request, so `--warmup-num` should be at least `--parallel` in closed-loop mode; when the value is too small the run logs a warning naming the value to pass. Use a larger one (for example `2 × --parallel`) if the server also has a genuinely cold start to absorb, or if you read the `max` column of the percentile table as well.

**Absolute count mode**: set `--warmup-num` to an integer `>= 1`, the exact number of warmup requests.

```bash
evalscope perf \
  --url 'http://127.0.0.1:8000/v1/chat/completions' \
  --parallel 10 \
  --model 'qwen2.5' \
  --number 100 \
  --warmup-num 10 \
  --api openai \
  --dataset openqa \
  --stream
```

The above command sends 10 warmup requests first, then 100 benchmark requests. Metrics are computed only from the latter 100 requests.

**Ratio mode**: set `--warmup-num` to a float between 0 and 1 to compute the warmup count as a proportion of `--number`. This is especially useful for sweep mode where each run has a different `--number`.

```bash
evalscope perf \
  --url 'http://127.0.0.1:8000/v1/chat/completions' \
  --parallel 10 \
  --model 'qwen2.5' \
  --number 100 \
  --warmup-num 0.1 \
  --api openai \
  --dataset openqa \
  --stream
```

`--warmup-num 0.1` means the warmup count is 10% of `--number`, i.e. `max(1, int(0.1 * 100)) = 10` warmup requests.

```{note}
**Important Notes**

- Warmup requests use the same dataset and request parameters as the benchmark.
- A separate progress bar is displayed during warmup (`Warmup[...]`) alongside the benchmark bar (`Processing[...]`). In closed-loop mode the two overlap briefly, because the trailing warmup responses arrive after the first measured requests have already been dispatched.
- `--duration` is anchored on the moment the first measured request is sent, so warmup does not consume the timed budget.
- In multi-turn mode, `--warmup-num` specifies the number of warmup conversations (consistent with the `--number` semantics); all turns within a warmup conversation are excluded from metrics. The `--parallel` guidance above applies to closed-loop single-turn runs; open-loop paces dispatch by arrival rate and multi-turn counts conversations rather than requests.
```

### Open-loop Mode

In open-loop mode, requests are dispatched immediately following a Poisson arrival schedule (controlled by `--rate`), without waiting for the server to return responses. This models realistic traffic patterns where arrivals are independent of service time. By specifying multiple rate values in a single command, you can automatically sweep the throughput-latency curve.

The following example runs three independent benchmark rounds at 5, 10, and 20 req/s, sending 500, 1000, and 2000 requests respectively, to observe how latency and throughput change under different loads:

```bash
evalscope perf \
  --url 'http://127.0.0.1:8000/v1/chat/completions' \
  --model 'qwen2.5' \
  --api openai \
  --dataset openqa \
  --open-loop \
  --rate 5 10 20 \
  --number 500 1000 2000 \
  --max-tokens 1024 \
  --stream
```

```{note}
**Important Notes**

- All `--rate` values must be **> 0**; `rate=-1` (unlimited) is not supported in open-loop mode.
- `--number` and `--rate` must have the **same length**; each `(rate, number)` pair corresponds to one independent benchmark run.
- `--parallel` is **ignored** in open-loop mode (internally set to INF); no need to specify it.
- Since concurrency is unbounded, a high rate may cause a large number of in-flight requests to accumulate if the server cannot keep up. Set rate limits according to your server's capacity.
- Core difference from closed-loop (default) mode: closed-loop workers wait for a response before sending the next request (backpressure protection); open-loop fires requests on schedule without waiting (closer to real traffic).
```

### Production Traffic Replay

The `workload_trace` dataset replays recorded production traffic verbatim following its **original arrival timing**, closely matching real-world load — bursty arrivals, heterogeneous request shapes, and multi-model routing that synthetic datasets (`random`, `openqa`, etc.) cannot reproduce. It builds on open-loop scheduling, but arrival times come from the trace's `timestamp` field, so **no `--rate` is needed**.

Prepare a JSONL trace file (one request record per line); field reference is in [Parameters · Production Traffic Replay](./parameters.md#production-traffic-replay):

```json
{"body": {"model": "qwen-plus", "messages": [{"role": "user", "content": "hello"}]}, "timestamp": 1700000000.0}
{"body": {"model": "qwen-max", "messages": [{"role": "user", "content": "write a poem"}]}, "timestamp": 1700000001.5, "request_id": "req-42"}
```

**Basic replay**: replay the whole trace verbatim following the original timestamps.

```bash
evalscope perf \
  --dataset workload_trace \
  --dataset-path trace.jsonl \
  --url http://127.0.0.1:8000/v1/chat/completions \
  --open-loop
```

**Speed-up + model mapping**: replay at 2× rate, map `gpt-4` in the trace to a local `qwen-max`, and match the recorded output lengths (requires `ignore_eos` support, e.g. vLLM).

```bash
evalscope perf \
  --dataset workload_trace \
  --dataset-path trace.jsonl \
  --url http://127.0.0.1:8000/v1/chat/completions \
  --open-loop \
  --dataset-args '{"speed": 2.0, "model_mapping": {"gpt-4": "qwen-max"}, "match_output_length": true}'
```

**Replay only the first 500 records**: truncate with `--number`.

```bash
evalscope perf \
  --dataset workload_trace \
  --dataset-path trace.jsonl \
  --url http://127.0.0.1:8000/v1/chat/completions \
  --open-loop \
  --number 500
```

```{note}
**Important Notes**

- **Open-loop only**: `--open-loop` is required, otherwise it raises an error.
- **`--model` is optional and does not rewrite the body**: each request keeps its own `model` (multi-model routing is preserved). To rewrite models, use `model_override` (replace all) or `model_mapping` (remap by name) via `--dataset-args`.
- **`--number` is optional**: omit to replay all records, or pass it to truncate to the first N.
- **Timestamps must be monotonically non-decreasing**: epoch numbers or ISO-8601 strings are accepted; out-of-order records trigger a warning and are sorted by timestamp.
- Use `--name` to set a meaningful output directory name (without `--model`, the directory name defaults to the dataset name).
```

## Embedding and Rerank

### Embedding Models

Use `openai_embedding` API mode and the `random_embedding` dataset. When using the random dataset, you need to specify `tokenizer-path` to generate queries of the specified length.

```bash
evalscope perf \
  --parallel 2 \
  --number 10 \
  --model 'text-embedding-v4' \
  --url 'https://dashscope.aliyuncs.com/compatible-mode/v1/embeddings' \
  --api-key ${DASHSCOPE_API_KEY} \
  --api openai_embedding \
  --dataset random_embedding \
  --min-prompt-length 256 \
  --max-prompt-length 256 \
  --tokenizer-path 'Qwen/Qwen3-Embedding-0.6B'
```

### Rerank Models

Use `openai_rerank` API mode and the `random_rerank` dataset. When using the random dataset, you need to specify `tokenizer-path` to generate queries of the specified length.

You can specify data generation parameters through `extra-args`:

- `num_documents`: number of documents per query
- `document_length_ratio`: document length multiplier relative to query length

```bash
evalscope perf \
  --parallel 2 \
  --number 10 \
  --model 'qwen3-rerank' \
  --url 'https://dashscope.aliyuncs.com/compatible-api/v1/reranks' \
  --api-key ${DASHSCOPE_API_KEY} \
  --api openai_rerank \
  --dataset random_rerank \
  --min-prompt-length 256 \
  --max-prompt-length 256 \
  --tokenizer-path 'Qwen/Qwen3-Embedding-0.6B' \
  --extra-args '{"num_documents": 5, "document_length_ratio": 3}'
```

## Debugging Requests

Use the `--debug` option to output the requests and responses.

**Non-`stream` mode output example**

```text
2024-11-27 11:25:34,161 - evalscope - http_client.py - on_request_start - 116 - DEBUG - Starting request: <TraceRequestStartParams(method='POST', url=URL('http://127.0.0.1:8000/v1/completions'), headers=<CIMultiDict('Content-Type': 'application/json', 'user-agent': 'modelscope_bench', 'Authorization': 'Bearer EMPTY')>)>
2024-11-27 11:25:34,163 - evalscope - http_client.py - on_request_chunk_sent - 128 - DEBUG - Request sent: <method='POST',  url=URL('http://127.0.0.1:8000/v1/completions'), truncated_chunk='{"prompt": "hello", "model": "qwen2.5"}'>
2024-11-27 11:25:38,172 - evalscope - http_client.py - on_response_chunk_received - 140 - DEBUG - Request received: <method='POST',  url=URL('http://127.0.0.1:8000/v1/completions'), truncated_chunk='{"id":"cmpl-a4565eb4fc6b4a5697f38c0adaf9b70b","object":"text_completion","created":1732677934,"model":"qwen2.5","choices":[{"index":0,"text":"，everyone！今天我给您撒个谎哦。 ))\\n\\n今天开心的事。","logprobs":null,"finish_reason":"length","stop_reason":null,"prompt_logprobs":null}],"usage":{"prompt_tokens":1,"total_tokens":17,"completion_tokens":16}}'>
```

**`stream` mode output example**

```text
2024-11-27 20:02:24,760 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: {"model":"Qwen2.5-0.5B-Instruct","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"重要的"},"finish_reason":null}],"usage":null}
2024-11-27 20:02:24,803 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: {"model":"Qwen2.5-0.5B-Instruct","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":""},"finish_reason":null}],"usage":null}
2024-11-27 20:02:24,847 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: {"model":"Qwen2.5-0.5B-Instruct","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"，以便"},"finish_reason":null}],"usage":null}
2024-11-27 20:02:24,890 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: {"model":"Qwen2.5-0.5B-Instruct","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"及时"},"finish_reason":null}],"usage":null}
2024-11-27 20:02:24,933 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: {"model":"Qwen2.5-0.5B-Instruct","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"得到"},"finish_reason":null}],"usage":null}
2024-11-27 20:02:24,976 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: {"model":"Qwen2.5-0.5B-Instruct","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"帮助"},"finish_reason":null}],"usage":null}
2024-11-27 20:02:25,023 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: {"model":"Qwen2.5-0.5B-Instruct","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"和支持"},"finish_reason":null}],"usage":null}
2024-11-27 20:02:25,066 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: {"model":"Qwen2.5-0.5B-Instruct","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":""},"finish_reason":null}],"usage":null}
2024-11-27 20:02:25,109 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: {"model":"Qwen2.5-0.5B-Instruct","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":""},"finish_reason":null}],"usage":null}
2024-11-27 20:02:25,111 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: {"model":"Qwen2.5-0.5B-Instruct","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"。<|im_end|>"},"finish_reason":null}],"usage":null}
2024-11-27 20:02:25,113 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: {"model":"Qwen2.5-0.5B-Instruct","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":50,"completion_tokens":260,"total_tokens":310}}
2024-11-27 20:02:25,113 - evalscope - http_client.py - _handle_stream - 57 - DEBUG - Response recevied: data: [DONE]
```

## Visualizing Results

### WandB

Install wandb:

```bash
pip install wandb
```

Add the following parameters before starting the test:

```bash
--visualizer wandb
--name 'name_of_wandb_log'
```

![wandb sample](https://modelscope.oss-cn-beijing.aliyuncs.com/resource/wandb_sample.png)

### SwanLab

Install SwanLab:

```bash
pip install swanlab
```

Add the following parameters before starting the test:

```bash
# You can use the SWANLAB_PROJ_NAME environment variable to specify the project name
--visualizer swanlab
--name 'name_of_swanlab_log'
```

![swanlab sample](https://sail-moe.oss-cn-hangzhou.aliyuncs.com/yunlin/images/evalscope/swanlab.png)

### ClearML

Install ClearML:

```bash
pip install clearml
```

Initialize the ClearML server:

```bash
clearml-init
```

Add the following parameters before starting the test:

```bash
# You can use the CLEARML_PROJECT_NAME environment variable to specify the project name
--visualizer clearml
--name 'name_of_clearml_task'
```

![clearml sample](https://sail-moe.oss-cn-hangzhou.aliyuncs.com/yunlin/images/evalscope/doc/clearml_vis.jpg)


</details>

<details><summary>Thinking</summary>

Now I have evalscope's parameters. I still need the exact metric definitions evalscope reports (their output table). Let me check the quick_start.md which usually shows the metrics output table with metric definitions (Time, TTFT, TPOT, throughput, etc.). Let me fetch that quickly.

</details>

再看 evalscope 的输出指标表定义（quick_start 里有指标说明）：

#### Tool Call: FetchURL (`https://raw.githubusercontent.com/modelscope/evalscope/main/…`)
<!-- call_id: FetchURL_6_ee0581cf -->
```json
{
  "url": "https://raw.githubusercontent.com/modelscope/evalscope/main/docs/en/user_guides/stress_test/quick_start.md"
}
```

<details><summary>Tool Result: FetchURL (`https://raw.githubusercontent.com/modelscope/evalscope/main/…`)</summary>

<!-- call_id: FetchURL_6_ee0581cf -->
The returned content is the full response body, returned verbatim. If you use it in your answer, cite this page as a markdown link, e.g. [title](url).

# Quick Start
Below is a quick guide for using EvalScope to conduct model inference performance testing. It supports OpenAI API format model services and various dataset formats, making it convenient for users to perform performance evaluations.

## Environment Preparation

EvalScope supports usage in Python environments. Users can install EvalScope via pip or from source. Here are examples of both installation methods:

::::{tab-set}
:::{tab-item} pip installation
```shell
# Install additional dependencies
pip install evalscope[perf] -U
```
:::

:::{tab-item} Source installation
```shell
git clone https://github.com/modelscope/evalscope.git
cd evalscope
pip install -e '.[perf]'
```
:::
::::

## Basic Usage
You can start the model inference performance testing tool using the following two methods (command line/Python script):

The example below demonstrates performance testing of the Qwen2.5-0.5B-Instruct model using the vLLM framework on an A100, with fixed input of 1024 tokens and output of 1024 tokens. Users can modify parameters according to their needs.

::::{tab-set}
:::{tab-item} Command line startup
```bash
evalscope perf \
  --parallel 1 10 50 100 200 \
  --number 10 20 100 200 400 \
  --model Qwen2.5-0.5B-Instruct \
  --url http://127.0.0.1:8801/v1/chat/completions \
  --api openai \
  --dataset random \
  --max-tokens 1024 \
  --min-tokens 1024 \
  --prefix-length 0 \
  --min-prompt-length 1024 \
  --max-prompt-length 1024 \
  --tokenizer-path Qwen/Qwen2.5-0.5B-Instruct \
  --extra-args '{"ignore_eos": true}'
```
:::

:::{tab-item} Python script startup
```python
from evalscope.perf.main import run_perf_benchmark
from evalscope.perf.arguments import Arguments

task_cfg = Arguments(
    parallel=[1, 10, 50, 100, 200],
    number=[10, 20, 100, 200, 400],
    model='Qwen2.5-0.5B-Instruct',
    url='http://127.0.0.1:8801/v1/chat/completions',
    api='openai',
    dataset='random',
    min_tokens=1024,
    max_tokens=1024,
    prefix_length=0,
    min_prompt_length=1024,
    max_prompt_length=1024,
    tokenizer_path='Qwen/Qwen2.5-0.5B-Instruct',
    extra_args={'ignore_eos': True}
)
results = run_perf_benchmark(task_cfg)
```
:::
::::

**Parameter description**:

- `parallel`: Number of concurrent requests, multiple values can be passed, separated by spaces
- `number`: Number of total requests for each concurrency, multiple values can be passed, separated by spaces (corresponding one-to-one with `parallel`)
- `url`: Request URL address
- `model`: Name of the model used
- `api`: API service used, default is `openai`
- `dataset`: Dataset name, here it's `random`, indicating randomly generated dataset, for specific usage instructions [refer to](./examples.md#random-dataset); for more available (multimodal) datasets, please refer to [Dataset Configuration](./parameters.md#dataset-configuration)
- `tokenizer-path`: Model's tokenizer path, used to calculate the number of tokens (necessary for random datasets)
- `extra-args`: Additional parameters in the request, passed as a JSON format string, e.g., `{"ignore_eos": true}` indicates ignoring the end token

```{seealso}
[Complete parameter description](./parameters.md)
```

### Output Results

The output test report summary includes basic information (model, dataset, API type, etc.) and the following four tables:

1. **Performance Overview**: One row per concurrency config, showing RPS, output throughput, success rate, and other scalar metrics
2. **Per-Request Metrics**: Distribution metrics (avg / p50 / p99) for latency, TTFT, TPOT, input/output tokens, etc.
3. **Per-Trace Metrics**: Trace-level metrics for multi-turn conversations (only shown in multi-turn mode)
4. **Workload Throughput**: Global token throughput rates across time windows (Overall / Last 30s / Steady)

![multi_perf](https://sail-moe.oss-cn-hangzhou.aliyuncs.com/yunlin/images/evalscope/stress_test/multi_perf.jpg)

```{note}
- The summary report covers test results across multiple concurrency levels, allowing users to compare model performance under different concurrency settings. No summary report is generated for a single concurrency level.
- This report will be saved in `outputs/<timestamp>/<model>/performance_summary.txt`, which users can view as needed.
- For explanations of the metrics in the table below, please refer to the "Metric Descriptions" section that follows. Results will be saved in `outputs/<timestamp>/<model>/benchmark.log`.
```

Additionally, the test results for each concurrency level are output separately, including metrics such as the number of requests, successful requests, failed requests, average latency, and average latency per token for each concurrency level.

```text
Benchmarking summary:
┌───────────────────────────┬───────────────┐
│ Metric                    │         Value │
├───────────────────────────┼───────────────┤
│ ── General ──             │               │
│ Test Duration (s)         │         38.31 │
│ Concurrency               │           200 │
│ Request Rate (req/s)      │         -1.00 │
│ Total / Success / Failed  │ 400 / 400 / 0 │
│ Req Throughput (req/s)    │         10.44 │
│ ── Latency ──             │               │
│ Avg Latency (s)           │         18.78 │
│ TTFT (ms)                 │        717.63 │
│ TPOT (ms)                 │         17.66 │
│ ITL (ms)                  │         17.64 │
│ ── Tokens ──              │               │
│ Avg Input Tokens          │       1024.00 │
│ Avg Output Tokens         │       1024.00 │
│ Output Throughput (tok/s) │      10692.45 │
│ Total Throughput (tok/s)  │      21384.91 │
└───────────────────────────┴───────────────┘
                                                                                         
Percentile results:
┌────────────────┬─────────┬─────────┬─────────┬─────────┬─────────┬─────────┬─────────┬─────────┬─────────┐
│ Metric         │      1% │      5% │     10% │     25% │     50% │     75% │     90% │     95% │     99% │
├────────────────┼─────────┼─────────┼─────────┼─────────┼─────────┼─────────┼─────────┼─────────┼─────────┤
│ Latency (s)    │   17.25 │   17.46 │   17.74 │   18.14 │   18.71 │   19.23 │   20.28 │   20.62 │   20.84 │
│ TTFT (ms)      │  176.30 │  212.60 │  246.76 │  279.87 │  333.79 │ 1082.24 │ 1812.13 │ 2097.23 │ 2319.52 │
│ ITL (ms)       │    0.00 │    0.01 │    0.01 │   10.62 │   15.67 │   20.37 │   26.97 │   32.50 │   50.11 │
│ TPOT (ms)      │   16.61 │   16.80 │   17.02 │   17.31 │   17.76 │   18.06 │   18.18 │   18.28 │   18.36 │
│ Input tokens   │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │
│ Output tokens  │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │ 1024.00 │
│ Output (tok/s) │   49.15 │   49.75 │   50.55 │   53.33 │   54.79 │   56.48 │   57.73 │   58.73 │   59.45 │
│ Total (tok/s)  │   98.30 │   99.50 │  101.09 │  106.66 │  109.58 │  112.97 │  115.46 │  117.45 │  118.89 │
│ Decode (tok/s) │   54.46 │   54.74 │   55.07 │   55.38 │   56.31 │   57.80 │   58.77 │   59.55 │   60.24 │
└────────────────┴─────────┴─────────┴─────────┴─────────┴─────────┴─────────┴─────────┴─────────┴─────────┘
```

### Metric Descriptions

#### Basic Metrics

| Metric | Explanation | Formula |
|--------|-------------|---------|
| Test Duration (s) | Total time from the start to the end of the test process | Last request end time - First request start time |
| Concurrency | Number of clients sending requests simultaneously | Preset value |
| Request Rate (req/s) | Target request scheduling rate; -1 means no rate pacing, while the default closed-loop mode is still bounded by the `--parallel` concurrency cap | Preset value |
| Total / Success / Failed | Total number of requests sent during the test and their success/failure counts | Successful requests + Failed requests |
| Req Throughput (req/s) | Average number of successful requests processed per second | Successful requests / Test Duration |
| Avg Latency (s) | Average time from sending a request to receiving a complete response | Total latency / Successful requests |
| TTFT (ms) | Average time from sending a request to receiving the first response token | Total first chunk latency / Successful requests |
| TPOT (ms) | Average time required to generate each output token (excluding first token) | Total time per output token / Successful requests |
| ITL (ms) | Average time interval between generating each output token | Total inter-token latency / Successful requests |
| Avg Input Tokens | Average number of input tokens per request | Total input tokens / Successful requests |
| Avg Output Tokens | Average number of output tokens per request | Total output tokens / Successful requests |
| Output Throughput (tok/s) | Average number of output tokens processed per second | Total output tokens / Test Duration |
| Total Throughput (tok/s) | Average number of tokens (input + output) processed per second | (Total input tokens + Total output tokens) / Test Duration |
| Decode toks/s | Number of decoded output tokens per second (excluding first token) | 1000 / TPOT |

#### Multi-Turn Metrics

The following metrics are only shown in multi-turn conversation mode. For details, see [Multi-Turn Stress Testing](./multi_turn.md).

| Metric | Explanation | Formula |
|--------|-------------|---------|
| Turns/Req | Average number of historical dialogue turns per request in multi-turn conversations (1 for single-turn) | Total input turns / Successful requests |
| Cache Hit (%) | Estimated prefix cache hit rate based on the ratio of cached tokens to input tokens (requires prefix caching enabled on the server and `cached_tokens` field returned) | Total cached tokens / Total input tokens × 100% |
| 1st-Turn TTFT (ms) | Average TTFT for the first turn (cold prefill) in multi-turn conversations | Sum of all first-turn TTFTs / Number of conversations |
| Subseq. TTFT (ms) | Average TTFT for subsequent turns (can hit prefix cache) in multi-turn conversations | Sum of all subsequent-turn TTFTs / Number of subsequent-turn requests |

#### Speculative Decoding Metrics

The following metrics are only shown in speculative decoding scenarios.

| Metric | Explanation | Formula |
|--------|-------------|---------|
| Decoded Tok/Iter | Average number of tokens accepted per model forward pass (iteration), reflecting draft model accuracy | (Total output tokens − 1) / (Total chunks − 1) |
| Spec. Accept Rate | Approximate token acceptance rate for speculative decoding; closer to 1 means a more accurate draft model | 1 − 1 / (Decoded Tok/Iter) |

#### Percentile Metrics

> Statistics are collected per individual request. Data is divided into 100 equal parts; the nth percentile means n% of data points fall below this value.

| Metric | Explanation |
|--------|-------------|
| Latency (s) | The time from sending a request to receiving a complete response (in seconds): TTFT + TPOT * Output tokens. |
| TTFT (ms) | The time from sending a request to generating the first token (in milliseconds), assessing the initial packet delay. |
| ITL (ms) | The time interval between generating each output token (in milliseconds), assessing the smoothness of output. |
| TPOT (ms) | The time required to generate each output token (excluding the first token, in milliseconds), assessing decoding speed. |
| Input tokens | The number of tokens input in the request. |
| Output tokens | The number of tokens generated in the response. |
| Output (tok/s) | The number of tokens output per second: Output tokens / Latency. |
| Total (tok/s) | The number of tokens processed per second: (Input tokens + Output tokens) / Latency. |
| Decode (tok/s) | The number of decoded output tokens per second. |

#### Per-Trace and Workload Throughput Metrics

In multi-turn mode, two additional tables are output: **Per-Trace Metrics** (trace-level distribution metrics) and **Workload Throughput** (global token throughput rates across time windows). For details, see [Multi-Turn Stress Testing — Output Metrics](./multi_turn.md#output-metrics).


## Visualizing Test Results

### Web Dashboard

The EvalScope Web Dashboard discovers performance test results under its output root. Start the service after the test completes, then open the **Performance** page:

```bash
pip install 'evalscope[service]' -U
evalscope service --outputs outputs
```

### Using WandB

First, install wandb and obtain the corresponding [API Key](https://wandb.ai/settings):
```bash
pip install wandb
```

To upload the test results to the wandb server and visualize them, add the following parameters when launching the evaluation:
```bash
# ...
--visualizer wandb
--name 'name_of_wandb_log'
```

For example:

![wandb sample](https://modelscope.oss-cn-beijing.aliyuncs.com/resource/wandb_sample.png)


### Using SwanLab

First, install SwanLab and obtain the corresponding [API Key](https://swanlab.cn/space/~/settings):
```bash
pip install swanlab
```

To upload the test results to the swanlab server and visualize them, add the following parameters when launching the evaluation:
```bash
# ...
--visualizer swanlab
--name 'name_of_swanlab_log'
```

For example:

![swanlab sample](https://sail-moe.oss-cn-hangzhou.aliyuncs.com/yunlin/images/evalscope/swanlab.png)

If you prefer to use [SwanLab in local dashboard mode](https://docs.swanlab.cn/guide_cloud/self_host/offline-board.html), install swanlab dashboard first:

```bash
pip install 'swanlab[dashboard]'
```

and set the following parameters instead:
```bash
--swanlab-api-key local
```

Then, use `swanlab watch <log_path>` to launch the local visualization dashboard.

### Using ClearML
Please install ClearML using the following command:
```bash
pip install clearml
```

Initialize the ClearML server:
```bash
clearml-init
```

Add the following parameters before starting the test:
```bash
# You can use the CLEARML_PROJECT_NAME environment variable to specify the project name
--visualizer clearml
--name 'name_of_clearml_task'
```

![clearml sample](https://sail-moe.oss-cn-hangzhou.aliyuncs.com/yunlin/images/evalscope/doc/clearml_vis.jpg)


</details>

<details><summary>Thinking</summary>

Now I have both sides. Let me write the comparison. The user asks:
1. 指标差距 (metrics gap) between their tool and evalscope
2. 谁的测试结果更可信 (whose results are more credible)

Let me organize:

**Local tool (cmd/performance)**:
- Load: closed-loop (default, with fixed think time 300ms) + open-loop Poisson with in-flight cap; duration-based levels; concurrency ladder sweep; ramp-up ~1ms/worker
- Warmup: default on, 10s per model; preflight aborts run
- Metrics: TTFT, TPOT, ITL, E2E, TpsPr/TpmPr (per-request rate percentiles), aggregate TPS/TPM/QPS/QPM, I/O ratio, cache hit rate, goodput (SLO-based), success rate, 15-class error taxonomy
- Percentiles: P50/95/99/99.5/99.9 + avg + N, exact sort nearest-rank
- Token counts: API usage field (stream_options.include_usage), multi-dialect (OpenAI/Anthropic/Gemini/Responses), fallback char heuristic flagged
- Guards: pseudo-streaming/buffered response filter (genWindow ≥2ms, ≥5% E2E, ≤4096 tok/s), throughput window excludes drain period, coordinated omission warning, low-sample warning, Avg E2E ≥ 20% window warning
- Errors: 15-class taxonomy, JSONL errlog with snippets, in-stream error/upstream error detection, silent truncation detection
- Cluster: multi-agent, raw sample pooling merge (exact percentiles), timestamp rebasing without NTP
- Providers: openai, anthropic, gemini, openai-response, openai-image
- Fixed max_tokens=8192, prompt modes: fixed/dynamic/codex

**evalscope**:
- Load: closed-loop (default, rate=-1 no pacing or Poisson paced) + open-loop Poisson unbounded; request-count based (--number) with optional --duration cap
- Warmup: optional (--warmup-num), default OFF (0); docs note warmup needed for trustworthy p99
- Metrics: Req throughput, Avg latency, TTFT, TPOT, ITL, input/output tokens, output/total throughput, decode tok/s, percentiles P1/5/10/25/50/75/90/95/99, per-request output/total/decode tok/s percentiles; multi-turn metrics (turns/req, cache hit %, 1st-turn TTFT, subseq TTFT); speculative decoding metrics (decoded tok/iter, accept rate); workload throughput windows (overall/last 30s/steady)
- Token counts: API usage field; tokenizer-path for client-side counting (dataset generation)
- Datasets: openqa, longalpaca, random (token-precise lengths), random_vl, ShareGPT multi-turn, workload_trace production replay, embedding/rerank
- SLA auto-tune
- Visualization: wandb/swanlab/clearml, web dashboard, SQLite DB
- Local inference: transformers/vLLM

**Metric gaps (what evalscope has that local lacks)**:
1. Percentile coverage: evalscope P1-P99 deciles + P25/P75/P90; local has P50/95/99/99.5/99.9 — local has finer tail (99.5/99.9), evalscope has finer body (10/25/75/90) and P1/P5 low-end. Actually both complementary. Also evalscope shows min/max? Their table shows 1%...99% and avg. The summary mentioned avg/min/max/stddev? Let me check: the explore agent said "evalscope reports avg/min/max/stddev too" — hmm, that was the agent's claim in comparison hooks. The docs I fetched show avg + percentiles 1-99. evalscope's benchmark.log likely includes more. I shouldn't over-claim stddev/min/max without evidence. The docs percentile table has 1%,5%,10%,25%,50%,75%,90%,95%,99%. No min/max/stddev in docs. I could hedge.
2. Decode tok/s (1000/TPOT) as explicit metric
3. Workload throughput windows (Overall/Last 30s/Steady state)
4. Multi-turn conversation metrics (turns/req, 1st-turn vs subseq TTFT)
5. Speculative decoding acceptance metrics
6. Cache hit rate — both have (local: cache_hit_ratio; evalscope: Cache Hit %)
7. Embedding/rerank benchmarking
8. Dataset-driven realistic input distributions (ShareGPT etc.), input length distributions
9. Production traffic replay
10. SLA auto-tune (find max concurrency/rate meeting SLO) — local has goodput SLO % but not auto-tune
11. Visualization/DB persistence (SQLite, wandb, dashboards)
12. Request-count based runs; local is duration-based only

**What local has that evalscope lacks**:
1. Goodput (SLO-compliant request %) — evalscope has SLA auto-tune instead
2. P99.5/P99.9 finer tail percentiles
3. Pseudo-streaming/buffered-response detection filter (genWindow validity checks) — important for gateway testing where responses may be buffered
4. Silent truncation detection (stream_truncated), upstream error detection after HTTP 200
5. 15-class error taxonomy with per-request JSONL errlog including request IDs
6. Cluster/multi-agent distributed load generation with exact pooled merge
7. Multi-protocol: Anthropic, Gemini, Responses, image gen — evalscope is OpenAI-centric (+ custom API plugins)
8. Dual throughput reporting: per-request rate percentiles AND windowed aggregate; evalscope per-request percentiles + aggregate
9. Explicit anti-prefix-cache nonce in dynamic mode
10. Coordinated-omission warning in closed-loop mode
11. Baseline static-endpoint measurement (gateway overhead)

**Credibility comparison**:

Both are credible within their designs; the answer depends on what you measure. Key points:

1. Metric formulas are nearly identical (TTFT, TPOT, ITL, E2E, throughput). Definitions: evalscope TPOT excludes first token ((latency - TTFT)/(output tokens - 1)?). Docs say "TPOT = Total time per output token / Successful requests" and "excluding first token". Local: (TotalLatency − TTFT) / OutputTokens — slight difference in denominator (output tokens vs output tokens − 1). Minor.

2. Where they may disagree and why:
   - Closed-loop think time: local inserts fixed 300ms cooldown per request; this lowers offered load vs evalscope's rate=-1 (no pacing). So QPS/TPS numbers under "same concurrency" will differ — evalscope closed-loop sends next request immediately. This makes local's closed-loop throughput slightly lower/more conservative. This is a design choice, not an error — but comparing raw numbers needs awareness.
   - Local throughput window excludes post-deadline drain; evalscope duration = last end − first start (includes drain). Both defensible; aggregate throughput numbers will differ slightly.
   - Local filters pseudo-streaming samples from TPOT/ITL — evalscope includes them, so behind a buffering gateway evalscope's TPOT/ITL can be wildly wrong (near-zero ITL, since chunks arrive in a burst). For API-gateway testing (which llm-inspector targets, with cache hit, baseline endpoint, multi-provider), local is more credible.
   - Token counting: evalscope with tokenizer-path counts client-side precisely; local trusts usage field with flagged fallback. When server omits usage in stream (common), local falls back to heuristic (flagged) — evalscope... their default also uses usage or counts chunks. Not sure; hedge.
   - Warmup: local default-on 10s warmup + preflight; evalscope default off (their docs explicitly warn p99 is polluted by the opening batch without warmup). So out-of-the-box local results are less polluted; evalscope can be made equally rigorous with --warmup-num >= parallel.
   - Percentile method: local exact sort nearest-rank; evalscope "data divided into 100 equal parts" — presumably numpy percentile. Both fine at N large; local warns when N<20.
   - Duration-based (local) vs count-based (evalscope default): with count-based small --number and large --parallel, a big fraction of the run is ramp; evalscope docs note parallel/number > 1% makes p99 decided by opening batch without warmup. Local's duration window + warmup avoids that.
   - Coordinated omission: both closed-loop modes suffer; local warns; open-loop Poisson both support. Local open-loop has in-flight cap (back-pressure → not truly open when saturated); evalscope open-loop is truly unbounded. For saturation finding, evalscope open-loop is more faithful; local is safer.
   - Client overhead: local Go (compiled, low overhead, tuned HTTP transport), evalscope Python aiohttp — at very high rates Python client can become the bottleneck; evalscope mitigates with queue/in-flight multipliers. For extreme throughput tests, Go client less likely to be the bottleneck.

3. Verdict: For gateway/API-level testing (multi-provider, cache behavior, error taxonomy, tail latency, distributed load), local tool is at least as credible, in some aspects more rigorous (warmup default, pseudo-streaming filter, errlog, exact pooled merge). For model-serving-engine benchmarking (vLLM/SGLang), evalscope is more standard and feature-rich (token-precise input lengths, datasets, replay, multi-turn, speculative decoding, SLA auto-tune) and is the more widely cross-validated baseline. Numbers won't be identical due to think time, window definition, TPOT denominator — not because either is wrong.

Actually, let me double check the TPOT denominator claim: local: (TotalLatency − TTFT) / OutputTokens. evalscope docs: "TPOT: Average time required to generate each output token (excluding first token)" with formula "Total time per output token / Successful requests" — ambiguous, but the ITL percentile and decode = 1000/TPOT. The common convention is (E2E − TTFT)/(output_tokens − 1). I'll mention the minor denominator difference carefully, hedged.

Also evalscope throughput: "Total output tokens / Test Duration" where Test Duration = last end − first start. Local: tokens of successes completed within window / window seconds (window = min(nominal, elapsed), drain excluded). These differ subtly.

Now, should I keep exploring? I think I have enough. One more consideration — the user's question: "谁的测试结果更可信？" — I should give a nuanced answer rather than "mine is better". I'll structure:

1. 两者定位
2. 指标对照表 (both have / only evalscope / only local)
3. 计算口径差异 (why numbers differ even on same server)
4. 可信度评判 by scenario
5. 建议 (optional short)

Keep it in Chinese, reasonably compact but thorough. Use tables sparingly but they fit here. The user's question is comparative analysis — a table for metric comparison is appropriate.

Let me write the final answer. Keep citations to file paths. Be careful with claims about evalscope internals I can't verify — stick to docs.

</details>

两边资料都齐了：你的工具我通读了 `cmd/performance/` 全部核心代码，evalscope 侧以官方文档为准。结论如下。

## 一、指标对照

两者共有的核心指标（公式几乎一致）：TTFT、TPOT、ITL、E2E 延迟、成功率、QPS、输出/总吞吐（tok/s）、per-request 速率分位数、缓存命中率（都依赖服务端返回 `cached_tokens`）。

**evalscope 有而你没有的：**

| 能力 | 说明 |
|---|---|
| 低分位覆盖 | P1/P5/P10/P25/P75/P90 全十分位表；你只报 P50/95/99/99.5/99.9 |
| Decode tok/s | 1000/TPOT 作为独立指标 |
| Workload 吞吐窗口 | Overall / Last 30s / Steady 稳态吞吐 |
| 多轮对话指标 | Turns/Req、首轮 TTFT vs 后续轮 TTFT |
| 投机解码指标 | Decoded Tok/Iter、接受率 |
| Token 级精确输入长度 | `--tokenizer-path` 本地分词，random 数据集可精确控制输入 token 数（你靠 `tokens*4` 字符估算） |
| 真实数据集/流量回放 | ShareGPT、openqa、workload_trace 生产流量按原始时间戳回放 |
| SLA 自动调参 | 自动搜索满足 SLO 的最大并发/速率（你有 Goodput%，但不会自动扫） |
| Embedding/Rerank/本地推理 | 覆盖面更广；持久化（SQLite）、wandb/可视化生态 |

**你有而 evalscope 没有的：**

| 能力 | 说明 |
|---|---|
| 更细的尾部分位 | P99.5/P99.9（evalscope 只到 P99） |
| 伪流式过滤 | `tokstats.go:16-45`：genWindow <2ms 或 >4096 tok/s 的"缓冲后一次性吐"样本被剔除，TPOT/ITL 不被假流式污染——evalscope 无此防护 |
| 静默截断/上游错误检测 | `stream_truncated`（干净 EOF 但无结束标记）、HTTP 200 后流内 error 事件单列 15 类错误；evalscope 错误分类粗得多 |
| 逐请求错误日志 | `internal/errlog` JSONL，带请求/响应片段和 request ID |
| Goodput | 满足全部 SLO（TTFT/TPOT/E2E）的请求占比 |
| 分布式压测 | `cluster/`：多 agent 并发拆分、**原始样本池化合并**（分位数是精确计算而非近似合并）、时间戳重定基（不依赖 NTP） |
| 多协议 | Anthropic / Gemini / Responses / 图像生成；evalscope 以 OpenAI 系为主 |
| 反前缀缓存 nonce | dynamic 模式每请求随机前缀防缓存污染（evalscope 的 `random` 数据集也行，但你的 codex 模式还能反向测缓存命中场景） |
| 基线测量 | `__baseline__` 静态端点测网关开销 |

指标总量上 evalscope 更全，尤其在**模型推理引擎评测**维度（多轮、投机解码、数据集）；你的工具在**网关/API 层观测**维度（错误分类、伪流式、尾部延迟、分布式）反而更深。

## 二、同一台服务器，两者数字也会对不上的原因

这不是谁的错，是口径差异，对比数据前必须知道：

1. **闭环思考时间**：你闭环模式每请求固定 sleep 300ms（`runner.go:19`），evalscope `rate=-1` 时完成即发。同样"并发 50"，你的实际负载更低，QPS/TPS 会更保守。
2. **吞吐窗口**：你只统计窗口内完成的请求、排除 deadline 后的 drain 段（`metrics.go:38-46`）；evalscope 的 Test Duration = 最后请求结束 − 最早请求开始，包含 drain。聚合吞吐会有系统性小差。
3. **TPOT 分母**：你是 `(E2E − TTFT) / OutputTokens`，evalscope 约定"不含首 token"（通常即 `output_tokens − 1`），长输出下差异可忽略，短输出下有几个百分点。
4. **运行形态**：你是时长制（每档 60s）+ 默认 10s warmup + preflight 失败即中止；evalscope 默认请求数制且 **warmup 默认关闭**——其官方文档自己警告：不加 `--warmup-num ≥ parallel` 时，开场一批请求的 TTFT 会污染 p99。开箱即用条件下你的分位数更干净。
5. **压测客户端自身开销**：你是 Go + 调优过的 HTTP 传输池；evalscope 是 Python/aiohttp，极高 RPS 下客户端更容易先成为瓶颈。

## 三、谁更可信？

**分场景，都有可信度，但适用面不同：**

- **测推理引擎本身**（vLLM/SGLang 调优、输入输出长度精确控制、多轮/投机解码/生产回放）→ evalscope 更可信。它的 token 级长度控制、真实数据集、被广泛使用和交叉验证的生态是你没有的；和别人的数据对标也必须用它这类标准工具。
- **测网关/API 服务**（多协议、缓存命中、错误归因、尾部延迟、需要分布式加压）→ 你的工具更可信。理由有三点是你的独有硬优势：
  1. 伪流式样本过滤——经过会缓冲响应的网关时，evalscope 的 ITL/TPOT 会算出接近 0 的假值，你不会；
  2. warmup 默认开启 + 协调遗漏（coordinated omission）告警 + 低样本量告警，默认配置下统计卫生更好；
  3. 15 类错误分类 + 逐请求 errlog + 静默截断检测，失败归因能力远超 evalscope 的 success/failed 计数。
- **open-loop 保真度**这一局 evalscope 胜：它开环无界（真泊松到达），你的开环带 in-flight 上限，打满后会退化成限速闭环——找系统饱和点 evalscope 更真实，防打挂你的更安全。

一句话：**两者核心指标公式一致、方法学上都成立；evalscope 胜在输入构造的精确性和评测生态，你的工具胜在测量卫生和错误可观测性。** 如果两边数字打架，先核对第 2 节的五个口径差异（思考时间、吞吐窗口、warmup、TPOT 分母、伪流式），大概率能对上。

## Turn 2

### User

吸收evalscope的优势，我能做哪些迭代？

### Assistant

<details><summary>Thinking</summary>

The user asks: "吸收evalscope的优势，我能做哪些迭代?" — What iterations can I make to absorb evalscope's advantages?

This is a planning/advisory question. I should propose concrete iterations for their tool (cmd/performance/) based on the gaps identified in the previous comparison. I have good knowledge of their codebase from the explore agent. I could verify a few things (e.g., whether tokenizers package exists and can be reused — yes, internal/tokenizers exists in the repo but unused by cmd/performance).

Should I enter plan mode? The user is asking "what iterations can I do" — they want a recommendation list, not necessarily immediate implementation. This is an advisory answer. I should give a prioritized, concrete iteration roadmap grounded in their codebase. Not write code yet.

Let me think about what to propose, prioritized by value/effort:

**P0 — 输入构造精确化 (biggest credibility gap)**
1. 接入 internal/tokenizers：本地分词精确控制输入长度。The repo already has internal/tokenizers! And configs/tokenizers directory. So wire that into prompts.go dynamic mode: generate prompts to exact token count. Also use tokenizer for fallback output token counting instead of char heuristic (tokstats.go fallback).
2. 输入长度分布：support min/max prompt length uniform sampling (currently single target). Also max_tokens range per request (min max) — currently fixed 8192. And ignore_eos/min_tokens support for engine benchmarking.
3. Dataset support: line_by_line (txt/jsonl), ShareGPT-style JSONL with multi-turn. At minimum line_by_line is trivial. ShareGPT single-turn sampling moderate.

**P1 — 测量与统计**
4. Percentile coverage: add P90/P25/P75 (they have exact sort already, trivial) — or make percentile levels configurable. Keep P99.5/P99.9 advantage.
5. Decode tok/s metric (1000/TPOT) — trivial.
6. Steady-state throughput window (exclude ramp-up/ramp-down; Overall/Last30s/Steady) — moderate; they already have windowing logic.
7. Warmup per-level instead of per-model first-level only? Actually evalscope warmup per run. Their warmup is per-model before first level. Could extend warmup to each level (configurable) — because each concurrency level has cold-start batch effect (their own ramp-up partially addresses). Worth mentioning.

**P2 — 负载模式**
8. True unbounded open-loop option (remove in-flight cap or make it optional) — small change in runner.
9. Request-count based runs (--number) in addition to duration — moderate.
10. Workload trace replay (JSONL with timestamps) — bigger, but very valuable for gateway testing; builds on open-loop scheduler.

**P3 — 评测维度扩展**
11. Multi-turn conversation benchmark — significant work: conversation state, per-turn metrics (1st-turn TTFT vs subseq), prefix cache hit analysis. Their codex mode already pokes at prefix caching.
12. Speculative decoding metrics — only if they test engines with spec decoding; decode from chunk deltas. Maybe skip/low priority.
13. SLA auto-tune: they already have Goodput + SLO config + concurrency ladder; auto-tune = find max level meeting SLO automatically (they have early-stop on error rate; extend to early-stop on SLO violation + report max passing level). Actually that's a natural small iteration: "auto-stop sweep when SLO violated, report last passing level".

**P4 — 生态/报告**
14. SQLite/JSONL raw sample persistence for re-analysis (they have errlog JSONL; add success samples?) — they have structured report JSON. Could add raw samples export enabling re-computation and cross-run comparison.
15. Visualization — probably out of scope; they have Excel/console/TUI. Maybe skip.

Also: tokenize client-side to decouple from usage field — evalscope counts tokens with tokenizer-path; llm-inspector trusts usage. Adding tokenizer-based verification (compare usage vs local count, flag drift) is a nice credibility feature — detects server usage-field bugs. That's a distinctive iteration.

Priority-wise, I'd organize into phases:
- 迭代1（输入保真）: tokenizer 接入（输入精确长度 + 输出token本地校验）、输入长度区间分布、max_tokens 范围/min_tokens/ignore_eos
- 迭代2（负载真实化）: line_by_line/JSONL 数据集、真开环选项、请求数制、trace 回放
- 迭代3（统计增强）: 分位数档位补齐+可配置、decode tok/s、稳态吞吐窗口、每档 warmup
- 迭代4（场景扩展）: 多轮对话、SLA auto-tune（在现有 ladder 上加自动截止与最大达标档位报告）

For each: which files to touch, effort estimate, and what gap it closes. Since they asked "我能做哪些迭代" — a roadmap answer. Keep it structured but not bloated. Include file references from their codebase: prompts.go, config.go, runner/api.go, runner/runner.go, metrics.go, report/*.

I should also note what NOT to copy: evalscope's default no-warmup, unbounded open-loop as default, Python overhead — keep their own advantages. And mention the 300ms think time should maybe become configurable (closed-loop think time config) since it affects comparability with evalscope numbers — actually that's an important comparability iteration: add `think_time` config (default 0?) to match evalscope's closed-loop semantics when benchmarking engines.

Also note: don't absorb blindly — some evalscope designs are weaker (default warmup off).

Effort estimates: rough (small/medium/large).

Also mention: internal/tokenizers exists — check what it provides. The repo has internal/tokenizers and configs/tokenizers. I haven't looked at its API. I should probably quickly check it to ground iteration 1. Let me do a quick grep/read to see what it offers (e.g., CountTokens function). That grounds the recommendation. Let me do a quick look.

Also verify whether config currently supports min/max prompt length distribution and think time — the explore agent said config has prompt.tokens target, fixed max_tokens 8192, cooldownPerRequest fixed 300ms (runner.go:19 const?). It said "sleep cooldownPerRequest (300ms fixed think time, runner.go:19)" — sounds like a const. Making it configurable is a real iteration.

Let me quickly check internal/tokenizers API surface and the runner.go cooldown definition to be concrete. Two quick tool calls.

</details>

先快速确认两个事实（仓库里已有的 tokenizer 能力、思考时间是硬编码还是可配置），好让迭代建议落到实处：

#### Tool Call: Bash (`ls internal/tokenizers/ configs/tokenizers/ 2>/dev/null | he…`)
<!-- call_id: Bash_7_faf83804 -->
```json
{
  "command": "ls internal/tokenizers/ configs/tokenizers/ 2>/dev/null | head -30; grep -n \"^func \" internal/tokenizers/*.go | grep -v _test | head -20"
}
```

#### Tool Call: Grep (`cmd/performance`)
<!-- call_id: Grep_8_bc8d18b4 -->
```json
{
  "pattern": "cooldownPerRequest|CooldownPerRequest|think",
  "path": "cmd/performance",
  "output_mode": "content",
  "-n": true,
  "head_limit": 15
}
```

<details><summary>Tool Result: Bash (`ls internal/tokenizers/ configs/tokenizers/ 2>/dev/null | he…`)</summary>

<!-- call_id: Bash_7_faf83804 -->
configs/tokenizers/:
deepseek-v4
kimi-k3

internal/tokenizers/:
bytelevel.go
hf_bpe.go
hf_normalizer.go
hf_pretokenizer.go
hf.go
pattern.go
testdata
tiktoken.go
tokenizer.go
internal/tokenizers/bytelevel.go:13:func init() {
internal/tokenizers/bytelevel.go:42:func byteLevelEncode(s string) string {
internal/tokenizers/hf_bpe.go:42:func (m *bpe) tokenize(piece string) []string {
internal/tokenizers/hf_bpe.go:71:func (m *bpe) initialSymbols(piece string) []string {
internal/tokenizers/hf_bpe.go:87:func (m *bpe) storeCache(piece string, parts []string) {
internal/tokenizers/hf_bpe.go:99:func (m *bpe) lookup(tokens []string, enc *Encoding) {
internal/tokenizers/hf_bpe.go:134:func (m *bpe) byteFallbackTokens(tok string) ([]int, []string, bool) {
internal/tokenizers/hf_bpe.go:165:func parseMerges(raw json.RawMessage) (map[bpePair]int, error) {
internal/tokenizers/hf_bpe.go:196:func putRank(ranks map[bpePair]int, p bpePair, rank int) {
internal/tokenizers/hf_normalizer.go:16:func buildNormalizer(raw json.RawMessage) (normalizer, error) {
internal/tokenizers/hf_normalizer.go:98:func (s seqNormalizer) normalize(text string) string {
internal/tokenizers/hf_normalizer.go:107:func (n formNormalizer) normalize(s string) string { return n.f.String(s) }
internal/tokenizers/hf_normalizer.go:111:func (lowercaseNormalizer) normalize(s string) string { return strings.ToLower(s) }
internal/tokenizers/hf_normalizer.go:115:func (n stripNormalizer) normalize(s string) string {
internal/tokenizers/hf_normalizer.go:127:func (n prependNormalizer) normalize(s string) string { return n.prefix + s }
internal/tokenizers/hf_normalizer.go:136:func newReplaceNormalizer(p patternSpec, content string) (normalizer, error) {
internal/tokenizers/hf_normalizer.go:150:func (n *replaceNormalizer) normalize(s string) string {
internal/tokenizers/hf_pretokenizer.go:20:func buildPreTokenizer(raw json.RawMessage) (preTokenizer, error) {
internal/tokenizers/hf_pretokenizer.go:136:func (s seqPreTokenizer) apply(pieces []string) ([]string, error) {
internal/tokenizers/hf_pretokenizer.go:153:func newSplitPreTokenizer(p patternSpec, behavior string, invert bool) (preTokenizer, error) {


</details>

<details><summary>Tool Result: Grep (`cmd/performance`)</summary>

<!-- call_id: Grep_8_bc8d18b4 -->
cmd/performance/README.md:297:| thinking / reasoning_effort    | 不传                                                                                                                                      | 不在压测范围                                 |
cmd/performance/internal/runner/runner.go:19:	cooldownPerRequest = 300 * time.Millisecond // 协程内发送每个请求的间隔时间
cmd/performance/internal/runner/runner.go:252:// “发请求 → 等响应 → 等 cooldownPerRequest → 发下一个”，直到 deadline 或取消。
cmd/performance/internal/runner/runner.go:270:				// 徒增 Elapsed（排空期被多算约一个 cooldownPerRequest）
cmd/performance/internal/runner/runner.go:277:				case <-time.After(cooldownPerRequest):

</details>

<details><summary>Thinking</summary>

Confirmed:
- `internal/tokenizers` is a full tokenizer package (HF BPE, tiktoken) with configs/tokenizers for deepseek-v4 and kimi-k3 — ready to wire in.
- `cooldownPerRequest` is a hardcoded const at runner.go:19.

Now let me check the tokenizer.go public API quickly for the CountTokens-like entry point name to reference it correctly.

</details>

#### Tool Call: Grep (`internal/tokenizers/tokenizer.go`)
<!-- call_id: Grep_9_4e9befcc -->
```json
{
  "-n": true,
  "head_limit": 10,
  "output_mode": "content",
  "path": "internal/tokenizers/tokenizer.go",
  "pattern": "^func [A-Z]|^type [A-Z]"
}
```

<details><summary>Tool Result: Grep (`internal/tokenizers/tokenizer.go`)</summary>

<!-- call_id: Grep_9_4e9befcc -->
internal/tokenizers/tokenizer.go:27:type Encoding struct {
internal/tokenizers/tokenizer.go:40:type Tokenizer struct {
internal/tokenizers/tokenizer.go:111:func New(path string) (tk *Tokenizer, err error) {
internal/tokenizers/tokenizer.go:129:func Load(dir string) (*Tokenizer, error) { return New(dir) }

</details>

<details><summary>Thinking</summary>

Let me check the Tokenizer methods (Count/Encode).

</details>

#### Tool Call: Grep (`internal/tokenizers/tokenizer.go`)
<!-- call_id: Grep_10_991b5bc0 -->
```json
{
  "-n": true,
  "head_limit": 10,
  "output_mode": "content",
  "path": "internal/tokenizers/tokenizer.go",
  "pattern": "^func \\(t(k)? \\*Tokenizer\\) [A-Z]"
}
```

<details><summary>Tool Result: Grep (`internal/tokenizers/tokenizer.go`)</summary>

<!-- call_id: Grep_10_991b5bc0 -->
internal/tokenizers/tokenizer.go:50:func (t *Tokenizer) EncodeSingle(text string) (enc *Encoding, err error) {
internal/tokenizers/tokenizer.go:67:func (t *Tokenizer) Count(text string) int {
internal/tokenizers/tokenizer.go:76:func (t *Tokenizer) Name() string {
internal/tokenizers/tokenizer.go:84:func (t *Tokenizer) VocabSize() int {

</details>

<details><summary>Thinking</summary>

The tokenizer package has `New(path)`, `Count(text)`, `EncodeSingle`. And `configs/tokenizers/` already ships deepseek-v4 and kimi-k3 tokenizer dirs. So wiring it into cmd/performance is very feasible.

Now write the roadmap. Organize by priority/theme with concrete file touchpoints, effort, and which evalscope gap each closes. Also add a "不要照抄" section — keep own advantages, and a "对标校准" iteration (think_time configurable, TPOT denominator option) so numbers can be compared against evalscope.

Structure:
1. P0 输入保真（tokenizer 接入等）
2. P1 负载模型（数据集、真开环、请求数制、trace 回放）
3. P2 统计与报告（分位数档位、decode tok/s、稳态窗口、每档 warmup、think time 可配置）
4. P3 场景扩展（多轮、SLA auto-tune）
5. 对标校准清单（让两者数字可比）
6. 不建议抄的

Keep effort estimates 小/中/大. Cite files. Be concrete and decisive with a recommended order. Not too long.

</details>

可以做的迭代不少，我按"投入产出比"排成四期，每条都标注了改哪个文件和大致工作量。有个好消息：你仓库里 `internal/tokenizers` 已经是完整的 BPE/tiktoken 实现（`Count()` 现成），还自带 `configs/tokenizers/` 两份词表——evalscope 最核心的优势（token 级精确输入）你不需要引入任何新依赖就能拿到。

## P0 · 输入保真（补齐最大的可信度差距）

1. **接入本地 tokenizer，精确控制输入长度**（中）
   `prompts.go` 的 dynamic 模式目前用 `tokens*4` 字符估算，改成用 `internal/tokenizers` 边生成边 `Count()`，收敛到目标 token 数 ±1%。config 加 `tokenizer` 字段指向 `configs/tokenizers/kimi-k3` 这类目录。
2. **输入长度从"单点"变"区间分布"**（小）
   config 加 `prompt.tokens_min/tokens_max`，每请求均匀采样——对标 evalscope 的 `min/max-prompt-length`，测出来的延迟分布比固定长度更真实。
3. **输出长度可控 + 范围随机**（小）
   `runner/api.go:137-139` 现在硬编码 `max_tokens=8192`，改成 config 支持固定值或 `[min, max]` 每请求随机；顺带支持 `min_tokens` / `ignore_eos`（extra_args 透传即可）。evalscope 文档特别提醒过：闭环模式下所有请求输出等长会同时完成、同时重发，造成 TTFT 周期性阶梯——范围随机能消掉这个伪影。
4. **用 tokenizer 交叉校验 usage 字段**（小，差异化能力）
   服务端返回的 `completion_tokens` 与本地 `Count()` 对比，偏差超阈值就打标。evalscope 是纯信一边，你可以做到"两边对账"——测网关时能直接抓出服务端 usage 统计 bug，这是超过 evalscope 的点。

## P1 · 负载真实化

5. **数据集支持：先做 `line_by_line`**（小）
   逐行读 txt/jsonl 当 prompt，配置指向文件即可。这是 evalscope 数据集体系里实现成本最低、使用频率最高的一个，先拿下。
6. **思考时间可配置**（小，且是对标前置项）
   `cooldownPerRequest` 是 `runner.go:19` 的硬编码 300ms。改成 config 项（建议默认 0 对齐 evalscope `rate=-1` 语义，保留 300ms 作为"拟人"挡位）。不改这条，你的吞吐数字永远没法和 evalscope 直接比。
7. **真开环选项**（小）
   open 模式现在到 in-flight 上限就阻塞（退化成限速闭环）。加一个 `open_loop_unbounded` 开关去掉信号量背压，用于找真实饱和点；保留有界模式做安全默认。
8. **请求数制运行模式**（中）
   现在只有时长制。支持 `requests_per_level` 与时长二选一（先到先停），对齐 evalscope `--number` 的使用习惯，也方便短回归。
9. **生产流量回放**（大，但值得）
   JSONL trace（`timestamp` + 请求体）按原始时间戳开环重放，支持 speed 倍速。你的 open-loop 调度器（泊松到达那套）已经把"按时间表发请求"的架子搭好了，主要工作是 trace 解析和请求体透传。这是 evalscope 压箱底的能力，对网关侧尤其有价值。

## P2 · 统计与报告增强

10. **分位数档位补齐并改为可配置**（小）
    `metrics.go` 已经是全排序精确计算，加 P10/P25/P75/P90 是零成本；保留你的 P99.5/P99.9 尾部优势。
11. **Decode tok/s 独立指标**（极小）：`1000/TPOT`，报告里加一列。
12. **稳态吞吐窗口**（中）：在现有 windowing 上加 Overall / Steady（掐头去尾各 10%）/ Last 30s 三档，对齐 evalscope 的 Workload Throughput 表。
13. **每档 warmup 选项**（小）：现在 warmup 只在每个 model 首档前做一次。并发档位切换也有冷启动批效应（你的 ramp-up 只缓解了一部分），加 `warmup_per_level` 配置。

## P3 · 场景扩展

14. **多轮对话压测**（大）：会话状态保持 + 首轮 TTFT vs 后续轮 TTFT 分列 + 结合你已有的 cache_hit 指标。你的 codex 模式已经验证了前缀缓存观测链路，多轮是自然延伸，但工作量大，建议排在回放之后。
15. **SLA auto-tune**（中，性价比很高）：你已经有 SLO 配置 + Goodput + 并发阶梯 + 错误率早停，离 evalscope 的 sla-auto-tune 只差一步：**SLO 违约自动升档截止，报告"满足 SLO 的最大并发/RPS 档位"**。改动集中在 `runner.go` 的档位循环和报告汇总。

## 不建议照抄的部分

- evalscope **warmup 默认关闭**、开环默认无界——你"默认开 warmup + 有界开环"是更安全的默认，保留，只把开关做出来。
- 分位数别换成 evalscope 的近似算法，你的精确全排序更好。
- 可视化生态（wandb/SQLite）投入大收益低，你的 Excel/JSON 报告对内部使用够了；真要做，优先做**原始样本 JSONL 导出**（成功样本也落盘），一次写入换来任意事后重算，比接可视化平台实用。

**建议的落地顺序**：1 → 6 → 2/3 → 10/11（一周内可见效的校准与保真项）→ 5 → 8 → 12 → 15（中期）→ 9 → 14（长期）。前两条做完，你的数字就能和 evalscope 在同口径下互相对账；P0+P1 做完，功能上就没有它有你没有的硬缺口了，剩下的是它的生态广度。

需要的话我可以先从 P0 的 tokenizer 接入和 think time 配置化开始动手。
