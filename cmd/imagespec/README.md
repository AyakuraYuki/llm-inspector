# imagespec 图片生成参数合规性测试工具

按 OpenAI 官方接口口径（[docs/create_image-openai-api-spec.md](../../docs/create_image-openai-api-spec.md)）测试 `gpt-image-2`
在 `/v1/images/generations` 各种请求参数下的正确性：**官方允许的参数必须成功生成图片，官方禁止的参数必须被拒绝**。

设计动机：部分供应商提供的 `gpt-image-2` 会放宽官方参数限制，例如生成官方不允许的 `4096x4096` 超分图片。
本工具用官方口径逐项验证目标端点的参数校验行为，并保留证据。

## 判定口径

内置用例分三类（`-list` 可查看全部）：

| 预期        | 判定规则                                                                                                                                     |
|-------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `success`   | 必须 HTTP 200 且返回可解码的图片；同时校验图片**实际分辨率/格式/张数**与请求一致（按图片魔数与头部解析，不信任响应里的声明字段），不一致判 FAIL |
| `reject`    | 必须被 HTTP 400/413/422 拒绝；返回了图片即判 FAIL（例如 4096x4096 超分生成成功）                                                               |
| `observe`   | probe 组：官方口径未明确的参数组合（如 `png` + `output_compression`），只记录实际行为（INFO），不参与判定，默认不运行                          |

无法判定的情况（鉴权失败、限流 429、服务端 5xx、网络异常等）记为 `INCONCLUSIVE`，不误判为通过或失败。

## 用例覆盖

- `size`：标准尺寸、任意 16 倍数分辨率、宽高比 3:1 / 1:3 边界、实验性分辨率上限 3840x2160；以及超分（4096x4096）、非 16 倍数、超宽高比、非法字符串等非法值
- `quality`：`low/medium/high/auto` 合法；`xhigh/max`（gpt-image-2.5 专属）、`hd/standard`（dall-e 专属）非法
- `background`：`transparent/opaque/auto` 合法；非法枚举值、`transparent` + `jpeg` 组合非法
- `output_format` / `output_compression`：`png/jpeg/webp` 与 0-100 压缩率合法；`gif/bmp`、越界压缩率非法
- `n` / `moderation` / `response_format` / `style` / `stream` / `partial_images` / 超长 `prompt`（>32000 字符）

## 使用

```bash
cp cmd/imagespec/configs/config.example.yaml imagespec.yaml   # 修改 base_url / api_key
go run ./cmd/imagespec -config imagespec.yaml

# 只列出用例，不发起请求
go run ./cmd/imagespec -config imagespec.yaml -list
```

常用配置（完整示例见 [configs/config.example.yaml](configs/config.example.yaml)）：

- `only` / `skip`：按用例 id 或分组名筛选，例如只测尺寸限制 `only: [size, size-invalid]`
- `include_probe`：运行 probe 组观察用例
- `save_images`：把返回的图片保存到目录——「期望拒绝但生成成功」的证据图也会保存
- `output`：JSON 报告路径（含每个用例的请求体、响应片段、图片实测元信息）

运行结束输出分组明细与汇总，存在 FAIL 用例时进程以退出码 1 结束，可直接接入脚本/CI。

## 注意事项

- 预期成功的用例约 27 个，包含 2560x1440、3840x2160 等大图，会产生真实的生成费用；可先用 `only` 聚焦关心的分组
- `concurrency` 默认 2，注意目标供应商的限流策略；单用例超时默认 5m
- 工具面向 `gpt-image-2`（含 `gpt-image-2-2026-04-21` 快照）的官方口径设计，不覆盖 `gpt-image-2.5` 系列的新增能力（`xhigh/max` 质量档在本工具中按非法处理）
