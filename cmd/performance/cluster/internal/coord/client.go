package coord

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/cluster/internal/proto"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

const resultFetchRetries = 3

// Client 是 coordinator 侧对单个 agent 的 HTTP 客户端。
// 所有调用的超时都由 ctx 控制，Client 本身不设全局超时
// （preflight 要等分钟级，progress 只等秒级，无法一刀切）。
type Client struct {
	Addr  string // host:port
	token string
	http  *http.Client
}

func NewClient(addr, token string) *Client {
	return &Client{
		Addr:  addr,
		token: token,
		http:  &http.Client{}, // 每 agent 独立连接池，互不干扰
	}
}

func (c *Client) Ping(ctx context.Context) (proto.AgentInfo, error) {
	var info proto.AgentInfo
	err := c.call(ctx, http.MethodGet, proto.PathPing, nil, &info)
	return info, err
}

func (c *Client) SessionStart(ctx context.Context, req proto.SessionStart) error {
	return c.call(ctx, http.MethodPost, proto.PathSessionStart, req, nil)
}

func (c *Client) SessionEnd(ctx context.Context) (proto.SessionEndResponse, error) {
	var resp proto.SessionEndResponse
	err := c.call(ctx, http.MethodPost, proto.PathSessionEnd, struct{}{}, &resp)
	return resp, err
}

func (c *Client) Preflight(ctx context.Context, req proto.PreflightRequest) (proto.PreflightResponse, error) {
	var resp proto.PreflightResponse
	err := c.call(ctx, http.MethodPost, proto.PathPreflight, req, &resp)
	return resp, err
}

func (c *Client) TaskStart(ctx context.Context, req proto.TaskStart) error {
	return c.call(ctx, http.MethodPost, proto.PathTaskStart, req, nil)
}

func (c *Client) TaskProgress(ctx context.Context, taskID string) (proto.TaskProgress, error) {
	var p proto.TaskProgress
	err := c.call(ctx, http.MethodGet, proto.PathTaskProgress+"?task_id="+url.QueryEscape(taskID), nil, &p)
	return p, err
}

func (c *Client) TaskCancel(ctx context.Context, taskID string) error {
	return c.call(ctx, http.MethodPost, proto.PathTaskCancel, proto.TaskCancel{TaskID: taskID}, nil)
}

// TaskResult 拉取已完成任务的完整原始结果，网络抖动时最多重试 3 次。
func (c *Client) TaskResult(ctx context.Context, taskID string) (types.BenchmarkResult, error) {
	var result types.BenchmarkResult
	var err error
	for attempt := 0; attempt < resultFetchRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(time.Second):
			}
		}
		result = types.BenchmarkResult{}
		if err = c.call(ctx, http.MethodGet, proto.PathTaskResult+"?task_id="+url.QueryEscape(taskID), nil, &result); err == nil {
			return result, nil
		}
	}
	return result, fmt.Errorf("拉取结果失败（重试 %d 次）: %w", resultFetchRetries, err)
}

// call 发起一次 JSON 调用：非 2xx 时解析统一错误体并带上 agent 地址返回。
// 响应若带 Content-Encoding: gzip（Transport 未做透明解压的场景）则手动解压。
func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("agent %s: 请求序列化失败: %w", c.Addr, err)
		}
		body = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.Addr+path, body)
	if err != nil {
		return fmt.Errorf("agent %s: %w", c.Addr, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set(proto.HeaderToken, c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("agent %s: %w", c.Addr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	reader := io.Reader(resp.Body)
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, gzErr := gzip.NewReader(resp.Body)
		if gzErr != nil {
			return fmt.Errorf("agent %s: gzip 解压失败: %w", c.Addr, gzErr)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e proto.ErrorResponse
		if json.NewDecoder(io.LimitReader(reader, 8<<10)).Decode(&e) == nil && e.Error != "" {
			return fmt.Errorf("agent %s: HTTP %d: %s", c.Addr, resp.StatusCode, e.Error)
		}
		return fmt.Errorf("agent %s: HTTP %d", c.Addr, resp.StatusCode)
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, reader)
		return nil
	}
	if err := json.NewDecoder(reader).Decode(out); err != nil {
		return fmt.Errorf("agent %s: 响应解析失败: %w", c.Addr, err)
	}
	return nil
}
