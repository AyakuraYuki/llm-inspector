# performance 压测工具

对 OpenAI 兼容生态多协议端点（OpenAI / Anthropic / Gemini / Responses / 图片生成 / baseline）发起并发压测，输出 TTFT/TPOT/TPS/TPM/QPS
等分位数与吞吐指标。完整配置示例见 [configs/config.example.yaml](configs/config.example.yaml)。

## 功能特性

- 按 `model × concurrency` 组合逐档运行，每个模型可关联独立的 provider、token 分组
- 支持的 provider：`openai`、`anthropic`、`gemini`、`openai-response`（Responses API）、`openai-image`（图片生成）；内部还保留一个不对外暴露的 `__baseline__`（原始 TCP/TLS 延迟基线）
- 负载模式可选 closed-loop（默认，固定并发协程数）或 open-loop（按目标 RPS 泊松到达），后者用于避免 Coordinated Omission，见下文「[负载模式：closed-loop 与 open-loop](#负载模式closed-loop-与-open-loop)」
- 正式测试前对每个模型做一次连通性预检（preflight），任何模型失败即中止整轮压测
- 可选预热阶段（`warmup`）：紧贴每个模型的首个正式档位执行，避免连接池冷启动开销污染首档指标
- 档位错峰启动（ramp）：按并发数摊开首批请求的发起时间，避免全部协程同时建连把排队延迟计入 TTFT/P99
- 错误率早停（`early_stop`）：某档位失败率超阈值时提前结束该档位，可选跳过该模型剩余的更高并发档位
- 可选 SLO 达标率（goodput）：按 TTFT/TPOT/E2E 阈值统计「同时满足全部已配置阈值」的请求占比
- 终端 TUI（默认，非 TTY 时自动降级为纯文本控制台）+ 运行结束后的文本汇总报告，可选 ASCII 延迟分布直方图
- Excel 报告导出（默认文件名 `bench-<时间戳>.xlsx`，8 个 sheet，覆盖时延、生成速度、QPS、I/O 比、缓存命中率、分位数全景、错误分析/明细；开启直方图后追加第 9 个 sheet）
- 全量分位数统计：每个指标都给出 Min/P10/P25/P50/P75/P90/P95/P99/P99.5/P99.9/Max/Avg/StdDev（全排序精确计算，非近似），低分位与标准差用于判断分布形态与抖动幅度
- 可选 JSON/CSV 结构化输出，供 CI 里做基线比对、画趋势图；可选原始样本 JSONL 导出（`sample_output`），支持不重跑压测就换口径重算
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
  mode: "text"           # text | dynamic | codex | dataset，四选一
  text: "..."            # mode=text 时使用的固定 prompt
  tokens: 2000           # mode=dynamic 时生成文本的目标 token 数（单点）
  #tokens_min: 500       # mode=dynamic 时改为在 [tokens_min, tokens_max] 内逐请求均匀采样，覆盖 tokens
  #tokens_max: 4000
  #dataset_path: "prompts.txt"  # mode=dataset 时必填：每个非空行一条 prompt
image_prompt: "A cute fluffy kitten playing with a ball of yarn, soft lighting, adorable, high detail."

#tokenizer:
#  path: "configs/tokenizers/kimi-k3"  # 本地分词器目录，留空不启用
#  usage_drift_pct: 10                 # usage 对拍阈值（%），显式 0 关闭对拍
```

| mode      | 行为                                                                                                             |
|-----------|------------------------------------------------------------------------------------------------------------------|
| `text`    | 每次请求都发送同一段固定文本（`prompt.text`）                                                                    |
| `dynamic` | 每次请求现场拼装目标长度的随机长文本，用于长上下文压测；长度可单点（`tokens`）或区间采样（`tokens_min/max`）     |
| `codex`   | 使用类 Codex 系统提示词 + 随机简短提问，模拟高相似度请求场景                                                     |
| `dataset` | 每次请求从 `dataset_path` 文件随机取一个非空行作为 prompt（对标 evalscope 的 `line_by_line`，仅纯文本行）        |

`image_prompt` 仅在模型 provider 为 `openai-image` 时使用，与文本端点的 `prompt` 互不影响。

#### 输入长度：单点还是区间

`tokens_min`/`tokens_max` 仅 `dynamic` 模式合法，两者须同时配置且 `min <= max`；配置后每个请求的目标 token 数在区间内均匀采样，覆盖 `tokens`。真实流量的输入长度是分布而非单点，固定长度测出的时延分位数会偏「干净」——想看长短输入混跑下的
TTFT 分布形态用区间，想做严格可比的回归基线用单点。

#### tokenizer（本地分词器）

`tokenizer.path` 指向 `configs/tokenizers/<name>` 这类目录（HF `tokenizer.json` 或 tiktoken 格式，纯 Go 加载，见 `internal/tokenizers`）。启用后三件事随之改变：

1. **输入精确控长**：`dynamic` 模式按分词器逐段累加、截断最后一段，把输入收敛到目标 token 数（实测偏差在个位数 token 以内）；不配时按「4 字符 ≈ 1 token」近似，不同模型词表下误差可达 ±30%。这是对标 evalscope
   `--tokenizer-path` 的关键能力
2. **usage 缺失时的估算更准**：provider 未上报 usage 的请求，输出 token 数改用分词器对可见文本计数（仍打 `OutputEstimated` 标记）
3. **usage 对拍**：服务端 `completion_tokens` 与本地对可见输出文本的计数比较，|偏差| 超过 `usage_drift_pct`（默认 10%）的请求打 `UsageDrifted` 标记；报表给出对拍条数、漂移条数与 |偏差| 分位数。大量漂移说明网关/上游的
   usage 统计与实际输出不符——计费和 TPOT 分母都会失真，这是 evalscope 做不到的「两边对账」

**范围限制，务必知道**：

- 分词器必须与被测模型一致，借用别家词表不会报错，但计数会静默失真。仓库目前预置 `deepseek-v4`（HF）与 `kimi-k3`（tiktoken）两份；GPT 系可自行补 `o200k_base`（tiktoken 格式，放目录 + `inspector.json` 即可）；**Claude 与
  Gemini 的词表不公开**，这两家做不到精确控长与 usage 对拍，evalscope 同样做不到
- **思考型请求不对拍**：思考 token 计入各协议的 completion 计数，却不在可见文本里，本地必然偏小。出现过思考内容（`thinking_delta`/`reasoning_content`/Gemini `thought:true`/Responses `reasoning_*`）的请求自动跳过对拍，
  `local_output_tokens` 为 0
- 配置了路径但目录不可加载时，配置加载阶段直接报错，而不是运行时静默退回字符估算——否则「配了分词器」的报告口径其实和没配一样

### 预热、冷却与输出偏好

```yaml
warmup: true          # 正式测试前是否执行预热阶段（并发=即将开始档位的并发数，时长由 warmup_duration 控制）
warmup_duration: 10s  # 预热阶段持续时长
warmup_per_level: false  # true 时每个并发档位前都预热（默认只在每个模型的首档前），仅 warmup 为真时有效
cooldown: 5s          # 每个并发档位之间的冷却等待时间
think_time: 300ms     # closed-loop 下同一 worker 两次请求之间的等待（拟人思考时间），默认 300ms
max_output_tokens: 8192  # 各协议输出长度上限的统一取值，默认 8192

output: ""       # Excel 输出路径（留空则自动生成时间戳文件名，如 bench-20260618T150405.xlsx）
no_excel: false  # 跳过 Excel 导出，仅打印终端报告
no_tui: false    # 禁用 TUI，使用纯文本控制台输出（stdout 非终端时自动禁用）
```

`warmup`、`cooldown`、`think_time` 用指针类型区分「未配置（取默认值）」与「显式设为 false/0s」，所以显式写 `cooldown: 0s`、`think_time: 0s` 就是真的不等待，不会被悄悄改回默认值。

#### warmup_per_level（每档预热）

默认只在每个模型的**首个**档位前预热一次（并发取首档并发）。档位切换同样有冷启动批效应——新增的 worker 要建连、上游可能要扩容——而每档开头的 ramp 只摊开了建连压力，没有把它挡在测量窗口外。`warmup_per_level: true`
让每个档位开始前都按**本档**并发/速率预热 `warmup_duration`，代价是总耗时增加「档位数 × warmup_duration」。预热始终按时长运行，不受 `requests_per_level` 约束。

#### think_time（思考时间）

closed-loop 下，同一个 worker 从「收到上一个响应」到「发出下一个请求」之间的等待，用来模拟真人看完回答再提问的停顿。默认 **300ms**（保持历史行为），`open` 模式不使用该项——发送节奏由 `request_rate` 决定。

| 场景                            | 建议取值                                             |
|---------------------------------|------------------------------------------------------|
| 模拟真实用户节奏（默认）        | 保持 `300ms`，或按业务场景调大                       |
| 与 evalscope 等工具对拍吞吐数字 | 显式设为 `0s`，对齐它 `--rate -1` 的「完成即发」语义 |

**注意可比性**：同样的 `concurrency` 下，`think_time` 越大实际负载越低，QPS/TPS 会更保守。改动它之后，新旧两次压测的吞吐数字不能直接比较——所以默认值刻意保持 300ms 不变，对拍时临时改成 0s
而不是把默认值改掉。当前取值会打印在配置头并写入 Excel 总览与 JSON 报告，事后可核对口径。

思考时间会被截断到档位 deadline：worker 在临近档位结束时完成请求，不会白等一整个 `think_time` 才发现超期（否则档位的排空期会被凭空拉长）。

#### max_output_tokens（输出上限）

各协议输出长度上限的统一取值，映射到 `max_tokens`（openai/anthropic）、`maxOutputTokens`（gemini）、`max_output_tokens`（responses），默认 **8192**。

压测要控制输出长度，否则 E2E/TPOT/TPS 会混入「模型这次想写多长」的自然波动，同一模型的分位数失真、跨协议横向对比也不公平，所以默认是固定值而非不限。调小它可以显著缩短单请求耗时、在同样时长内拿到更多样本（适合快速回归），但
**改动后与历史报告不可比**。

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

### 负载模式：closed-loop 与 open-loop

```yaml
load_mode: "open"          # closed（默认，不写就是这个）| open
request_rate: [ 5, 10, 20 ]  # load_mode: open 时必填，长度必须与 concurrency 相等
#open_loop_unbounded: true   # open 模式下去掉在途上限（严格开环），默认 false
#requests_per_level: [ 500 ] # 每档请求数上限：一项作用于全部档位，或与 concurrency 等长逐档对应；默认纯时长制
```

| 字段                  | 必填                     | 说明                                                                                                                                                     |
|-----------------------|--------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|
| `load_mode`           | 否                       | `closed`（默认）：每个 worker 等响应才发下一个（现有行为）；`open`：按目标 RPS 泊松到达发送请求，不等响应                                                |
| `request_rate`        | `load_mode: open` 时必填 | 与 `concurrency` 一一对应的目标 RPS 列表，长度必须一致；`open` 模式下 `concurrency[i]` 改为该档位「同时在途请求数上限」，防止过载时本地无限堆积协程/连接 |
| `open_loop_unbounded` | 否                       | 仅 `open` 下合法。`true` 时不再受 `concurrency` 在途上限约束，到点即发，是严格意义的开环（对标 evalscope `--open-loop`）                                 |
| `requests_per_level`  | 否                       | 各档位请求数上限。档位在「发满该数量」与「到达 `duration`」两者**先到者**结束（对标 evalscope `--number` + `--duration`）；留空为纯时长制                |

**为什么需要 open-loop**：closed-loop 下，一旦服务端出现排队延迟，压测客户端会因为「等响应才发下一个」自动降低实际发送速率，相当于自己给自己让路——这就是 **Coordinated Omission**：服务端越慢，closed-loop
测出的延迟分位数反而越「好看」，因为真正被压垮期间本该发出、却被延迟发出的那些请求根本没有被采样到。closed-loop 只能回答「给定并发数，延迟大概是多少」，open-loop 才能回答「给定目标
RPS（贴近线上真实流量的到达模式），尾延迟会不会因排队而爆炸」。

**什么时候切到 open-loop**：已知或想设定线上目标 RPS、需要验证某个 RPS 水位下 P99/P999 是否可接受时，用 `open`；只是想画一条「并发数 vs 延迟」的曲线做粗略容量摸底时，`closed`（默认）够用。两种模式可以用同一份配置分别跑两次，互相佐证。

**有界 vs 无界开环**：默认的有界开环在途请求数到达 `concurrency[i]` 后，新到达会先排队等空位——过载时退化成「限速闭环」，测出的是「被压测机自我保护后」的数字，安全但偏乐观。`open_loop_unbounded: true`
去掉这层保护，到点即发，服务端跟不上时在途请求只会越积越多，测出的排队延迟才是真实流量下的形态，是找系统**饱和点**的正确姿势；代价是压测机自身的协程/连接/内存会随积压增长，务必先估算承受力再开。

**请求数制**：`requests_per_level` 让每档在发满 N 个请求后就结束（在途请求照常等完），不必等 `duration` 到期，适合短回归和与 evalscope 默认的请求数制对拍。注意两点：档位仍受 `duration` 兜底（先到者结束，不想被时长截断就把
`duration` 调大）；请求数制下吞吐分母是实际用时（含最后一批请求的排空），与 evalscope 口径一致，而纯时长制的分母是名义窗口。TUI/控制台的进度条仍按时长渲染，档位提前结束属正常现象。

### SLO 达标率（Goodput）

```yaml
slo:
  ttft_ms: 800    # 可选，TTFT 阈值（毫秒）
  tpot_ms: 50     # 可选，TPOT 阈值（毫秒）
  e2e_ms: 10000   # 可选，E2E 时延阈值（毫秒）
```

三项阈值都可选，只填要考察的维度；一个都不填等价于不配置 `slo` 这一整块（默认行为，不产生任何新报表内容）。开启后，终端和 Excel 会额外展示 **Goodput**：同时满足全部已配置阈值的请求占总请求数的比例——失败请求必然不达标，TTFT/TPOT
阈值只在流式端点生效。相比单纯的分位数，Goodput 直接回答「这个并发/RPS 水位下，服务能不能上线」这个业务问题。

### 结构化输出与延迟分布直方图

```yaml
json_output: "bench.json"          # 结构化 JSON 报告路径，留空（默认）不导出
csv_output: "bench.csv"            # 逐档位 CSV 汇总路径，留空（默认）不导出
sample_output: "bench-samples.jsonl"  # 原始样本 JSONL 路径，留空（默认）不导出
show_histogram: true               # 终端/Excel 额外输出 E2E/TTFT 延迟分布直方图，默认 false
```

JSON/CSV 用于 CI 里做基线比对、画趋势图：JSON 是完整的每档位分位数/吞吐/goodput/错误计数摘要（不含错误明细原始记录），CSV 是每个 `模型×档位` 一行的扁平化汇总，字段见 `internal/report/structured.go`。
`show_histogram` 开启后，终端在每个档位报告下追加 ASCII 条形图，Excel 追加一个「延迟分布」sheet（按对数刻度分桶，避免长尾分布把绝大多数样本都挤在第一个桶里）。

#### sample_output（原始样本）

一行一条请求的 JSONL， **含失败请求**，逐档位追加落盘（中途 `Ctrl+C` 也不会丢已完成档位）。报表里的分位数是聚合结果，一旦想换口径重算——换分位算法、按时段切窗口看稳态、剔除某类样本再看分布——没有原始样本就只能重跑一遍压测，而压测环境往往不可复现。

每行包含：档位标识（`model`/`provider`/`token_group`/`concurrency`/`target_rate_rps`/`level_start`）、`ts`（请求发起时刻）、`success`、`ttft_ms`、`e2e_ms`、token 数（`input_tokens`/`output_tokens`/
`cached_input_tokens` 及 `output_estimated`/`cache_reported` 标记）、`itl_ms`（逐输出事件间隔数组）、失败时的 `error_type`/`error`/`request_id`。

```bash
# 例：重算某档位 TTFT 的 P99.99（报表只到 P99.9）
jq -s 'map(select(.concurrency==50 and .success)) | sort_by(.ttft_ms) | .[(length*0.9999|floor)].ttft_ms' bench-samples.jsonl
```

**体积提示**：每条成功样本都带 `itl_ms` 数组（长度约等于输出事件数），长输出高并发的档位单档可达几十 MB，按需开启。

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
2. **预热（可选）**：每个模型在自己的首个正式档位前预热（`warmup_per_level: true` 时每个档位前都预热），并发数/目标 RPS 取即将开始档位的取值，时长由 `warmup_duration` 控制，结果丢弃不计入报表。
3. **正式测试**：按 `models × concurrency` 的顺序逐档运行。配置了 `requests_per_level` 时，档位在发满该数量与到达 `duration` 两者先到者结束。closed-loop（默认）时每档内部按并发数错峰启动 worker（错峰窗口 ≈1ms/worker，上限 5s 且不超过 `duration` 的 1/6），worker
   持续发请求直到档位 `duration` 到期或被早停取消，等响应才发下一个；open-loop（`load_mode: open`）时按 `request_rate` 中对应的目标 RPS 以泊松过程持续发出请求（不等响应），`concurrency`
   改为同时在途请求数上限，超过上限时新请求会阻塞等空位再发出。两种模式下，deadline 前发出、deadline 后才完成的长尾请求都仍计入时延分位数，但不计入 QPS/TPS 分母。
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
  Metric            P50          P90          P95          P99          Avg          StdDev       N
  --------------------------------------------------------------------------------------------------
  TTFT              320.5ms      520.1ms      680.2ms      950.1ms      350.8ms      180.3ms      810
  TPOT              18.2ms       22.4ms       25.6ms       32.1ms       19.4ms       4.1ms        810
  ITL               15.1ms       30.2ms       48.3ms       90.7ms       17.2ms       12.6ms       7920
  E2E Latency       3.20s        4.10s        4.80s        5.90s        3.45s        780.2ms      810
  --------------------------------------------------------------------------------------------------
  TPS:     2650.30 tok/s  |  TPM:  159018.0 tok/min  |  QPS: 13.5000 req/s  |  QPM: 810.00 req/min  |  I/O Ratio: 0.081
  Decode: 51.5 tok/s (单流解码速度，1/平均 TPOT)
  Error types: timeout: 2

============================================================
  SUMMARY TABLE
--------------------------------------------------------------------------------
  Model (Provider)        Token Group       Conc   QPS       TPS       TTFT P50    TTFT P95    I/O Ratio
  --------------------------------------------------------------------------
  gpt-5.6-sol (openai)    openai-channel    50     13.500    2650.3    320.5ms     680.2ms     0.081
============================================================
```

per-level 明细下方可能出现的提示行，含义如下：

| 提示                                            | 触发条件与含义                                                                                                                   |
|-------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------|
| `[WARN] 平均 E2E 时延达吞吐窗口的 N%...`        | 平均端到端时延占 `duration` 比例 ≥20% 时出现；QPS/TPS 只统计窗口内完成的请求，比例越高吞吐被低估越严重，建议加大 `duration` 重测 |
| `[WARN] 本档位因错误率超阈值被提前终止...`      | 该档位触发了 `early_stop`，未跑满设定时长                                                                                        |
| `[NOTE] N 条样本未通过速率有效性校验...`        | 生成窗口过窄（一次性到达）或超出单流物理天花板的样本，被剔除出 TPOT/TPS/TPM/ITL 分位数，疑似网关缓冲或压测机读流饥饿             |
| `[NOTE] N/M 条成功样本的 token 数为文本估算...` | provider 未上报 usage，token 数按输出字符数粗估（`len/4`），速率分位数可信度下降                                                 |
| `[WARN] low sample count (N=n)...`              | 该档位样本数 <20，P95/P99 可能不准                                                                                               |

`Concurrency` 后面可能跟一段 `Target Rate: N req/s (open-loop)`，出现即表示该档位是 open-loop（`load_mode: open`），否则为 closed-loop（默认）。配置了 `slo` 时会在吞吐行下方追加一行
`Goodput: NN.N% (SLO: ...)`；开启 `show_histogram` 时会在档位报告末尾追加 E2E/TTFT 的 ASCII 分布直方图。这些都是纯追加内容，不配置对应项时终端输出与现状完全一致。

图片生成端点（`openai-image`）没有 TTFT/TPOT/ITL/TPS/TPM，只展示 E2E Latency、QPS、QPM。

### Excel 报告

除非配置 `no_excel: true`，压测结束后会导出一份 `.xlsx`（默认文件名 `bench-<时间戳>.xlsx`，可用 `output` 覆盖），包含 8 个 sheet（开启 `show_histogram` 后追加第 9 个）：

| Sheet               | 内容                                                                                                                                                                                    |
|---------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 总览                | 测试日期、接口地址、时长/并发档位、负载模式、模型与 token 分组概览、错误率早停配置摘要、SLO/Goodput 配置摘要、各项指标口径说明                                                          |
| TTFT延迟            | 每个 `模型×并发` 组合的 TTFT 与 E2E 延迟 P50/P95/P99/P99.5/P99.9/Avg                                                                                                                    |
| 生成速度(TPS·token) | per-request tokens/s、TPOT、ITL、TPM 分位数，以及 System TPS/TPM；备注列标注样本量不足/剔除数/估算占比                                                                                  |
| QPS压测(TPS·req)    | 实际时长、吞吐窗口、目标 RPS（open-loop）、QPS/QPM、成功率、成功/失败请求数、Goodput/SLO 阈值；备注列包含吞吐低估提示与早停提示                                                         |
| 输入输出Token比     | per-request I/O Ratio 分位数与 System I/O Ratio；per-request 与 System 缓存命中率、总输入/缓存 token 数                                                                                 |
| 分位数全景          | 每个 `档位 × 指标` 一行，横向列出 Min/P10/P25/P50/P75/P90/P95/P99/P99.5/P99.9/Max/Avg/StdDev 全量统计（TTFT/TPOT/ITL/E2E/tokens·s/TPM/IO Ratio/Cache Hit Rate）；本档无样本的指标不占行 |
| 错误分析            | 每个 `模型×并发` 组合的总请求数、失败数、成功率、是否提前终止，以及按 `types.ErrorTypeOrder` 顺序的各错误类型计数                                                                       |
| 错误明细            | 每条失败请求一行：发生时间、错误类型、RequestID、总时延、错误信息，按时间排序                                                                                                           |
| 延迟分布（可选）    | 仅当至少一个档位携带直方图数据（即运行时开启了 `show_histogram`）才出现；每个 `模型×并发×指标（E2E/TTFT）` 一行，列出各分桶的区间与样本数                                               |

图片生成端点（`openai-image`）不出现在 TTFT延迟/生成速度/输入输出Token比 三个 sheet（这些指标对它无意义），但会出现在 QPS压测 与 错误分析/错误明细。

## 指标口径

- **TTFT / TPOT / E2E Latency**：仅统计成功请求的时延分位数；TPOT = `(总时延 - TTFT) / 输出 token 数`
- **分位数档位**：全部指标统一给出 Min/P10/P25/P50/P75/P90/P95/P99/P99.5/P99.9/Max/Avg/StdDev。全排序 + 最近秩（`idx = ceil(n*p) - 1`）精确计算，不用近似算法/直方图插值。终端表只渲染
  P50/P90/P95/P99/Avg/StdDev 六列（保证一行放得下），全量在 Excel「分位数全景」sheet 与 JSON 报告里。StdDev 为 **总体**标准差（分母 N）；单样本时为 0，与「无数据」的 `N/A` 区分
- **Decode tok/s**：`1 / 平均 TPOT`，单流解码速度，对标 evalscope 的同名指标。与 per-request `tokens/s Avg` 同源但口径不同——后者是各请求速率的算术均值，长短响应混跑时被短响应拉高；Decode 是「平均每个 token
  要等多久」的倒数（调和均值口径）。入样口径随 TPOT，未通过速率有效性校验的样本已被剔除
- **ITL**：逐输出内容事件之间的间隔，按 SSE 事件粒度采样——第一个内容事件打 TTFT，之后每次再出现输出内容都记一条与上一次的间隔。多数 provider 一个事件对应一个或几个 token，因此这是 **逐 token 生成间隔的近似值，不是逐
  token 精确值**；剔除口径与 TPOT/TPS 一致（同一请求未通过 `tokstats.ValidStreamTPS` 校验时，其 ITL 样本也不入池）。相比 TPOT 的单一均值，ITL 的分位数能看出解码过程中的周期性卡顿（如 KV cache 争抢、batching
  切换）
- **per-request TPS/TPM**：`输出 token 数 / 生成窗口秒数` 的分位数；生成窗口过窄（一次性到达）或超出单流物理天花板的样本会被剔除，剔除数计入 `GenSpeedExcluded`
- **System TPS/TPM**：吞吐窗口内完成的请求总 token 数 / 窗口时长（`Window`），区别于 per-request 分位数——系统级口径反映整体吞吐，per-request 口径反映单条流的解码速度
- **QPS/QPM**：吞吐窗口内完成的成功请求数 / 窗口时长
- **Steady / Last 30s**：稳态吞吐窗口，对标 evalscope 的 Workload Throughput 表。**Steady** 掐掉窗口头尾各 10%，只算在中间 80% 内**完成**的请求 / 0.8×窗口；**Last 30s** 只算窗口最后 30 秒内完成的请求 / 30s，窗口不足
  30s 时显示 N/A。三者（Overall/Steady/Last 30s）差异大说明档位内负载未进入稳态——ramp 未过、上游还在扩容、或收尾衰减占比过高，此时 Steady 比 Overall 更接近「稳定运行时能扛多少」
- **usage 对拍**（需 `tokenizer`）：服务端 `completion_tokens` 与本地分词器对可见输出文本计数的相对偏差 `(usage - local) / local`。报表给出对拍条数、|偏差| 超过 `usage_drift_pct` 的条数、|偏差| 的分位数；思考型请求与无 usage
  的请求不对拍。原始样本里逐请求带 `local_output_tokens`/`usage_drift_pct`/`usage_drifted`
- **I/O Ratio**：输入/输出 token 比，per-request 分位数与系统级总量比（`总 input_tokens / 总 output_tokens`）两种口径
- **Cache Hit Rate**：`cached_input_tokens / input_tokens * 100%`，同样有 per-request 分位数与系统级总量比两种口径；仅统计上报了缓存字段的 provider，未上报时显示 `N/A`（区别于「上报了但命中率为 0%」）
- **Goodput**：同时满足全部已配置 `slo` 阈值（TTFT/TPOT/E2E）的请求数占总请求数的比例；失败请求必然不达标，未配置 `slo` 时显示 `N/A`（区别于「配置了但 0%」）
- **错误类型**：`timeout`/`net_timeout`/`canceled`/`dns_error`/`conn_refused`/`conn_reset`/`tls_error`/`connect`/`rate_limited`/`server_error`/`http_error`/`upstream_error`/`stream_broken`/
  `stream_truncated`/`no_content`，按此固定顺序展示在进度和报表中

## 注意事项

1. **`gpt-image-2` 被硬编码排除**：`main.go` 里写死了排除名单 `excludedModel = "gpt-image-2"`，模型名（大小写不敏感）里只要包含这个字符串就会在启动时被跳过并打印 `[skip] 已排除模型: ...`
   ，不会计入本次压测；配置里写了这个模型但发现被跳过属预期行为
2. **预检失败会中止整轮压测**：任一模型的预检请求失败，压测直接终止且不产生报表，先检查该模型的 `base_url`/`token_group`/网络连通性
3. **`token_group` 缺 token 时不会回退到 `default`**：只有完全不配置 `token_group` 的模型才使用 `tokens`/`default` 分组
4. **并发数越高，`duration` 建议越长**：平均 E2E 时延接近吞吐窗口时 QPS/TPS 会被系统性低估，报表里的 `[WARN]` 会提示这种情况
7. **请求数制的进度条仍按时长渲染**：配置了 `requests_per_level` 的档位会在发满后提前结束，TUI/控制台的时间进度条不会走满，属正常现象；档位报告头里的 `cap N` 标注了本档上限
8. **`tokenizer` 词表必须与被测模型匹配**：借用别家词表不报错但计数静默失真；Claude/Gemini 词表不公开，对这两家配 `tokenizer` 只能拿到「按某个开源词表近似」的输入长度，usage 对拍结果没有意义
6. **`think_time`/`max_output_tokens` 影响可比性**：两者都会改变实际负载与单请求耗时，改动后新旧报告的吞吐/时延数字不能直接比较；当前取值会打印在配置头、写入 Excel 总览与 JSON 报告，对比数据前先核对这两项是否一致
5. **`load_mode: open` 时 `concurrency` 的含义变化**：不再是「协程数」，而是该档位「同时在途请求数上限」；`request_rate` 才是真正驱动发送节奏的目标 RPS，两者长度必须一致

## 参数归一化语义

本工具的 SSE 流式解析已下沉到 `internal/llm/sse`（`ParseSSELine`/`SSEIsTerminal`/`SSEHasOutputContent`/`ConsumeSSEUsage`/`ApplySSEEvent` 等纯函数），三个工具共享同一套协议判定与 usage
提取逻辑。本工具在参数映射总表中的位置：

| 统一参数                       | performance 的实现                                                                                                                            | 说明                                                                  |
|--------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------|
| 输出上限                       | `max_output_tokens` 配置项，默认 8192；映射为 `max_tokens`（openai/anthropic）/ `maxOutputTokens`（gemini）/ `max_output_tokens`（responses） | 压测对比需控制输出长度，默认固定 8192；可配置但改动后与历史报告不可比 |
| `stream_options.include_usage` | 恒为 true（openai）                                                                                                                           | 与 evaluation 默认、benchmark 显式开启对齐                            |
| `temperature` / `top_p`        | 不传                                                                                                                                          | 压测用服务端默认值                                                    |
| thinking / reasoning_effort    | 不传                                                                                                                                          | 不在压测范围                                                          |

**token 统计口径**：与 evaluation/benchmark 一致，采用 usage 上报值；Gemini 的 `thoughtsTokenCount` 计入 `completion_tokens`（思考时间在生成窗口里，token 计入分母保证跨协议可比）。usage 缺失时，配置了 `tokenizer`
就用本地分词器对可见文本计数，否则按文本构成粗估（ASCII 4 字符/token、CJK 1.5 字符/token 加权）；两种情况都打 `OutputEstimated` 标记。
