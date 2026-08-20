# performance 压测工具

对 OpenAI 兼容生态多协议端点（OpenAI / Anthropic / Gemini / Responses / 图片生成 / baseline）发起并发压测，输出 TTFT/TPOT/TPS/TPM/QPS
等分位数与吞吐指标。完整配置示例见 [configs/config.example.yaml](configs/config.example.yaml)。

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
