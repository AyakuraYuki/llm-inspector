# LLM Benchmark Tool

这是一个基于 OpenAI-Compatible API 的 LLM 能力测试工具，用内置的 huggingface 题库和自定义问题去压模型，同时统计答题准确率和推理性能。

## 功能特性

- 并发执行多个问题测试，支持流式响应
- 内置三个 huggingface 题库（AIME 2025、AIME 2026、MMLU-Pro），编译时通过 `go:embed` 打进二进制
- 支持在配置文件中混入自定义问题
- 性能指标：
    - **TTFT** (Time To First Token): 首字符延迟
    - **TPS** (Tokens Per Second): 每秒生成 token 数
    - **TPM** (Tokens Per Minute): 每分钟生成 token 数
    - **总用时**: 完整响应时间
- **答案验证**: 自动从 `\boxed{}` 中提取模型答案并与标准答案比较，会先剔除思考内容避免误判
- **准确率统计**: 计算模型在有标准答案的问题上的正确率
- **进度心跳**: 每 30 秒输出整体进度以及正在执行的问题及其已运行时长
- 输出统一收敛到单个 `reports_TIMESTAMP/` 目录：一份 JSON 汇总 + 每题一份文本报告

## 快速开始

### 1. 准备数据集

内置题库不入库（`cmd/benchmark/internal/dataset/hf/` 已在 `.gitignore` 中），需要先从 huggingface 拉取。这一步依赖 [huggingface CLI](https://huggingface.co/docs/huggingface_hub/guides/cli)：

```bash
pip install -U "huggingface_hub[cli]"
```

然后在 **仓库根目录**下执行（`benchmark` 与 `evaluation`、`performance` 共用同一个根 `Makefile`）：

```bash
make setup
```

`make setup` 会下载三个数据集到 `cmd/benchmark/internal/dataset/hf/` 对应目录：

| 数据集    | huggingface repo     | 落地路径                                                |
|-----------|----------------------|---------------------------------------------------------|
| AIME 2025 | `math-ai/aime25`     | `cmd/benchmark/internal/dataset/hf/math-ai/aime25/`     |
| AIME 2026 | `math-ai/aime26`     | `cmd/benchmark/internal/dataset/hf/math-ai/aime26/`     |
| MMLU-Pro  | `TIGER-Lab/MMLU-Pro` | `cmd/benchmark/internal/dataset/hf/TIGER-Lab/MMLU-Pro/` |

数据集是 `go:embed` 的输入， **没有下载完成的话编译会直接失败**。

### 2. 编译

`benchmark` 没有自己独立的 Makefile，跟 `evaluation`、`performance` 共用仓库根目录下的同一份 `Makefile`，所以下面的命令都要在 **仓库根目录**执行：

```bash
make build-benchmark
```

产物落在 `build/benchmark/` 目录：

- `build/benchmark/benchmark-<GOOS>_<GOARCH>`：可执行文件
- `build/benchmark/config.yml`：从 `cmd/benchmark/configs/config.example.yml` 复制的配置模板

默认交叉编译目标是 `darwin/amd64`，可以通过变量覆盖：

```bash
make build-benchmark GOOS=linux GOARCH=arm64
```

`make build`（不带后缀）会把 `benchmark`、`evaluation`、`performance` 三个工具一起编译。清理产物和 build cache：

```bash
make clean
```

### 3. 运行

改好配置后带 `-config` 启动，配置文件是必填参数：

```bash
./build/benchmark/benchmark-darwin_amd64 -config ./build/benchmark/config.yml
```

也可以不编译直接跑（在 `cmd/benchmark` 目录下）：

```bash
cd cmd/benchmark
go run . -config ./configs/config.example.yml
```

## 配置文件

完整示例见 [configs/config.example.yml](configs/config.example.yml)。

### 连接与运行参数

```yaml
base_url: "https://api.openai.com/v1" # API 服务地址
api_key: "sk-xxx"                     # Bearer token
model: "gpt-4"                        # 待测模型名称
max_tokens: 65536                     # 最大输出 token 数限制
max_workers: 1                        # 并发数
reasoning_effort: high                # 思考强度，支持 low | medium | high | max（部分模型）
```

| 字段               | 必填 | 默认值  | 说明                                                                    |
|--------------------|------|---------|-------------------------------------------------------------------------|
| `base_url`         | 是   | -       | OpenAI-Compatible 的 API 地址                                           |
| `api_key`          | 是   | -       | 鉴权 token                                                              |
| `model`            | 是   | -       | 待测模型名                                                              |
| `max_tokens`       | 否   | `65536` | 映射到请求的 `max_completion_tokens`，填 0 或不填时取默认值             |
| `max_workers`      | 否   | `1`     | 同时在跑的问题数，小于 1 时按 1 处理                                    |
| `reasoning_effort` | 否   | 空      | 非空时透传给请求的 `reasoning_effort`（会转小写），具体取值参考模型规格 |

`dataset` 和 `custom_questions` 至少要有一个能产出问题，否则启动时会以「缺少测试数据集」报错退出。

### 内置题库

```yaml
dataset:
  aime25: true                        # 2025 AIME 题库
  aime26: false                       # 2026 AIME 题库
  mmlu_pro:
    enabled: false                    # 使用 MMLU-Pro 题库
    use_validation: false             # 追加 MMLU-Pro 验证题库
    use_pickup: false                 # 从 MMLU-Pro 题库随机摘选问题
    biology: 5                        # 摘选 n 个生物学分类的问题
    business: 5
    chemistry: 5
    computer_science: 5
    economics: 5
    engineering: 5
    health: 5
    history: 5
    law: 5
    math: 5
    philosophy: 5
    physics: 5
    psychology: 5
    other: 5
```

题量与来源：

| 配置                             | 题量       | 说明                                           |
|----------------------------------|------------|------------------------------------------------|
| `aime25`                         | 30         | 2025 AIME I / II，答案是整数                   |
| `aime26`                         | 30         | 2026 AIME I / II，答案是整数                   |
| `mmlu_pro.use_validation`        | 70         | MMLU-Pro validation 集，十选一（选项最多到 J） |
| `mmlu_pro.use_pickup`            | 按分类求和 | 从 test 集打乱后按分类摘选                     |
| `mmlu_pro.enabled` 但未开 pickup | 12032      | MMLU-Pro test 全集                             |

关于 MMLU-Pro 的两个要点：

- `enabled: true` 且 `use_pickup: false` 时会 **回退到加载 test 全集**，也就是 12032 道题。想小规模试跑就一定要开 `use_pickup` 并配好每个分类的数量。
- `use_validation` 是 **追加**行为，开启后 70 道验证题会和 pickup/全集的题目一起进入本次测试。
- 每个分类的摘选数量会被该分类的实际题量截断，填得比题库大不会报错。摘选是随机的，每次运行的题目组合都不一样。

AIME 题目会自动追加 `Please reason step by step, and put your final answer within \boxed{}.`；MMLU-Pro 题目会自动拼装成带 `(A) (B) (C) ...` 选项的多选题模板，并要求把选项字母放进 `\boxed{}`
。这些提示词由程序拼接，不需要在配置里写。

### 自定义问题

```yaml
custom_questions:
  - { question: "Explain quantum entanglement in one paragraph." }
  - { question: "What is 2+3?\n\nPlease reason step by step, and put your final answer within \\boxed{}.", answer: "5" }
```

- `question`: 问题文本，必填
- `answer`: 标准答案，可选。省略或为 `null` 时只测性能不判对错

自定义问题原样发送，不会自动追加任何提示词，需要判题的话得自己在问题里写清 `\boxed{}` 的要求。这些问题在报告里的 dataset 标记为 `__custom_questions__`，并排在内置题库之后。

## 输出

### 控制台输出

运行过程中实时输出带时间戳的日志：

```
[14:30:25] Loaded 33 questions
[14:30:25] Config: max_tokens=65536, max_workers=2
[14:30:25] Model: gpt-4, Base URL: https://api.openai.com/v1
[14:30:25] Benchmark started
[14:30:25] Question 1 started
[14:30:25] Question 2 started
[14:30:26] Question 1 first token received (TTFT=250ms)
[14:30:40] Question 1 completed (1/33 done): TTFT=250ms, Total=15000ms, Tokens=1500, TPS=100.00 ✓ Correct
[14:30:55] Progress: 1/33 completed, 2 in progress (elapsed 30s)
[14:30:55]   -> question 2 running for 30s
[14:30:55]   -> question 3 running for 15s
[14:31:10] Question 3 completed (3/33 done): TTFT=200ms, Total=5000ms, Tokens=50, TPS=10.00 ✗ Wrong [⚠ finish_reason=null]
...
```

跑完后打印统计汇总：

```
============================================================
BENCHMARK STATISTICS
============================================================
Total questions: 33
Successful: 33
Failed: 0

Finish Reason Distribution:
  stop: 30 (90.9%)
  null: 3 (9.1%)

Questions with answers: 32
Correct answers: 28
Wrong answers: 4
Accuracy: 87.50%

Average TTFT: 245 ms
Average Total Time: 14500 ms
Average Tokens: 1450
Average TPS: 98.50
Average TPM: 5910.00
============================================================
```

统计只覆盖没有报错的问题；准确率的分母是「有标准答案且成功完成」的题数。

### 报告目录

每次运行会在当前工作目录下创建一个 `reports_TIMESTAMP/`，本次的所有输出都在里面：

```
reports_20260810_143025/
├── benchmark_results.json
├── question_001_aime25.txt
├── question_002_aime25.txt
├── question_031_MMLU_Pro.txt
├── question_033___custom_questions__.txt
...
```

单题报告的文件名格式是 `question_<序号>_<数据集>.txt`，序号和 JSON 里的 `question_index + 1` 对应。

### JSON 汇总

`benchmark_results.json` 是一个数组，每个元素对应一道题：

```json
[
  {
    "question_index": 0,
    "question": "问题文本...",
    "expected_answer": "70",
    "model_answer": "模型的完整回答...",
    "extracted_answer": "70",
    "is_correct": true,
    "finish_reason": "stop",
    "ttft_ms": 250,
    "total_time_ms": 15000,
    "tokens_used": 1500,
    "tps": 100.0,
    "tpm": 6000.0
  }
]
```

字段说明：

| 字段               | 说明                                                                           |
|--------------------|--------------------------------------------------------------------------------|
| `question_index`   | 问题序号，从 0 开始                                                            |
| `question`         | 完整的问题文本（含程序拼接的提示词）                                           |
| `expected_answer`  | 标准答案，没有标准答案时该字段不输出                                           |
| `model_answer`     | 模型的完整回答                                                                 |
| `extracted_answer` | 从 `\boxed{}` 中提取的答案                                                     |
| `is_correct`       | 答案是否正确，仅当有标准答案时输出                                             |
| `finish_reason`    | 响应结束原因：`stop` 正常结束、`length` 达到 token 限制被截断、`null` 异常终止 |
| `ttft_ms`          | 首字符延迟（毫秒）                                                             |
| `total_time_ms`    | 总用时（毫秒）                                                                 |
| `tokens_used`      | 生成的 token 数（近似值，见下文）                                              |
| `tps` / `tpm`      | 每秒 / 每分钟 token 数                                                         |
| `error`            | 错误信息，仅在出错时输出                                                       |

### 单题报告

每份 `question_*.txt` 包含问题原文、标准答案、模型完整响应、提取的答案、判题结果和性能指标。出错或 `finish_reason` 异常时还会附带一段成因分析：

```
================================================================================
QUESTION #3 BENCHMARK REPORT
================================================================================

QUESTION:
--------------------------------------------------------------------------------
An isosceles trapezoid has an inscribed circle tangent to each of its four...
Please reason step by step, and put your final answer within \boxed{}.

EXPECTED ANSWER:
--------------------------------------------------------------------------------
504

MODEL RESPONSE:
--------------------------------------------------------------------------------
Let me solve this step by step...
The answer is \boxed{504}.

EXTRACTED ANSWER:
--------------------------------------------------------------------------------
504

VERIFICATION:
--------------------------------------------------------------------------------
✓ CORRECT

PERFORMANCE METRICS:
--------------------------------------------------------------------------------
TTFT (Time To First Token): 245 ms
Total Time:                 14500 ms
Tokens Generated:           1450
TPS (Tokens Per Second):    100.00
TPM (Tokens Per Minute):    6000.00
Finish Reason:              stop

================================================================================
```

判错时会同时列出 `Expected` 和 `Got`，方便区分是模型算错了还是答案提取出了问题。

`finish_reason` 用来区分失败的原因：

- **答案错误但响应正常**：`finish_reason=stop`，模型正常完成推理但给出了错误答案
- **响应被截断**：`finish_reason=length`，达到了 `max_tokens` 限制，`extracted_answer` 可能为空
- **异常终止**：`finish_reason=null`，响应提前终止（网络问题、API 错误等）

对于请求层面的失败（建流失败、流中断、超时），报告里会把错误归类到 EOF、JSON 截断、超时、建流失败几种情形并给出可能成因。

## 判题逻辑

答案提取按以下顺序进行：

1. 先剔除思考内容，只在正文里找答案。识别的闭合标签有 `</think>`、`</thinking>`、`</reasoning>`、`</thought>`、`◁/think▷`、`[/THINK]`，取最靠后的那个之后的内容。只匹配闭合标签是因为部分模型或网关会丢掉起始标签。
2. 在正文里取 **最后一个**内容非空的 `\boxed{...}`，支持嵌套大括号。取最后一个是因为模型常在推导过程中多次提及 `\boxed{}`。
3. 如果正文里找不到（思考标签异常或回答被截断），回退到在完整回答里找。

比较答案时会做标准化：去首尾空格、转小写、去掉所有空格和逗号。所以 `1,024` 和 `1024`、`C` 和 `c` 都会判为一致。

## 运行测试

```bash
cd cmd/benchmark
go test ./...
```

数据集相关的测试会校验 AIME 题库的答案表和 MMLU-Pro 的题量，需要先跑过 `make setup`。

## 注意事项

1. **Token 计数是近似值**：当前实现按流式响应的 chunk 数量估算 token 数，不是 API 返回的精确 usage。TPS / TPM 同样受此影响，适合做相对比较而非绝对指标。

2. **TTFT 的口径**：只在收到第一个非空的 `delta.content` 时计时。如果网关把思考内容放在单独的字段里，思考阶段的耗时会被算进 TTFT。

3. **超时**：单题固定 30 分钟超时，超时后该题记为失败并进入统计的 Failed 计数。

4. **并发控制**：`max_workers` 决定同时在跑的问题数，需要按 API 的速率限制来调。开高了容易触发限流，表现为大量建流失败。

5. **MMLU-Pro 全集很大**：12032 道题在低并发下会跑很久，且会产生同样数量的报告文件。生产验证前建议先用 `use_pickup` 小规模试跑。

6. **API 兼容性**：使用 OpenAI SDK，支持所有 OpenAI-Compatible API（Azure OpenAI、本地部署的模型、各类网关等）。`reasoning_effort` 是可选透传，不支持该参数的服务留空即可。

## 题源

- AIME 2025: [math-ai/aime25](https://huggingface.co/datasets/math-ai/aime25)
- AIME 2026: [math-ai/aime26](https://huggingface.co/datasets/math-ai/aime26)
- MMLU-Pro: [TIGER-Lab/MMLU-Pro](https://huggingface.co/datasets/TIGER-Lab/MMLU-Pro)

AIME 题目原始来源为 Mathematical Association of America (MAA)，答案表可对照 MAA 许可转载页面：

- [2025 AIME I 答案表](https://live.poshenloh.com/past-contests/aime/2025I/answers)
- [2025 AIME II 答案表](https://live.poshenloh.com/past-contests/aime/2025II/answers)

## 故障排查

### 编译报错 `pattern hf: cannot embed directory hf: contains no embeddable files`

数据集没下载。仓库里 `cmd/benchmark/internal/dataset/hf/` 只有一个 `.gitkeep` 占位，而 `go:embed` 会跳过点开头的文件，所以此时目录对 embed 来说是空的。先在 **仓库根目录**执行 `make setup`。

如果连 `hf/` 目录本身都不存在，报错会是 `pattern hf: no matching files found`，处理方式相同。

### `错误: 缺少 -config`

启动时必须带 `-config`，没有默认配置路径。

### `配置错误: 缺少 base_url / api_key / model`

这三项是必填，检查配置文件里是否写全，以及 YAML 缩进有没有问题。

### `配置错误: 缺少测试数据集`

`dataset` 里所有开关都是 `false` 且 `custom_questions` 为空。至少开一个题库或写一道自定义问题。

### `配置错误: 无法加载数据集`

数据集文件缺失或损坏，重新执行 `make setup`。注意 `hf` 目录在 `.gitignore` 中，clone 仓库后不会自带数据集。

### 所有请求都失败

检查 `base_url`、`api_key`、`model` 是否正确。单题报告里的 ERROR ANALYSIS 可以帮助定位是网络、鉴权还是限流问题。

## 参数归一化语义

三个工具（benchmark / evaluation / performance）对 OpenAI 兼容协议的参数语义已对齐（见 `internal/llm/params` 包），本工具在映射总表中的位置：

| 统一参数                | benchmark 的实现                                                       | 说明                                                  |
|-------------------------|------------------------------------------------------------------------|-------------------------------------------------------|
| 输出上限                | `max_tokens` 配置 → 请求的 `max_completion_tokens`（默认 65536）       | 保留（不映射到 `max_tokens`：o 系列模型不接受该字段） |
| `reasoning_effort`      | 配置项 `reasoning_effort`（自动转小写）                                | 与 evaluation 的 SDK 直传等价                         |
| `temperature` / `top_p` | 配置项（范围校验 0–2 / 0–1）                                           | 与 evaluation 对齐                                    |
| thinking 类厂商参数     | 配置项 `extra_thinking`（JSON 原样注入顶层 `thinking` 字段）           | 等价于 evaluation 的 `ExtraParams["thinking"]`        |
| 其他厂商参数            | fork 库的 `chat_template_kwargs` / `service_tier` / `verbosity` 等字段 | 等价于 evaluation 的 `ExtraParams` 任意参数透传       |

**token 统计**：请求带 `stream_options.include_usage=true`，`TokensUsed` 采用最终 usage chunk 的 `completion_tokens`；网关不支持该选项时回退到旧行为（内容 chunk 计数）。报告 JSON 同时输出 `prompt_tokens` /
`cached_tokens` / `reasoning_tokens`。

## 扩展建议

1. 支持断点续传（保存进度，失败后继续）
2. 支持按数据集分组统计准确率
3. 支持多种输出格式（CSV、Excel 等）
4. 超时时长和心跳间隔改为可配置



