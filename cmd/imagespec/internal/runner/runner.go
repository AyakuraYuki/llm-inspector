// Package runner 逐用例向 /v1/images/generations 发起请求，并根据用例预期
// 与实际响应（HTTP 状态、图片数据的真实尺寸/格式/张数）给出判定。
package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/cases"
	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/imagemeta"
)

// Verdict 是单个用例的判定结果。
type Verdict string

const (
	// VerdictPass 表示行为符合官方口径（合法参数成功生成 / 非法参数被拒绝）。
	VerdictPass Verdict = "PASS"
	// VerdictFail 表示行为违背官方口径（合法参数被拒 / 非法参数生成成功 /
	// 返回图片与请求参数不一致）。
	VerdictFail Verdict = "FAIL"
	// VerdictInconclusive 表示无法判定（限流、鉴权失败、服务端错误、网络异常等）。
	VerdictInconclusive Verdict = "INCONCLUSIVE"
	// VerdictInfo 是 probe 用例的观察记录，不参与通过与否统计。
	VerdictInfo Verdict = "INFO"
)

// ImageInfo 记录一张返回图片的实际元信息。
type ImageInfo struct {
	Format string `json:"format"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Bytes  int    `json:"bytes"`
	ViaURL bool   `json:"via_url,omitempty"` // 供应商以 url 而非 b64_json 返回（偏离 GPT image 官方口径）
}

// Result 是单个用例的完整执行结果。
type Result struct {
	CaseID          string      `json:"case_id"`
	Group           string      `json:"group"`
	Note            string      `json:"note,omitempty"`
	Expect          string      `json:"expect"`
	Verdict         Verdict     `json:"verdict"`
	Detail          string      `json:"detail"`
	HTTPStatus      int         `json:"http_status,omitempty"`
	LatencyMS       int64       `json:"latency_ms"`
	RequestBody     string      `json:"request_body"`
	ResponseSnippet string      `json:"response_snippet,omitempty"`
	Images          []ImageInfo `json:"images,omitempty"`
	SavedFiles      []string    `json:"saved_files,omitempty"`
}

const (
	maxBodyBytes    = 512 << 20 // 同步响应体读取上限（n=2 的 4K PNG base64 可达数十 MB）
	urlFetchTimeout = 2 * time.Minute
)

// Run 以固定大小的 worker 池并发执行用例，返回与 cs 顺序一致的结果。
// ctx 取消后不再派发新用例，在途请求随 ctx 收敛，未执行的用例标记为已取消。
func Run(ctx context.Context, cfg *config.Config, cs []cases.Case) []Result {
	client := &http.Client{Transport: &http.Transport{
		MaxIdleConnsPerHost: cfg.Concurrency,
		ForceAttemptHTTP2:   true,
	}}

	results := make([]Result, len(cs))
	workers := min(cfg.Concurrency, len(cs))

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		done int
	)
	idxCh := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idxCh {
				r := runCase(ctx, client, cfg, cs[i])
				results[i] = r
				mu.Lock()
				done++
				fmt.Printf("[%d/%d] %-12s %-22s %s\n", done, len(cs), r.Verdict, r.CaseID, r.Detail)
				mu.Unlock()
			}
		}()
	}

feed:
	for i := range cs {
		select {
		case <-ctx.Done():
			break feed
		case idxCh <- i:
		}
	}
	close(idxCh)
	wg.Wait()

	for i := range results {
		if results[i].CaseID == "" {
			results[i] = Result{
				CaseID: cs[i].ID, Group: cs[i].Group, Note: cs[i].Note,
				Expect: string(cs[i].Expect), Verdict: VerdictInconclusive,
				Detail: "已取消（未执行）",
			}
		}
	}
	return results
}

// outcome 汇总一次响应中与判定相关的信息（同步 JSON 与 SSE 流共用）。
type outcome struct {
	apiError      string   // 错误 JSON 或 SSE error 事件中的错误信息
	b64s          []string // data[].b64_json 或 completed 事件中的图片数据
	urls          []string // data[].url（b64 缺失时的回退，供应商偏离口径）
	partialCount  int      // SSE partial_image 事件数
	completedSeen bool     // SSE 是否收到 completed 事件
	notStreamed   bool     // stream=true 但服务端返回了同步 JSON
	snippet       string   // 无法结构化解析时的响应片段
}

// errText 返回可读的错误描述，优先取结构化错误信息。
func (o *outcome) errText() string {
	if o.apiError != "" {
		return truncate(o.apiError, 300)
	}
	return truncate(o.snippet, 300)
}

// imgBlob 把图片元信息与原始字节绑定，便于保存文件。
type imgBlob struct {
	Info ImageInfo
	Raw  []byte
}

func runCase(ctx context.Context, client *http.Client, cfg *config.Config, c cases.Case) Result {
	payload := map[string]any{"model": cfg.Model, "prompt": cfg.Prompt}
	for k, v := range c.Params {
		payload[k] = v
	}
	body, _ := json.Marshal(payload)

	res := Result{
		CaseID: c.ID, Group: c.Group, Note: c.Note, Expect: string(c.Expect),
		RequestBody: truncate(string(body), 2048),
	}

	reqCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	t0 := time.Now()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.BaseURL+"/v1/images/generations", bytes.NewReader(body))
	if err != nil {
		res.Verdict, res.Detail = VerdictInconclusive, "构造请求失败: "+err.Error()
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	if c.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	resp, err := client.Do(req)
	if err != nil {
		res.LatencyMS = time.Since(t0).Milliseconds()
		res.Verdict = VerdictInconclusive
		switch {
		case errors.Is(err, context.Canceled):
			res.Detail = "已取消"
		case errors.Is(err, context.DeadlineExceeded):
			res.Detail = fmt.Sprintf("请求超时（超过 %s）", cfg.Timeout)
		default:
			res.Detail = "请求失败: " + err.Error()
		}
		return res
	}
	defer func(rc io.ReadCloser) { _ = rc.Close() }(resp.Body)
	res.HTTPStatus = resp.StatusCode

	var o outcome
	if c.Stream && resp.StatusCode == http.StatusOK && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		o = consumeStream(resp.Body)
	} else {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		o = parseSyncBody(raw)
		if c.Stream && resp.StatusCode == http.StatusOK {
			o.notStreamed = true
		}
	}
	res.LatencyMS = time.Since(t0).Milliseconds()

	// 只有 200 响应才有必要还原图片（含 url 回退下载），作为判定与证据
	var blobs []imgBlob
	if resp.StatusCode == http.StatusOK {
		blobs = materialize(ctx, client, &o)
	}
	for _, b := range blobs {
		res.Images = append(res.Images, b.Info)
	}
	res.ResponseSnippet = o.errText()

	res.Verdict, res.Detail = evaluate(c, resp.StatusCode, &o, res.Images)

	// 无论判定如何，只要拿到了图片就按需保存——“期望拒绝却生成成功”的证据图尤其重要
	if cfg.SaveImages != "" && len(blobs) > 0 {
		saved, saveErr := saveImages(cfg.SaveImages, c.ID, blobs)
		res.SavedFiles = saved
		if saveErr != nil {
			res.Detail += "（保存图片失败: " + saveErr.Error() + "）"
		}
	}
	return res
}

// evaluate 根据用例预期与实际响应给出判定与说明。
func evaluate(c cases.Case, status int, o *outcome, imgs []ImageInfo) (Verdict, string) {
	switch c.Expect {
	case cases.ExpectSuccess:
		switch {
		case status == http.StatusOK:
			if o.notStreamed && c.Stream {
				return VerdictFail, "stream=true 但服务端返回同步 JSON 而非 SSE 流"
			}
			if len(imgs) == 0 {
				if e := o.errText(); e != "" {
					return VerdictFail, "HTTP 200 但未生成图片: " + e
				}
				return VerdictFail, "HTTP 200 但响应中没有图片数据"
			}
			if c.WantN > 0 && len(imgs) != c.WantN {
				return VerdictFail, fmt.Sprintf("请求 n=%d 但返回 %d 张图片", c.WantN, len(imgs))
			}
			for _, im := range imgs {
				if im.Format == "" || im.Format == "unknown" {
					return VerdictFail, "返回的数据无法识别为图片: " + describeImages(imgs)
				}
				if c.WantFormat != "" && im.Format != c.WantFormat {
					return VerdictFail, fmt.Sprintf("请求 output_format=%s 但实际返回 %s", c.WantFormat, im.Format)
				}
				if c.WantWidth > 0 && (im.Width != c.WantWidth || im.Height != c.WantHeight) {
					return VerdictFail, fmt.Sprintf("请求尺寸 %dx%d 但实际返回 %dx%d", c.WantWidth, c.WantHeight, im.Width, im.Height)
				}
			}
			detail := "生成成功: " + describeImages(imgs)
			if c.Stream {
				detail += fmt.Sprintf("（%d 个 partial 事件）", o.partialCount)
			}
			return VerdictPass, detail
		case rejectStatus(status):
			return VerdictFail, fmt.Sprintf("官方允许的参数被拒绝 (HTTP %d): %s", status, o.errText())
		default:
			return VerdictInconclusive, inconclusiveDetail(status, o)
		}

	case cases.ExpectReject:
		switch {
		case rejectStatus(status):
			return VerdictPass, fmt.Sprintf("已被正确拒绝 (HTTP %d): %s", status, o.errText())
		case status == http.StatusOK:
			if len(imgs) > 0 {
				return VerdictFail, "期望拒绝但生成成功: " + describeImages(imgs)
			}
			return VerdictPass, "HTTP 200 但未返回图片（未实际生成）: " + o.errText()
		default:
			return VerdictInconclusive, inconclusiveDetail(status, o)
		}

	default: // cases.ExpectObserve
		switch {
		case status == http.StatusOK && len(imgs) > 0:
			return VerdictInfo, "生成成功: " + describeImages(imgs)
		case status == http.StatusOK:
			return VerdictInfo, "HTTP 200 但未返回图片: " + o.errText()
		default:
			return VerdictInfo, fmt.Sprintf("HTTP %d: %s", status, o.errText())
		}
	}
}

// rejectStatus 判断状态码是否属于“参数被正确拒绝”：400 参数校验失败、
// 413 请求体过大（超长 prompt 可能在网关层被拦）、422 语义校验失败。
func rejectStatus(code int) bool {
	return code == http.StatusBadRequest || code == http.StatusRequestEntityTooLarge || code == http.StatusUnprocessableEntity
}

// inconclusiveDetail 解释为什么该状态码无法用于判定参数校验行为。
func inconclusiveDetail(status int, o *outcome) string {
	var reason string
	switch {
	case status == http.StatusUnauthorized:
		reason = "鉴权失败 (HTTP 401)"
	case status == http.StatusPaymentRequired:
		reason = "余额/配额不足 (HTTP 402)"
	case status == http.StatusForbidden:
		reason = "访问被拒 (HTTP 403)"
	case status == http.StatusNotFound:
		reason = "接口或模型不存在 (HTTP 404)"
	case status == http.StatusTooManyRequests:
		reason = "触发限流 (HTTP 429)"
	case status >= 500:
		reason = fmt.Sprintf("服务端错误 (HTTP %d)", status)
	default:
		reason = fmt.Sprintf("非预期状态码 (HTTP %d)", status)
	}
	if msg := o.errText(); msg != "" {
		reason += ": " + msg
	}
	return reason + "，无法判定参数校验行为"
}

func describeImages(imgs []ImageInfo) string {
	parts := make([]string, 0, len(imgs))
	for _, im := range imgs {
		s := fmt.Sprintf("%dx%d %s", im.Width, im.Height, im.Format)
		if im.ViaURL {
			s += "(经 url 返回)"
		}
		parts = append(parts, s)
	}
	return fmt.Sprintf("%d 张图片 [%s]", len(imgs), strings.Join(parts, ", "))
}

// parseSyncBody 解析同步 JSON 响应，兼容标准 ImagesResponse 与常见网关错误结构。
func parseSyncBody(raw []byte) outcome {
	var o outcome
	var r struct {
		Data []struct {
			B64 string `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"` // 部分网关直接平铺错误信息
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		o.snippet = truncate(string(raw), 500)
		return o
	}
	if r.Error != nil && r.Error.Message != "" {
		o.apiError = r.Error.Message
	} else if r.Message != "" && len(r.Data) == 0 {
		o.apiError = r.Message
	}
	for _, d := range r.Data {
		switch {
		case d.B64 != "":
			o.b64s = append(o.b64s, d.B64)
		case d.URL != "":
			o.urls = append(o.urls, d.URL)
		}
	}
	if len(r.Data) == 0 && o.apiError == "" {
		o.snippet = truncate(string(raw), 500)
	}
	return o
}

