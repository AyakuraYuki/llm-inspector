// Package cases 内置 gpt-image-2 的参数合规性测试用例集。
// 用例口径完全依据 docs/create_image-openai-api-spec.md（OpenAI 官方接口文档）：
//   - ExpectSuccess：官方允许的参数，供应商必须成功生成图片，且返回的图片
//     尺寸/格式/张数要与请求一致；
//   - ExpectReject：官方禁止的参数，供应商必须拒绝（HTTP 400/413/422），
//     返回图片即判 FAIL（例如 4096x4096 超分生成）；
//   - ExpectObserve（probe）：官方口径未明确的参数组合，只记录实际行为，
//     不判定通过与否，默认不运行（include_probe: true 开启）。
package cases

import (
	"fmt"
	"slices"
	"strings"
)

// Expectation 表示用例的预期结果类别。
type Expectation string

const (
	ExpectSuccess Expectation = "success"
	ExpectReject  Expectation = "reject"
	ExpectObserve Expectation = "observe"
)

// Case 描述一个测试用例：请求参数与校验预期。
type Case struct {
	ID     string
	Group  string
	Note   string
	Expect Expectation
	// Params 会覆盖在基础请求（model + prompt）之上，键值原样进入请求体，
	// 因此可以构造 SDK 层面无法表达的非法参数。
	Params map[string]any

	// 以下为成功用例的附加校验项，零值表示不校验。
	WantWidth  int    // 返回图片的宽必须等于该值
	WantHeight int    // 返回图片的高必须等于该值
	WantFormat string // 返回图片的实际格式（按魔数嗅探）必须等于该值
	WantN      int    // 返回图片张数必须等于该值
	Stream     bool   // 以 SSE 流式方式发起并消费响应
}

type p = map[string]any

// ok 构造一个预期成功的用例。未显式指定时补上 size=1024x1024 和 quality=low，
// 让非尺寸/质量组的用例也能校验返回分辨率，同时控制生成成本。
func ok(id, group, note string, params p) Case {
	if _, exists := params["size"]; !exists {
		params["size"] = "1024x1024"
	}
	if _, exists := params["quality"]; !exists {
		params["quality"] = "low"
	}
	c := Case{ID: id, Group: group, Expect: ExpectSuccess, Note: note, Params: params}
	if params["size"] == "1024x1024" {
		c.WantWidth, c.WantHeight = 1024, 1024
	}
	return c
}

func bad(id, group, note string, params p) Case {
	return Case{ID: id, Group: group, Expect: ExpectReject, Note: note, Params: params}
}

func probe(id, group, note string, params p) Case {
	return Case{ID: id, Group: group, Expect: ExpectObserve, Note: note, Params: params}
}

// sizeOK 构造一个合法尺寸用例，低质量生成并校验返回分辨率。
func sizeOK(id string, w, h int, note string) Case {
	return Case{
		ID: id, Group: "size", Expect: ExpectSuccess, Note: note,
		Params:    p{"size": fmt.Sprintf("%dx%d", w, h), "quality": "low"},
		WantWidth: w, WantHeight: h,
	}
}

func sizeBad(id, size, note string) Case {
	return Case{ID: id, Group: "size-invalid", Expect: ExpectReject, Note: note, Params: p{"size": size}}
}

