// Package proto 定义 performance-cluster 的 coordinator↔agent 线协议：
// HTTP + JSON，agent 为 server，coordinator 单向拨号。DTO 直接复用
// cmd/performance/internal/types 的可序列化类型（time.Duration 编码为
// 纳秒整数，time.Time 编码为 RFC3339Nano，往返无损；单调时钟丢失不影响
// coordinator 侧的相对时间归一——rebase 只做同机 wall-clock 减法）。
package proto

import (
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// Version 是协议版本。coordinator 探活时校验，不匹配即中止，
// 避免新旧二进制混部时字段语义悄然错位。
const Version = 1

// HeaderToken 是可选的共享密钥鉴权头。agent 以 -token 启动时，
// 所有请求都必须携带一致的值；未配置时跳过校验。
const HeaderToken = "X-Cluster-Token"

// 端点路径。agent 注册路由与 coordinator 客户端共用同一组常量。
const (
	PathPing         = "/v1/ping"
	PathSessionStart = "/v1/session/start"
	PathSessionEnd   = "/v1/session/end"
	PathPreflight    = "/v1/preflight"
	PathTaskStart    = "/v1/task/start"
	PathTaskProgress = "/v1/task/progress"
	PathTaskCancel   = "/v1/task/cancel"
	PathTaskResult   = "/v1/task/result"
)

// AgentInfo 是 GET /v1/ping 的响应：探活 + 能力上报。
type AgentInfo struct {
	Proto  int    // 协议版本
	GOOS   string // 运行平台，仅展示用
	NumCPU int    // 逻辑核数，仅展示用
	Busy   bool   // 是否有运行中的任务
	TaskID string // Busy 时的任务 ID，方便定位卡住的 run
}

// SessionStart 是 POST /v1/session/start 的请求：为一次完整 run 建立会话。
// agent 收到后按 MaxLocalConcurrency 一次性重建连接池（避免逐档重建丢连接），
// 并初始化本机的请求错误日志。已有活跃任务时返回 409。
type SessionStart struct {
	Proto               int
	RunID               string // coordinator 生成，如 20260904T150405
	MaxLocalConcurrency int    // 本 agent 在整个 run 中的最大并发分片
}

// SessionEndResponse 是 POST /v1/session/end 的响应，
// 回传 agent 本机错误日志的位置，coordinator 收尾时逐台提示。
type SessionEndResponse struct {
	ErrlogCount int64
	ErrlogPath  string
}

// PreflightRequest 是 POST /v1/preflight 的请求。每个 agent 对全部模型
// 串行预检一次，各机自验到上游的网络路径（token 明文下发，用户已确认内网环境）。
type PreflightRequest struct {
	RunID string
	Bench types.BenchmarkConfig
}

// PreflightResponse 是 POST /v1/preflight 的响应。
type PreflightResponse struct {
	Results []PreflightResult
}

// PreflightResult 是单模型的预检结果。不回传 ModelSpec 本身，避免 token 回流。
type PreflightResult struct {
	ModelName  string
	Provider   types.Provider
	TokenGroup string
	Metric     types.RequestMetrics
}

// TaskKind 区分正式档位与预热任务。
type TaskKind string

const (
	TaskLevel  TaskKind = "level"
	TaskWarmup TaskKind = "warmup" // 结果丢弃：agent 回传时置空 Metrics 省带宽
)

// TaskStart 是 POST /v1/task/start 的请求：异步启动一个档位任务。
// agent 单任务互斥，忙碌时返回 409。
type TaskStart struct {
	RunID  string
	TaskID string // <runID>-<seq>-<agentIdx>，progress/cancel/result 用它校验对象
	Kind   TaskKind

	// Bench 的 Duration 为本档时长；EarlyStopEnabled 恒为 false
	//（早停判定统一由 coordinator 汇总全局错误率后广播 cancel）。
	Bench       types.BenchmarkConfig
	Model       types.ModelSpec // 含 token
	Concurrency int             // 本机分片并发数
	Ramp        time.Duration   // coordinator 按全局并发统一计算的错峰窗口
}

// TaskProgress 是 GET /v1/task/progress?task_id= 的响应：
// LevelCounters 瞬时快照 + 每错误类型最近一条错误信息（喂 coordinator TUI 的失败抽样）。
// 请求本身会刷新 agent 侧的孤儿看门狗。
type TaskProgress struct {
	TaskID     string
	Done       bool
	Err        string // agent 侧异常（panic recover / 配置错误），非空时该任务已失败
	N, OK      int64
	ByType     map[types.ErrorType]int64
	ErrSamples map[types.ErrorType]string
	Elapsed    time.Duration
}

// TaskCancel 是 POST /v1/task/cancel 的请求（全局早停 / 用户中止）。
type TaskCancel struct {
	TaskID string
}

// ErrorResponse 是所有端点出错时的统一响应体。
type ErrorResponse struct {
	Error string
}
