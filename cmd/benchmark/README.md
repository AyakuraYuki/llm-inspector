# LLM Benchmark Tool

这是一个基于 OpenAI-Compatible API 的 LLM 性能测试工具，用于评估模型在解决高阶问题时的性能指标。

## 功能特性

- 并发执行多个问题测试
- 测量以下性能指标：
  - **TTFT** (Time To First Token): 首字符延迟
  - **TPS** (Tokens Per Second): 每秒生成 token 数
  - **TPM** (Tokens Per Minute): 每分钟生成 token 数
  - **总用时**: 完整响应时间
- 支持流式响应
- **答案验证**: 自动从 `\boxed{}` 中提取模型答案并与标准答案比较
- **准确率统计**: 计算模型在有标准答案的问题上的正确率
- 结果输出为 JSON 格式
- 统计信息汇总

## 配置参数

程序使用以下默认配置：
- `max_tokens`: 65536
- `max_workers`: 2（并发数）
- `thinking_style`: "none"

## 环境变量

运行前需要设置以下环境变量：

```bash
# 必需
export OPENAI_API_KEY="your-api-key"

# 可选（默认值如下）
export OPENAI_BASE_URL="https://api.openai.com/v1"  # API 基础 URL
export MODEL_NAME="gpt-4"                            # 模型名称
```

## 安装依赖

```bash
cd cmd/benchmark
go mod download
```

## 运行程序

```bash
cd cmd/benchmark
go run main.go
```

## 输出

程序会生成三种输出：

### 1. 控制台输出

实时显示测试进度和每个问题的结果：

```
Loaded 33 questions
Config: max_tokens=65536, max_workers=2, thinking_style=none
Model: gpt-4, Base URL: https://api.openai.com/v1

[1/33] Processing question 1...
[1/33] Completed: TTFT=250ms, Total=15000ms, Tokens=1500, TPS=100.00 ✓ Correct
[2/33] Processing question 2...
[2/33] Completed: TTFT=230ms, Total=14200ms, Tokens=1420, TPS=100.00 ✗ Wrong
[3/33] Processing question 3...
[3/33] Completed: TTFT=200ms, Total=5000ms, Tokens=50, TPS=10.00 ✗ Wrong [⚠ finish_reason=null]
...

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

### 2. JSON 结果文件

生成一个时间戳命名的 JSON 文件（例如 `benchmark_results_20260731_143025.json`），包含详细结果：

```json
[
  {
    "question_index": 0,
    "question": "问题文本...",
    "expected_answer": "70",
    "model_answer": "模型的完整回答...",
    "extracted_answer": "70",
    "is_correct": true,
    "ttft_ms": 250,
    "total_time_ms": 15000,
    "tokens_used": 1500,
    "tps": 100.0,
    "tpm": 6000.0,
    "error": ""
  },
  ...
]
```

字段说明：
- `question_index`: 问题序号
- `question`: 问题文本
- `expected_answer`: 标准答案（如果提供）
- `model_answer`: 模型的完整回答
- `extracted_answer`: 从 `\boxed{}` 中提取的答案
- `is_correct`: 答案是否正确（仅当有标准答案时）
- `finish_reason`: 响应结束原因
  - `stop`: 正常结束
  - `length`: 达到 token 限制被截断
  - `null`: 异常终止（可能是网络中断或内部错误）
- `ttft_ms`: 首字符延迟（毫秒）
- `total_time_ms`: 总用时（毫秒）
- `tokens_used`: 生成的 token 数
- `tps`: 每秒 token 数
- `tpm`: 每分钟 token 数
- `error`: 错误信息（如果有）

### 3. 单独问题报告

为每个问题生成一个独立的文本报告文件，保存在 `reports_TIMESTAMP/` 目录中：

```
reports_20260731_143025/
├── question_001.txt
├── question_002.txt
├── question_003.txt
...
```

每个报告文件包含：
- 问题原文
- 标准答案（如果有）
- 模型完整响应
- 提取的答案
- 验证结果（正确/错误）
- 详细的性能指标（TTFT、总用时、TPS、TPM）
- Finish Reason 及警告信息

**报告示例：**

```
================================================================================
QUESTION #3 BENCHMARK REPORT
================================================================================

QUESTION:
--------------------------------------------------------------------------------
An isosceles trapezoid has an inscribed circle tangent to each of its four...
Put your final answer within a \boxed{}.

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

这些单独的报告文件便于：
- 快速查看单个问题的完整测试过程
- 追溯测试细节和问题诊断
- 分享特定问题的测试结果
- 对比不同模型在同一问题上的表现

`finish_reason` 可以帮助你区分失败的原因：
- **答案错误但响应正常**：`finish_reason=stop`，模型正常完成推理但给出了错误答案
- **响应被截断**：`finish_reason=length`，达到了 `max_tokens` 限制
- **异常终止**：`finish_reason=null`，响应提前终止（网络问题、API 错误等），此时 `extracted_answer` 通常为空

## 注意事项

1. **Token 计数**: 当前实现通过流式响应的 chunk 数量来近似估算 token 数。如果 API 返回 usage 信息，可以修改代码使用精确值。

2. **并发控制**: `max_workers=2` 意味着同时最多有 2 个问题在测试。可以根据 API 限制调整此值。

3. **超时处理**: 程序会捕获并记录错误，但没有设置超时。如果某些问题响应时间过长，可以添加 context 超时。

4. **API 兼容性**: 本工具使用 OpenAI SDK，支持所有 OpenAI-compatible API（如 Azure OpenAI、本地部署的模型等）。

## 问题来源

原文件中的 30 道竞赛数学题全部可对应到 2025 AIME I / 2025 AIME II。

题源答案表：

- [2025 AIME I 答案表（MAA 许可转载页面）](https://live.poshenloh.com/past-contests/aime/2025I/answers)
- [2025 AIME II 答案表（MAA 许可转载页面）](https://live.poshenloh.com/past-contests/aime/2025II/answers)

两页均注明题目经 Mathematical Association of America (MAA) 官方许可使用，并提供可打印试卷及逐题页面。

完整机器可读映射见 [questions_answer_key.json](questions_answer_key.json)。

另有两题外部问题，一道是开放式问题，另一道是基础数学加法算术题。

## 问题文件格式

`questions.json` 应该是一个对象数组，每个对象包含 `question` 和 `answer` 字段：

```json
[
  {
    "question": "问题文本...",
    "answer": "标准答案"
  },
  {
    "question": "另一个问题...",
    "answer": null
  }
]
```

- `question`: 问题文本（必需）
- `answer`: 标准答案（可选，如果为 `null` 则不验证答案）

对于要求在 `\boxed{}` 中给出答案的数学题，程序会自动从模型响应中提取答案并与标准答案比较。

## 故障排查

### 错误: "OPENAI_API_KEY environment variable is required"
确保设置了 `OPENAI_API_KEY` 环境变量。

### 错误: "Failed to load questions"
确保 `questions.json` 文件存在且格式正确。

### 所有请求都失败
检查 `OPENAI_BASE_URL` 和 `MODEL_NAME` 是否正确配置。

## 扩展建议

1. 添加命令行参数支持，允许动态配置 `max_tokens`、`max_workers` 等
2. 支持从 API 响应中获取精确的 token usage 信息
3. 添加超时控制
4. 支持断点续传（保存进度，失败后继续）
5. 添加更详细的错误日志
6. 支持多种输出格式（CSV、Excel 等）
