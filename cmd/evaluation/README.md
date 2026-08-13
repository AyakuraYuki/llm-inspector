# evaluation

大语言模型可用性评测工具（Go 实现）。支持三种协议的目标端点，做由浅到深的六层评测与评分：

| 协议                       | 配置值                     | 适用场景                                        |
|----------------------------|----------------------------|-------------------------------------------------|
| OpenAI 兼容                | `protocol: openai`（默认） | new-api 等网关、vLLM/SGLang/Ollama、OpenAI 官方 |
| Anthropic Messages API     | `protocol: anthropic`      | Claude 第一方或支持该协议的网关                 |
| Gemini generateContent API | `protocol: gemini`         | Google 第一方或支持该协议的网关                 |

| 层 | 名称               | 内容                                                                                                                                                                              |
|----|--------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| L1 | API 可用性         | 模型列表可达、最小对话往返、错误语义（错误凭据/不存在模型须显式拒绝：标准码满分——Gemini 坏 key 的 400 视为标准；5xx 半分；不拒绝判 fail）、目标模型在列表中（门控层，失败即中止） |
| L2 | 协议兼容性         | SSE 流式、system prompt、max_tokens、temperature=0、多轮对话、JSON 输出、tool calling、usage 字段；扩展：stop 停止词、seed 幂等、stream_options 两态、编码与特殊字符、json_schema、并行工具调用、工具结果回传二次推理、thinking 控制、reasoning_effort、max_tokens 默认值探测、无默认 system prompt 探针 |
| L3 | 模型能力           | 内建冒烟题库（推理/指令遵循/JSON/多轮/知识，中英双语），支持自定义数据集与裁判模型打分                                                                                            |
| L4 | 稳定性             | 自一致性（N 次采样一致率）、prompt 扰动、浸测（错误率+延迟漂移）、对抗输入                                                                                                        |
| L5 | 模型性能           | TTFT/总时延分位数、单流吞吐、并发扩展曲线、实际上下文长度探针                                                                                                                     |
| L6 | 参数边界与健壮性   | 非法输入应被显式拒绝：messages 空值/缺 role/非法 role、top_p / temperature / frequency_penalty / presence_penalty 越界与类型错误、max_tokens 边界（0/负数/极大值）、max_completion_tokens 兼容、鉴权边界（未传/畸形凭据）。通过裸请求绕过 SDK 强类型校验发送畸形负载 |

L2 的协议差异说明：JSON 输出在 openai/gemini 走原生参数，Anthropic 无原生 JSON mode 故走 prompt 诱导（detail 会注明）；json_schema 同理（openai response_format / gemini responseSchema / anthropic prompt 诱导）；tool calling 三协议格式不同（tool_calls / tool_use / functionCall）由 provider
统一映射，检查时强制调用一次以排除模型自主性的干扰。seed / stream_options / frequency_penalty / presence_penalty / reasoning_effort / max_completion_tokens 为 openai（或部分 openai/gemini）参数，协议不适用时记 skip。

L6 的判定口径与 L1 错误语义一致：非法输入返回 4xx 满分；5xx 为非标准但显式拒绝，半分；2xx 接受非法输入判 fail。每项先发一次合法探针（sanity）——合法值都被拒绝时该项判定不可信，直接 fail。

规划中：L7 资源消耗（token 成本统计，所有请求的 usage 已在报告原始指标中记录）、L8 业务评测（数据集格式与 L3 相同，可直接复用）。

## 构建

```bash
go build -o evaluation ./cmd/evaluation
```

## 快速开始

```bash
cp configs/example.yaml eval.yaml   # 修改 target 的 base_url / api_key / model
./evaluation run --config eval.yaml
```

其他命令：

```bash
./evaluation list                          # 查看全部评测层与检查项
./evaluation run --config eval.yaml --layers L1,L2      # 只跑指定层
./evaluation run --config eval.yaml --dataset my.yaml   # 用自定义题库替换 L3 内建题库
```

退出码：评测结论为 `pass` 时退出 0，否则退出 1（可直接接入 CI）。

## 配置说明

```yaml
target: # 被测端点（必填）
  base_url: http://localhost:8000/v1
  api_key: sk-xxx
  model: qwen2.5-72b-instruct
  protocol: openai         # openai（默认）/ anthropic / gemini
  timeout: 60s
  constraints: # 可选：模型特定约束（详见 configs/config.example.yml）
    # thinking_enable_params: { thinking: { type: enabled } }    # 思考开关参数（GLM 系示例）
    # thinking_disable_params: { thinking: { type: disabled } }
    # reasoning_efforts: [ high, medium, low ]   # 支持的 reasoning_effort 档位（openai 协议）
    # default_max_tokens: 32768                  # 官方标称的 max_tokens 默认值

judge: # 可选：裁判模型，给开放式题目打分（同样支持三种协议）
  base_url: https://api.openai.com/v1
  api_key: sk-yyy
  model: gpt-4o

layers:
  availability: { enabled: true }                    # L1
  protocol: { enabled: true }                    # L2
  capability: { enabled: true, dataset: ..., concurrency: 4 }   # L3
  stability: { enabled: true, samples: 5, soak_requests: 50, temperature: 1.0 }  # L4
  performance: { enabled: true, runs: 20, concurrency: [ 1,4,16 ], # L5
                 max_probe_tokens: 32768,
                 slo: { ttft_p99_ms: 2000, min_tokens_per_sec: 10, max_error_rate: 0.01 } }
  boundary: { enabled: true }                    # L6

thresholds:
  min_layer_score: 0.8     # 层通过线（加权平均）
  fail_fast: false         # 层不达标即中止（L1 门控恒生效）

output:
  dir: ./reports
  formats: [ json, markdown ]
```