// consumeStream 消费 SSE 流，收集 partial/completed/error 事件。
// 只认 completed 事件中的图片（partial 是中间态，不参与尺寸/格式校验）。
func consumeStream(r io.Reader) outcome {
	var o outcome
	sc := bufio.NewScanner(r)
	// 4K PNG 的 base64 会以单行 data: 出现，可达数十 MB
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev struct {
			Type  string `json:"type"`
			B64   string `json:"b64_json"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(payload), &ev) != nil {
			if o.snippet == "" {
				o.snippet = truncate(payload, 300)
			}
			continue
		}
		switch {
		case strings.HasSuffix(ev.Type, "partial_image"):
			o.partialCount++
		case strings.HasSuffix(ev.Type, "completed"):
			o.completedSeen = true
			if ev.B64 != "" {
				o.b64s = append(o.b64s, ev.B64)
			}
		case ev.Error != nil && ev.Error.Message != "":
			o.apiError = ev.Error.Message
		case strings.Contains(ev.Type, "error"):
			if ev.Message != "" {
				o.apiError = ev.Message
			} else {
				o.apiError = truncate(payload, 300)
			}
		}
	}
	if err := sc.Err(); err != nil && o.apiError == "" {
		o.apiError = "读取 SSE 流失败: " + err.Error()
	}
	return o
}

// materialize 把响应中的图片数据还原为字节并嗅探元信息。
// b64 解码失败或 url 下载失败时仍记录一个 unknown 条目，让判定看得到异常。
func materialize(ctx context.Context, client *http.Client, o *outcome) []imgBlob {
	var blobs []imgBlob
	for _, b64 := range o.b64s {
		s := b64
		// 兼容带 data URI 前缀的实现
		if strings.HasPrefix(s, "data:") {
			if i := strings.IndexByte(s, ','); i >= 0 {
				s = s[i+1:]
			}
		}
		raw, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			// 兼容缺 padding 的实现
			raw, err = base64.RawStdEncoding.DecodeString(s)
		}
		if err != nil {
			blobs = append(blobs, imgBlob{Info: ImageInfo{Format: "unknown"}})
			continue
		}
		blobs = append(blobs, imgBlob{Info: sniffInfo(raw), Raw: raw})
	}
	for _, u := range o.urls {
		raw, err := fetchURL(ctx, client, u)
		if err != nil {
			blobs = append(blobs, imgBlob{Info: ImageInfo{Format: "unknown", ViaURL: true}})
			continue
		}
		info := sniffInfo(raw)
		info.ViaURL = true
		blobs = append(blobs, imgBlob{Info: info, Raw: raw})
	}
	return blobs
}

func sniffInfo(raw []byte) ImageInfo {
	meta, err := imagemeta.Sniff(raw)
	if err != nil {
		return ImageInfo{Format: "unknown", Bytes: len(raw)}
	}
	return ImageInfo{Format: meta.Format, Width: meta.Width, Height: meta.Height, Bytes: len(raw)}
}

// fetchURL 下载以 url 形式返回的图片（GPT image 官方口径应返回 b64_json，
// 但部分网关会转成 url，此处回源以便完成尺寸/格式校验）。
func fetchURL(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, urlFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func(rc io.ReadCloser) { _ = rc.Close() }(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载图片失败: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
}

// saveImages 把本用例拿到的图片写入 dir，文件名为 <caseID>-<序号>.<扩展名>。
func saveImages(dir, caseID string, blobs []imgBlob) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ext := map[string]string{"png": ".png", "jpeg": ".jpg", "webp": ".webp"}
	var saved []string
	for i, b := range blobs {
		if len(b.Raw) == 0 {
			continue
		}
		e, exists := ext[b.Info.Format]
		if !exists {
			e = ".bin"
		}
		path := filepath.Join(dir, fmt.Sprintf("%s-%d%s", caseID, i+1, e))
		if err := os.WriteFile(path, b.Raw, 0o644); err != nil {
			return saved, err
		}
		saved = append(saved, path)
	}
	return saved, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