// Build 返回全量用例，顺序即报告展示顺序。
func Build() []Case {
	var cs []Case

	// ---- size：官方允许的尺寸 ----
	cs = append(cs,
		Case{ID: "size-auto", Group: "size", Expect: ExpectSuccess, Note: "自动选择尺寸",
			Params: p{"size": "auto", "quality": "low"}},
		sizeOK("size-1024x1024", 1024, 1024, "官方标准尺寸 1:1"),
		sizeOK("size-1536x1024", 1536, 1024, "官方标准尺寸 3:2"),
		sizeOK("size-1024x1536", 1024, 1536, "官方标准尺寸 2:3"),
		sizeOK("size-1536x864", 1536, 864, "任意分辨率（16:9，宽高均可被 16 整除）"),
		sizeOK("size-1536x512", 1536, 512, "宽高比上边界 3:1"),
		sizeOK("size-512x1536", 512, 1536, "宽高比下边界 1:3"),
		sizeOK("size-2560x1440", 2560, 1440, "非实验性分辨率上边界"),
		sizeOK("size-3840x2160", 3840, 2160, "官方最大分辨率（实验性）"),
	)

	// ---- size-invalid：官方禁止的尺寸 ----
	cs = append(cs,
		sizeBad("size-4096x4096", "4096x4096", "超过官方最大分辨率 3840x2160（供应商超分探测）"),
		sizeBad("size-4096x2304", "4096x2304", "16:9 但超过官方最大分辨率"),
		sizeBad("size-1000x1000", "1000x1000", "宽高不能被 16 整除"),
		sizeBad("size-1048x1024", "1048x1024", "宽不能被 16 整除"),
		sizeBad("size-2048x512", "2048x512", "宽高比 4:1，超出 3:1 上限"),
		sizeBad("size-512x2048", "512x2048", "宽高比 1:4，超出 1:3 下限"),
		sizeBad("size-0x0", "0x0", "零尺寸"),
		sizeBad("size-garbage", "banana", "非法尺寸字符串"),
	)

	// ---- quality ----
	cs = append(cs,
		ok("quality-low", "quality", "GPT image 模型支持", p{"quality": "low"}),
		ok("quality-medium", "quality", "GPT image 模型支持", p{"quality": "medium"}),
		ok("quality-high", "quality", "GPT image 模型支持", p{"quality": "high"}),
		ok("quality-auto", "quality", "默认值", p{"quality": "auto"}),
		bad("quality-xhigh", "quality-invalid", "仅 gpt-image-2.5 系列支持", p{"quality": "xhigh"}),
		bad("quality-max", "quality-invalid", "仅 gpt-image-2.5 系列支持", p{"quality": "max"}),
		bad("quality-hd", "quality-invalid", "仅 dall-e-3 支持", p{"quality": "hd"}),
		bad("quality-standard", "quality-invalid", "仅 dall-e-2/3 支持", p{"quality": "standard"}),
		bad("quality-garbage", "quality-invalid", "非法枚举值", p{"quality": "ultra"}),
	)

	// ---- background ----
	bgTransparent := ok("bg-transparent-png", "background", "透明背景要求 png/webp 输出", p{"background": "transparent", "output_format": "png"})
	bgTransparent.WantFormat = "png"
	cs = append(cs,
		bgTransparent,
		ok("bg-opaque", "background", "官方支持", p{"background": "opaque"}),
		ok("bg-auto", "background", "默认值", p{"background": "auto"}),
		bad("bg-invalid", "background", "非法枚举值", p{"background": "blurple"}),
		bad("bg-transparent-jpeg", "background", "官方要求 transparent 搭配 png/webp，jpeg 不含透明通道", p{"background": "transparent", "output_format": "jpeg"}),
	)

	// ---- output_format ----
	for _, f := range []string{"png", "jpeg", "webp"} {
		c := ok("fmt-"+f, "output_format", "官方支持的输出格式", p{"output_format": f})
		c.WantFormat = f
		cs = append(cs, c)
	}
	cs = append(cs,
		bad("fmt-gif", "output_format", "非法输出格式", p{"output_format": "gif"}),
		bad("fmt-bmp", "output_format", "非法输出格式", p{"output_format": "bmp"}),
	)

	// ---- output_compression ----
	compOK := func(id, format string, level int) Case {
		c := ok(id, "output_compression", "compression 仅支持 jpeg/webp，取值 0-100", p{"output_format": format, "output_compression": level})
		c.WantFormat = format
		return c
	}
	cs = append(cs,
		compOK("comp-jpeg-0", "jpeg", 0),
		compOK("comp-jpeg-50", "jpeg", 50),
		compOK("comp-webp-100", "webp", 100),
		bad("comp-over-100", "output_compression", "超出 0-100 取值范围", p{"output_format": "jpeg", "output_compression": 101}),
		bad("comp-negative", "output_compression", "超出 0-100 取值范围", p{"output_format": "jpeg", "output_compression": -1}),
		probe("comp-png-50", "output_compression", "官方口径 compression 仅支持 jpeg/webp，png 下行为未明确", p{"output_format": "png", "output_compression": 50}),
	)

	// ---- n ----
	n2 := ok("n-2", "n", "一次生成多张，校验返回张数", p{"n": 2})
	n2.WantN = 2
	cs = append(cs,
		n2,
		bad("n-0", "n", "n 取值范围为 1-10", p{"n": 0}),
		bad("n-11", "n", "n 取值范围为 1-10", p{"n": 11}),
	)

	// ---- moderation ----
	cs = append(cs,
		ok("mod-low", "moderation", "官方支持", p{"moderation": "low"}),
		ok("mod-auto", "moderation", "默认值", p{"moderation": "auto"}),
		bad("mod-invalid", "moderation", "非法枚举值（只有 low/auto）", p{"moderation": "high"}),
	)

	// ---- response_format / style（dall-e 专属参数）----
	cs = append(cs,
		bad("respfmt-url", "response_format", "GPT image 模型不支持 response_format，且无法以 url 返回", p{"response_format": "url"}),
		probe("respfmt-b64", "response_format", "官方口径不支持该参数，但 b64_json 与默认行为一致，可能被忽略", p{"response_format": "b64_json"}),
		probe("style-vivid", "style", "style 仅 dall-e-3 支持，其他模型可能拒绝或忽略", p{"style": "vivid"}),
	)

	// ---- stream / partial_images ----
	streamOK := func(id, note string, params p) Case {
		c := ok(id, "stream", note, params)
		c.Stream = true
		return c
	}
	partial4 := bad("partial-4", "stream", "partial_images 取值范围为 0-3", p{"stream": true, "partial_images": 4})
	partial4.Stream = true
	partialNeg := bad("partial-negative", "stream", "partial_images 取值范围为 0-3", p{"stream": true, "partial_images": -1})
	partialNeg.Stream = true
	cs = append(cs,
		streamOK("stream-basic", "流式生成", p{"stream": true}),
		streamOK("stream-partial-2", "流式生成并请求 2 个 partial 图", p{"stream": true, "partial_images": 2}),
		partial4,
		partialNeg,
		probe("partial-no-stream", "stream", "未开启 stream 时传 partial_images，官方行为未明确", p{"partial_images": 2}),
	)

	// ---- prompt ----
	// 23 字符 × 1392 = 32016 字符，恰好超过 GPT image 模型 32000 字符上限。
	longPrompt := strings.Repeat("Describe a red circle. ", 1392)
	cs = append(cs,
		bad("prompt-too-long", "prompt", "超过 GPT image 模型 32000 字符上限", p{"prompt": longPrompt, "size": "1024x1024"}),
	)

	return cs
}

// Filter 按 only/skip（用例 ID 或分组名）筛选用例。probe 用例默认剔除，
// 仅当 includeProbe 为 true 或其 ID 被 only 显式点名时保留。
func Filter(cs []Case, only, skip []string, includeProbe bool) []Case {
	matches := func(c Case, keys []string) bool {
		return slices.ContainsFunc(keys, func(k string) bool { return k == c.ID || k == c.Group })
	}

	out := make([]Case, 0, len(cs))
	for _, c := range cs {
		if matches(c, skip) {
			continue
		}
		if len(only) > 0 && !matches(c, only) {
			continue
		}
		if c.Expect == ExpectObserve && !includeProbe && !slices.Contains(only, c.ID) {
			continue
		}
		out = append(out, c)
	}
	return out
}