各层的 `enabled` 省略时默认启用。

## 评分与报告

- 每个检查项产出 `pass / fail / unsupported / skip` 与 0~1 得分；`unsupported`（如服务不支持 tool calling）与 `skip` 不计入层均分
- 层得分 = 检查项加权平均；总评 = 各层加权平均（L1 10%，L2 15%，L3 30%，L4/L5/L6 各 15%）
- 结论（verdict）分三档：
    - `pass`：全部已执行层达到 `min_layer_score`，退出码 0
    - `pass_with_warnings`：总评达标但个别层未达标（模型可用、存在短板），退出码 0
    - `fail`：总评不达标；`abort`：L1 门控失败中止——退出码均为 1
- 报告写入 `reports/<时间戳>/report.json`（含全部原始指标，供 CI 消费）与 `report.md`，终端同步打印汇总

### 判定口径说明

- **max_tokens**：探测值 16 tokens。`finish_reason=length` 或 usage 中 completion_tokens 未超限即通过；输出明显超限（如限 16 产出上千 tokens）说明参数被平台/网关丢弃，判 fail
- **temperature_zero**：3 次采样，请求成功即通过（参数被接受），一致率作为得分。GPU 推理在 temp=0 下不保证逐位确定（批处理数值抖动），输出不完全一致属常见服务侧行为，不判 fail
- **内容型检查项的 token 预算**：L2/L4 中需要验证输出内容的检查项（多轮对话、自一致性、prompt 扰动等）统一使用 1024 tokens 预算。思考型（reasoning）模型会先消耗 completion tokens
  生成思考过程，预算过小时正文为空，会把"预算不足"误判成"能力缺失"。空输出会在 detail/metrics 中单独标注（`empty` 计数），不与"回答错误"混为一谈
- **答案一致性归一化**：self_consistency 比对答案前做宽松归一化（去空白/收尾标点/代码围栏、统一小写），"巴黎"与"巴黎。"视为同一答案；空输出不计为一种答案
- **context_probe**：全部档位通过时仅说明上下文"至少"达到 `max_probe_tokens`（从未触及真实上限），真实上限可能更高；只有中途失败才能给出实测上限
- **streaming_sse**：首内容 token 延迟占总耗时超过 90%（且总耗时 >2s）时，detail 会提示疑似伪流式转发或思考型模型（流式对降低体感延迟无效），不影响得分
- **seed_consistency**：seed 复现性在多数实现中是"尽力而为"语义（openai 官方文档亦如此声明），因此参数被接受即通过，一致率仅作为得分呈现
- **stop_sequence**：只验证输出不含停止词（协议侧语义）；模型是否在语义上"该停就停"不在本检查范围
- **thinking_control**：开启态以"出现独立思考内容"为满分信号，参数被接受但思考混入正文只给半分；关闭态以"无思考内容"为满分信号，token 消耗未下降视为弱异常
- **default_max_tokens**：只有输出触发 length 截断时才能验证默认值（截断点须落在标称 ±10% 内）；自然结束只能确认默认值不小于实测输出，不判 fail
- **no_default_system_prompt**：启发式探针——模型自述不完全可信，仅在输出疑似复述出注入指令时降分并提示人工复核
- **L6 边界检查**：每项先发合法探针（sanity），合法值被拒绝时该项判定不可信直接 fail；非法值 4xx 满分、5xx 半分、2xx 判 fail。`max_tokens=0` 与极大值的静默容忍属实现分歧，各记半分不判 fail

## 自定义数据集（L3 / 未来 L7）

YAML 数组，每题包含 id、category、turns（完整对话）、scorer：

```yaml
- id: biz-001
  category: refund_policy
  turns: [ { role: user, content: "我们的退货期限是几天？只回答数字。" } ]
  scorer: { type: numeric, expected: 7 }
```

内置打分器：

| type           | 说明                                 | 参数                       |
|----------------|--------------------------------------|----------------------------|
| `exact_match`  | 归一化后完全匹配                     | `expected`                 |
| `contains`     | 必含关键词（大小写不敏感，部分给分） | `keywords`                 |
| `regex`        | 正则匹配                             | `pattern`                  |
| `numeric`      | 提取输出中最后一个数字比对           | `expected`、`tolerance`    |
| `json_valid`   | 输出为合法 JSON（容忍代码围栏）      | —                          |
| `json_schema`  | JSON 对象包含必需字段                | `fields`                   |
| `bullet_count` | 要点列表项数                         | `expected`                 |
| `keyword`      | 必含 + 禁含组合                      | `keywords`、`forbidden`    |
| `lowercase`    | 全部小写                             | —                          |
| `judge`        | 裁判模型按评分标准打 0-10 分         | `rubric`（需配置 `judge`） |

注：`exact_match` / `contains` / `keyword` / `numeric` 匹配前会将上下标数字与全角数字归一为 ASCII——例如模型输出 `H₂O`（下标）或 `３９５`（全角）等高阶排版均可正确匹配 `H2O`、`395`。

## 开发

```bash
go test ./...    # 单测 + 基于 httptest mock 的端到端测试
```

新增检查项：在对应 `internal/suites/<layer>/` 包中编写返回 `core.CheckResult` 的函数并挂入该层的 `Run`；新增打分器：在 `internal/scorer/scorer.go` 的 `Score` 中注册新 `type`。
