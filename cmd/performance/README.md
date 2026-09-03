# performance 压测工具

对 OpenAI 兼容生态多协议端点（OpenAI / Anthropic / Gemini / Responses / 图片生成 / baseline）发起并发压测，输出 TTFT/TPOT/TPS/TPM/QPS
等分位数与吞吐指标。完整配置示例见 [configs/config.example.yaml](configs/config.example.yaml)。

## 功能特性

- 按 `model × concurrency` 组合逐档运行，每个模型可关联独立的 provider、token 分组
- 支持的 provider：`openai`、`anthropic`、`gemini`、`openai-response`（Responses API）、`openai-image`（图片生成）；内部还保留一个不对外暴露的 `__baseline__`（原始 TCP/TLS 延迟基线）
- 正式测试前对每个模型做一次连通性预检（preflight），任何模型失败即中止整轮压测
- 可选预热阶段（`warmup`）：紧贴每个模型的首个正式档位执行，避免连接池冷启动开销污染首档指标
- 档位错峰启动（ramp）：按并发数摊开首批请求的发起时间，避免全部协程同时建连把排队延迟计入 TTFT/P99
- 错误率早停（`early_stop`）：某档位失败率超阈值时提前结束该档位，可选跳过该模型剩余的更高并发档位
- 终端 TUI（默认，非 TTY 时自动降级为纯文本控制台）+ 运行结束后的文本汇总报告
- Excel 报告导出（默认文件名 `bench-<时间戳>.xlsx`，7 个 sheet，覆盖时延、生成速度、QPS、I/O 比、缓存命中率、错误分析/明细）
- token 用量统计对齐 evaluation/benchmark 的口径（见文末[参数归一化语义](#参数归一化语义)）：无 usage 上报时按字符数粗估

## 构建与运行

`performance` 没有自己独立的 Makefile，跟 `benchmark`、`evaluation` 共用仓库根目录下的同一份 `Makefile`。

### 编译

在 **仓库根目录**执行：

```bash
make build-performance
```

产物落在 `build/performance/` 目录：

- `build/performance/performance-<GOOS>_<GOARCH>`：可执行文件
- `build/performance/config.yaml`：从 `cmd/performance/configs/config.example.yaml` 复制的配置模板

默认交叉编译目标是 `darwin/amd64`，可通过变量覆盖，例如 `make build-performance GOOS=linux GOARCH=arm64`。

### 运行

```bash
cp cmd/performance/configs/config.example.yaml config.yaml   # 修改 models / token_groups / base_url / concurrency
./build/performance/performance-darwin_amd64 -config config.yaml
```

也可以不编译直接跑（在 `cmd/performance` 目录下）：

```bash
cd cmd/performance
go run . -config config.yaml   # -config 默认值为 config.yaml，可省略
```

## 配置文件

完整示例见 [configs/config.example.yaml](configs/config.example.yaml)。配置文件用 `gopkg.in/yaml.v3` 的 `KnownFields(true)` 解析，写错字段名会直接报错，方便及早发现拼写问题。

### 连接与运行参数

```yaml
base_url: "https://api.openai.com"                    # API 服务地址
duration: 60s                                         # 每个并发档位的测试时长
concurrency: [ 10, 20, 30, 40, 50, 75, 100, 120, 150 ]  # 并发档位列表，逐档运行
```

| 字段          | 必填 | 默认值                            | 说明                                                                    |
|---------------|------|-----------------------------------|-------------------------------------------------------------------------|
| `base_url`    | 否   | `https://api.openai.com`          | API 服务地址                                                            |
| `duration`    | 否   | `60s`                             | 每个并发档位的测试时长（`time.Duration` 格式，如 `60s`、`2m`、`1m30s`） |
| `concurrency` | 否   | `[10,20,30,40,50,75,100,120,150]` | 并发档位列表，逐档运行；每个值必须为正整数                              |

### Prompt 配置

```yaml
prompt:
  mode: "text"     # text | dynamic | codex，三选一
  text: "..."      # mode=text 时使用的固定 prompt
  tokens: 2000     # mode=dynamic 时生成文本的目标近似 token 数
image_prompt: "A single red circle on white background, minimal flat design."
```

| mode      | 行为                                                                       |
|-----------|----------------------------------------------------------------------------|
| `text`    | 每次请求都发送同一段固定文本（`prompt.text`）                              |
| `dynamic` | 每次请求现场拼装约 `prompt.tokens` 个 token 的随机长文本，用于长上下文压测 |
| `codex`   | 使用类 Codex 系统提示词 + 随机简短提问，模拟高相似度请求场景               |

`image_prompt` 仅在模型 provider 为 `openai-image` 时使用，与文本端点的 `prompt` 互不影响。

### 预热、冷却与输出偏好

```yaml
warmup: true          # 正式测试前是否执行预热阶段（并发=首个正式档位的并发数，时长由 warmup_duration 控制）
warmup_duration: 10s  # 预热阶段持续时长
cooldown: 5s          # 每个并发档位之间的冷却等待时间

output: ""       # Excel 输出路径（留空则自动生成时间戳文件名，如 bench-20260618T150405.xlsx）
no_excel: false  # 跳过 Excel 导出，仅打印终端报告
no_tui: false    # 禁用 TUI，使用纯文本控制台输出（stdout 非终端时自动禁用）
```

`warmup`、`cooldown` 用指针类型区分「未配置（取默认值）」与「显式设为 false/0s」，所以显式写 `cooldown: 0s` 就是真的不等待，不会被悄悄改回默认的 5s。

### 错误率早停

```yaml
early_stop:
  enabled: true                 # 总开关，默认 false
  max_error_rate: 0.5           # 档位失败率超过该值判定为不可用，(0,1]，默认 0.5
  min_samples: 20               # 至少凑够这么多请求才评估错误率，避免开局抖动误判，默认 20
  skip_higher_concurrency: true # 判定不可用时是否跳过该模型剩余的更高并发档位，默认 true
```

默认整块关闭，不影响现有配置。开启后，某个并发档位的样本数达到 `min_samples` 且累计失败率超过 `max_error_rate` 时，立即结束该档位（不影响其他模型或档位），并在报表里标记 `StoppedEarly`；
`skip_higher_concurrency` 为 true 时会跳过该模型剩余的更高并发档位，避免在明显顶不住的档位上继续空跑。

### 模型与 Token 分组

```yaml
models:
  - name: "gpt-5.6-sol"
    provider: "openai"
    token_group: "openai-channel"
  - name: "claude-sonnet-5"
    provider: "anthropic"
    token_group: "anthropic-channel"

token_groups:
  openai-channel:
    - "sk-openai-abcdef0123456789"
  anthropic-channel:
    - "sk-anthropic-abcdef0123456789"
    - "sk-anthropic-fedcba9876543210"

# 兼容旧配置：未填写 models[].token_group 的模型使用 default 分组
tokens:
  - "sk-default-abcdef0123456789"
```

| 字段                   | 必填 | 说明                                                                                              |
|------------------------|------|---------------------------------------------------------------------------------------------------|
| `models`               | 是   | 待测模型列表，至少一个；同一模型可重复配置以测试不同渠道分组                                      |
| `models[].name`        | 是   | 模型名称                                                                                          |
| `models[].provider`    | 是   | 协议类型，见上文「功能特性」中的 provider 列表                                                    |
| `models[].token_group` | 否   | 该模型使用的 token 分组名；留空时使用 `tokens` 里的 token，作为 `default` 分组                    |
| `token_groups`         | 否   | 命名 token 分组，每个分组对应一组渠道关联的 Bearer token，同一分组内的多个 token 每次请求随机选取 |
| `tokens`               | 否   | 旧版兜底字段，映射为 `default` 分组                                                               |

**注意**：如果某个模型配置了 `token_group`，但该分组下没有有效 token，启动时会直接报错退出—— **不会**静默回退到 `default` 分组。

## 运行流程

1. **预检（preflight）**：对每个模型发一次完整的流式请求（超时 2 分钟，覆盖思考型模型的长思考阶段），验证渠道配置、Token 有效性和网络连通性；任一模型失败则整轮压测中止，不会进入正式测试。
2. **预热（可选）**：每个模型在自己的首个正式档位前预热，并发数取该模型首档的并发数，时长由 `warmup_duration` 控制，结果丢弃不计入报表。
3. **正式测试**：按 `models × concurrency` 的顺序逐档运行；每档内部按并发数错峰启动 worker（错峰窗口 ≈1ms/worker，上限 5s 且不超过 `duration` 的 1/6），worker 持续发请求直到档位 `duration` 到期或被早停取消；deadline
   前发出、deadline 后才完成的长尾请求仍计入时延分位数，但不计入 QPS/TPS 分母。
4. **冷却**：非最后一档时，档位之间按 `cooldown` 等待后再进入下一档；因早停中止的档位不会执行冷却。
5. 收到 `Ctrl+C`/`SIGTERM` 时优雅中止：返回已完成部分的结果并继续打印报告；再按一次直接终止进程。

## 输出

### 终端

- 是 TTY 且未设置 `no_tui: true` 时默认启动 TUI（`q`/`Ctrl+C`/`Esc` 中止，中止后再按一次立即退出）；否则自动降级为纯文本控制台输出，每 10 秒打印一次当前档位的进度和失败原因分布。
- 压测（或 TUI）结束后，无论哪种模式都会打印一份配置头（base_url/duration/concurrency/warmup/cooldown/early stop/模型列表/prompt 配置）和汇总报告：

```
============================================================
  BENCHMARK RESULTS
============================================================

--------------------------------------------------------------------------------
  Model: gpt-5.6-sol  |  Provider: openai  |  Token Group: openai-channel  |  Concurrency: 50
  Elapsed: 60.12s  |  Window: 60.00s  |  Requests: 812 total, 810 ok, 2 failed (0.2% error)
--------------------------------------------------------------------------------
  Metric            P50          P95          P99          Avg          N
  --------------------------------------------------------------------------
  TTFT              320.5ms      680.2ms      950.1ms      350.8ms      810
  TPOT              18.2ms       25.6ms       32.1ms       19.4ms       810
  E2E Latency       3.20s        4.80s        5.90s        3.45s        810
  --------------------------------------------------------------------------
  TPS:     2650.30 tok/s  |  TPM:  159018.0 tok/min  |  QPS: 13.5000 req/s  |  QPM: 810.00 req/min  |  I/O Ratio: 12.400
  Error types: timeout: 2

============================================================
  SUMMARY TABLE
--------------------------------------------------------------------------------
  Model (Provider)        Token Group       Conc   QPS       TPS       TTFT P50    TTFT P95    I/O Ratio
  --------------------------------------------------------------------------
  gpt-5.6-sol (openai)    openai-channel    50     13.500    2650.3    320.5ms     680.2ms     12.400
============================================================
```

per-level 明细下方可能出现的提示行，含义如下：

| 提示                                            | 触发条件与含义                                                                                                                   |
|-------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------|
| `[WARN] 平均 E2E 时延达吞吐窗口的 N%...`        | 平均端到端时延占 `duration` 比例 ≥20% 时出现；QPS/TPS 只统计窗口内完成的请求，比例越高吞吐被低估越严重，建议加大 `duration` 重测 |
| `[WARN] 本档位因错误率超阈值被提前终止...`      | 该档位触发了 `early_stop`，未跑满设定时长                                                                                        |
| `[NOTE] N 条样本未通过速率有效性校验...`        | 生成窗口过窄（一次性到达）或超出单流物理天花板的样本，被剔除出 TPOT/TPS/TPM 分位数，疑似网关缓冲或压测机读流饥饿                 |
| `[NOTE] N/M 条成功样本的 token 数为文本估算...` | provider 未上报 usage，token 数按输出字符数粗估（`len/4`），速率分位数可信度下降                                                 |
| `[WARN] low sample count (N=n)...`              | 该档位样本数 <20，P95/P99 可能不准                                                                                               |

图片生成端点（`openai-image`）没有 TTFT/TPOT/TPS/TPM，只展示 E2E Latency、QPS、QPM。

### Excel 报告

除非配置 `no_excel: true`，压测结束后会导出一份 `.xlsx`（默认文件名 `bench-<时间戳>.xlsx`，可用 `output` 覆盖），包含 7 个 sheet：

| Sheet               | 内容                                                                                                              |
|---------------------|-------------------------------------------------------------------------------------------------------------------|
| 总览                | 测试日期、接口地址、时长/并发档位、模型与 token 分组概览、错误率早停配置摘要、各项指标口径说明                    |
| TTFT延迟            | 每个 `模型×并发` 组合的 TTFT 与 E2E 延迟 P50/P95/P99/P99.5/P99.9/Avg                                              |
| 生成速度(TPS·token) | per-request tokens/s、TPOT、TPM 分位数，以及 System TPS/TPM；备注列标注样本量不足/剔除数/估算占比                 |
| QPS压测(TPS·req)    | 实际时长、吞吐窗口、QPS/QPM、成功率、成功/失败请求数；备注列包含吞吐低估提示与早停提示                            |
| 输入输出Token比     | per-request I/O Ratio 分位数与 System I/O Ratio；per-request 与 System 缓存命中率、总输入/缓存 token 数           |
| 错误分析            | 每个 `模型×并发` 组合的总请求数、失败数、成功率、是否提前终止，以及按 `types.ErrorTypeOrder` 顺序的各错误类型计数 |
| 错误明细            | 每条失败请求一行：发生时间、错误类型、RequestID、总时延、错误信息，按时间排序                                     |

图片生成端点（`openai-image`）不出现在 TTFT延迟/生成速度/输入输出Token比 三个 sheet（这些指标对它无意义），但会出现在 QPS压测 与 错误分析/错误明细。

## 指标口径

- **TTFT / TPOT / E2E Latency**：仅统计成功请求的时延分位数（P50/P95/P99/P99.5/P99.9/Avg）；TPOT = `(总时延 - TTFT) / 输出 token 数`
- **per-request TPS/TPM**：`输出 token 数 / 生成窗口秒数` 的分位数；生成窗口过窄（一次性到达）或超出单流物理天花板的样本会被剔除，剔除数计入 `GenSpeedExcluded`
- **System TPS/TPM**：吞吐窗口内完成的请求总 token 数 / 窗口时长（`Window`），区别于 per-request 分位数——系统级口径反映整体吞吐，per-request 口径反映单条流的解码速度
- **QPS/QPM**：吞吐窗口内完成的成功请求数 / 窗口时长
- **I/O Ratio**：输出/输入 token 比，per-request 分位数与系统级总量比（`总 output_tokens / 总 input_tokens`）两种口径
- **Cache Hit Rate**：`cached_input_tokens / input_tokens * 100%`，同样有 per-request 分位数与系统级总量比两种口径；仅统计上报了缓存字段的 provider，未上报时显示 `N/A`（区别于「上报了但命中率为 0%」）
- **错误类型**：`timeout`/`net_timeout`/`canceled`/`dns_error`/`conn_refused`/`conn_reset`/`tls_error`/`connect`/`rate_limited`/`server_error`/`http_error`/`upstream_error`/`stream_broken`/
  `stream_truncated`/`no_content`，按此固定顺序展示在进度和报表中

## 注意事项

1. **`gpt-image-2` 被硬编码排除**：`main.go` 里写死了排除名单 `excludedModel = "gpt-image-2"`，模型名（大小写不敏感）里只要包含这个字符串就会在启动时被跳过并打印 `[skip] 已排除模型: ...`
   ，不会计入本次压测；配置里写了这个模型但发现被跳过属预期行为
2. **预检失败会中止整轮压测**：任一模型的预检请求失败，压测直接终止且不产生报表，先检查该模型的 `base_url`/`token_group`/网络连通性
3. **`token_group` 缺 token 时不会回退到 `default`**：只有完全不配置 `token_group` 的模型才使用 `tokens`/`default` 分组
4. **并发数越高，`duration` 建议越长**：平均 E2E 时延接近吞吐窗口时 QPS/TPS 会被系统性低估，报表里的 `[WARN]` 会提示这种情况

## 参数归一化语义

本工具的 SSE 流式解析已下沉到 `internal/llm/sse`（`ParseSSELine`/`SSEIsTerminal`/`SSEHasOutputContent`/`ConsumeSSEUsage`/`ApplySSEEvent` 等纯函数），三个工具共享同一套协议判定与 usage
提取逻辑。本工具在参数映射总表中的位置：

| 统一参数                       | performance 的实现                                                                                                                        | 说明                                         |
|--------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------|
| 输出上限                       | 固定 `max_tokens=8192`（openai）/ `max_tokens=8192`（anthropic）/ `maxOutputTokens=8192`（gemini）/ `max_output_tokens=8192`（responses） | 压测对比需控制输出长度，固定常量而非可配置项 |
| `stream_options.include_usage` | 恒为 true（openai）                                                                                                                       | 与 evaluation 默认、benchmark 显式开启对齐   |
| `temperature` / `top_p`        | 不传                                                                                                                                      | 压测用服务端默认值                           |
| thinking / reasoning_effort    | 不传                                                                                                                                      | 不在压测范围                                 |

**token 统计口径**：与 evaluation/benchmark 一致，采用 usage 上报值；Gemini 的 `thoughtsTokenCount` 计入 `completion_tokens`（思考时间在生成窗口里，token 计入分母保证跨协议可比）。usage 缺失时按收集的文本字符数粗估（
`len/4`）。
