# imagespec 图片生成参数合规性测试工具

按 OpenAI 官方接口口径（[docs/create_image-openai-api-spec.md](../../docs/create_image-openai-api-spec.md)）测试 GPT image 模型
（`gpt-image-2` 及 `gpt-image-2.5-sunburst` / `gpt-image-2.5-flare` 系列）在 `/v1/images/generations` 各种请求参数下的正确性： **官方允许的参数必须成功生成图片，官方禁止的参数必须被拒绝**。

设计动机：部分供应商提供的 GPT image 模型会放宽官方参数限制，例如生成官方不允许的 `4096x4096` 超分图片。
本工具用官方口径逐项验证目标端点的参数校验行为，并保留证据。

## 判定口径

内置用例分三类（`-list` 可查看全部）：

| 预期      | 判定规则                                                                                                                                        |
|-----------|-------------------------------------------------------------------------------------------------------------------------------------------------|
| `success` | 必须 HTTP 200 且返回可解码的图片；同时校验图片**实际分辨率/格式/张数**与请求一致（按图片魔数与头部解析，不信任响应里的声明字段），不一致判 FAIL |
| `reject`  | 必须被 HTTP 400/413/422 拒绝；返回了图片即判 FAIL（例如 4096x4096 超分生成成功）                                                                |
| `observe` | probe 组：官方口径未明确的参数组合（如 `png` + `output_compression`），只记录实际行为（INFO），不参与判定，默认不运行                           |

无法判定的情况（鉴权失败、限流 429、服务端 5xx、网络异常等）记为 `INCONCLUSIVE`，不误判为通过或失败。

## 模型分族

绝大多数用例的口径在 `gpt-image-2` 与 `gpt-image-2.5-sunburst`/`gpt-image-2.5-flare` 系列之间是一致的，
只有 `quality=xhigh/max` 这两档官方仅对 2.5 系列开放。工具按配置文件中的 `model` 字段自动判断：

- `model` 命中 `gpt-image-2.5-sunburst*` 或 `gpt-image-2.5-flare*`（含 `2026-09-08` 等日期快照）：`quality-xhigh`/`quality-max` 归入 `quality` 组，预期 `success`
- 其他模型（如 `gpt-image-2`、`gpt-image-2-2026-04-21`）：这两个用例归入 `quality-invalid` 组，预期 `reject`

`-list` 会按当前配置的 `model` 实时展示这两个用例落在哪个分组、预期结果是什么，运行前可用它确认口径是否符合预期。

## 用例覆盖

- `size`：标准尺寸、任意 16 倍数分辨率、宽高比 3:1 / 1:3 边界、实验性分辨率上限 3840x2160；以及超分（4096x4096）、非 16 倍数、超宽高比、非法字符串等非法值
- `quality`：`low/medium/high/auto` 合法；`xhigh/max` 是否合法取决于模型分族（见上）；`hd/standard`（dall-e 专属）始终非法
- `background`：`transparent/opaque/auto` 合法；非法枚举值、`transparent` + `jpeg` 组合非法
- `output_format` / `output_compression`：`png/jpeg/webp` 与 0-100 压缩率合法；`gif/bmp`、越界压缩率非法
- `n` / `moderation` / `response_format` / `style` / `stream` / `partial_images` / 超长 `prompt`（>32000 字符）

## 速度指标

工具在验证参数正确性的同一次请求里，顺带把耗时拆解成几个跟文本生成 TTFT/TPS 对应的指标（只在确实拿到图片时才有）。
这些都是 **单次采样**，不是压测意义上的百分位统计——要看统计分布，应该用 `performance` 工具或者自己重复跑。

| 指标                              | 含义                                                                        | 对应文本生成的什么          |
|-----------------------------------|-----------------------------------------------------------------------------|-----------------------------|
| `ms_per_image`                    | 总耗时 ÷ 实际返回的图片张数，抹平 `n` 的影响                                | -                           |
| `megapixels`                      | 单张图片的实际像素数（宽×高/1e6）                                           | -                           |
| `ms_per_megapixel`                | `ms_per_image` ÷ `megapixels`，按输出规模归一化后的生成速率                 | TPS（按输出量归一化的速率） |
| `ttfpi_ms`                        | 首个 `partial_image` 事件到达耗时（仅流式）                                 | TTFT                        |
| `partial_intervals_ms`            | 相邻 `partial_image` 事件之间的耗时（仅流式）                               | TPOT                        |
| `completed_after_last_partial_ms` | 最后一个 partial 到 `completed` 事件的耗时（仅流式）                        | -                           |
| `likely_buffered`                 | 首个 partial 几乎与总耗时同时到达时置位，怀疑是攒完整图后一次性下发的假流式 | -                           |

运行结束后控制台会打印一张「速度指标」表（按用例列出上述字段），JSON 报告里每个用例的 `speed` 字段包含完整数据。

## 使用

```bash
cp cmd/imagespec/configs/config.example.yaml imagespec.yaml   # 修改 base_url / api_key / model
go run ./cmd/imagespec -config imagespec.yaml

# 只列出用例，不发起请求
go run ./cmd/imagespec -config imagespec.yaml -list
```

常用配置（完整示例见 [configs/config.example.yaml](configs/config.example.yaml)）：

- `model`：被测模型，决定 `quality-xhigh`/`quality-max` 用例的预期结果（见上「模型分族」）
- `only` / `skip`：按用例 id 或分组名筛选，例如只测尺寸限制 `only: [size, size-invalid]`
- `include_probe`：运行 probe 组观察用例
- `save_images`：把返回的图片保存到目录——「期望拒绝但生成成功」的证据图也会保存
- `output`：JSON 报告路径（含每个用例的请求体、响应片段、图片实测元信息、速度指标）

运行结束输出分组明细、速度指标表与汇总，存在 FAIL 用例时进程以退出码 1 结束，可直接接入脚本/CI。

除 JSON 报告外，运行期间打印到控制台的所有内容（表头、逐用例进度、报告明细、速度指标表）都会带时间戳同步写入一份运行日志
（`<output 去掉扩展名>.txt`，存放在当前工作目录），与仓库里其他工具（`benchmark`/`evaluation`）使用 `internal/logger` 的方式一致。
例如 `output: imagespec.json` 时，日志文件是 `imagespec.txt`。`-list` 模式只是预览用例、不发起请求，不产生日志文件。

## 注意事项

- 预期成功的用例约 27-29 个（视模型分族而定），包含 2560x1440、3840x2160 等大图，会产生真实的生成费用；可先用 `only` 聚焦关心的分组
- `concurrency` 默认 2，注意目标供应商的限流策略；单用例超时默认 5m
- 工具面向官方文档中已明确定义的 GPT image 参数口径设计，暂不覆盖 `gpt-image-2.5` 系列未来可能新增的、文档尚未记录的能力
