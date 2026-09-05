package agentd

import (
	"compress/gzip"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/cluster/internal/proto"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/internal/errlog"
	"github.com/AyakuraYuki/llm-inspector/internal/logger"
)

// Server 是 agent 守护进程的核心：持有会话与单任务互斥状态。
// 同一时刻至多服务一个 coordinator 的一个任务，简单可靠——
// 压测 agent 本来就该独占机器资源，多任务并行只会互相污染指标。
type Server struct {
	token string

	mu   sync.Mutex
	sess string // 当前 run 的 RunID，空表示无会话
	task *runningTask
}

func NewServer(token string) *Server {
	return &Server{token: token}
}

// Run 启动 HTTP 服务并阻塞至 ctx 取消，随后优雅关停（先取消运行中任务）。
func (s *Server) Run(ctx context.Context, listen string) error {
	srv := &http.Server{Addr: listen, Handler: s.handler()}

	errCh := make(chan error, 1)
	go func() {
		logger.Printf("[agent] 监听 %s（协议版本 %d）", listen, proto.Version)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	s.mu.Lock()
	if s.task != nil && !s.task.done.Load() {
		s.task.cancel()
	}
	s.mu.Unlock()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// Handler 返回完整路由（含鉴权中间件），供进程内集成测试挂到 httptest.Server。
func (s *Server) Handler() http.Handler { return s.handler() }

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+proto.PathPing, s.handlePing)
	mux.HandleFunc("POST "+proto.PathSessionStart, s.handleSessionStart)
	mux.HandleFunc("POST "+proto.PathSessionEnd, s.handleSessionEnd)
	mux.HandleFunc("POST "+proto.PathPreflight, s.handlePreflight)
	mux.HandleFunc("POST "+proto.PathTaskStart, s.handleTaskStart)
	mux.HandleFunc("GET "+proto.PathTaskProgress, s.handleTaskProgress)
	mux.HandleFunc("POST "+proto.PathTaskCancel, s.handleTaskCancel)
	mux.HandleFunc("GET "+proto.PathTaskResult, s.handleTaskResult)
	return s.auth(mux)
}

// auth 校验可选的共享密钥。常量时间比较防时序侧信道（内网纵深防御，成本为零）。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := r.Header.Get(proto.HeaderToken)
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				writeErr(w, http.StatusUnauthorized, "invalid or missing "+proto.HeaderToken)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	busy := s.task != nil && !s.task.done.Load()
	taskID := ""
	if busy {
		taskID = s.task.id
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, proto.AgentInfo{
		Proto:  proto.Version,
		GOOS:   runtime.GOOS,
		NumCPU: runtime.NumCPU(),
		Busy:   busy,
		TaskID: taskID,
	})
}

func (s *Server) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	var req proto.SessionStart
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Proto != proto.Version {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("协议版本不匹配：coordinator=%d agent=%d", req.Proto, proto.Version))
		return
	}
	if req.MaxLocalConcurrency <= 0 {
		writeErr(w, http.StatusBadRequest, "MaxLocalConcurrency 必须为正整数")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task != nil && !s.task.done.Load() {
		writeErr(w, http.StatusConflict, "agent 忙碌：任务 "+s.task.id+" 仍在运行")
		return
	}
	s.sess = req.RunID
	s.task = nil

	// 连接池按本 agent 全 run 的最大分片并发一次性配置：
	// 逐档重建会丢掉 warmup 阶段建好的连接，且存在在途请求竞态。
	runner.ConfigureClient(req.MaxLocalConcurrency)
	errlog.Init(fmt.Sprintf("bench-agent-%s-request-errors.jsonl", req.RunID))
	logger.Printf("[agent] 会话建立: run=%s maxLocalConcurrency=%d", req.RunID, req.MaxLocalConcurrency)

	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleSessionEnd(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	runID := s.sess
	s.sess = ""
	s.mu.Unlock()
	logger.Printf("[agent] 会话结束: run=%s", runID)

	writeJSON(w, http.StatusOK, proto.SessionEndResponse{
		ErrlogCount: int64(errlog.Count()),
		ErrlogPath:  errlog.Path(),
	})
}

// handlePreflight 串行预检全部模型。每个 agent 各自跑一遍，
// 验证的是"这台机器到上游"的网络路径，任何一台不通都应该在开测前暴露。
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	var req proto.PreflightRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	s.mu.Lock()
	if s.task != nil && !s.task.done.Load() {
		s.mu.Unlock()
		writeErr(w, http.StatusConflict, "agent 忙碌：任务 "+s.task.id+" 仍在运行")
		return
	}
	s.mu.Unlock()

	resp := proto.PreflightResponse{Results: make([]proto.PreflightResult, 0, len(req.Bench.Models))}
	for _, model := range req.Bench.Models {
		if r.Context().Err() != nil {
			writeErr(w, http.StatusBadRequest, "preflight 被取消")
			return
		}
		m := runner.PreflightModel(r.Context(), req.Bench, model)
		resp.Results = append(resp.Results, proto.PreflightResult{
			ModelName:  model.Name,
			Provider:   model.Provider,
			TokenGroup: model.TokenGroup,
			Metric:     m,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleTaskStart(w http.ResponseWriter, r *http.Request) {
	var req proto.TaskStart
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TaskID == "" || req.Concurrency <= 0 {
		writeErr(w, http.StatusBadRequest, "TaskID 与 Concurrency 为必填")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess == "" || s.sess != req.RunID {
		writeErr(w, http.StatusBadRequest, "会话不存在或 RunID 不匹配，请先调用 session/start")
		return
	}
	if s.task != nil && !s.task.done.Load() {
		writeErr(w, http.StatusConflict, "agent 忙碌：任务 "+s.task.id+" 仍在运行")
		return
	}
	s.task = newRunningTask(req)
	logger.Printf("[agent] 任务启动: %s kind=%s model=%s concurrency=%d ramp=%s duration=%s",
		req.TaskID, req.Kind, req.Model.Name, req.Concurrency, req.Ramp, req.Bench.Duration)

	writeJSON(w, http.StatusAccepted, struct{}{})
}

// currentTask 按 task_id 取当前任务；不匹配时写好错误响应并返回 nil。
func (s *Server) currentTask(w http.ResponseWriter, taskID string) *runningTask {
	s.mu.Lock()
	task := s.task
	s.mu.Unlock()
	if task == nil || task.id != taskID {
		writeErr(w, http.StatusNotFound, "任务不存在: "+taskID)
		return nil
	}
	return task
}

func (s *Server) handleTaskProgress(w http.ResponseWriter, r *http.Request) {
	task := s.currentTask(w, r.URL.Query().Get("task_id"))
	if task == nil {
		return
	}
	writeJSON(w, http.StatusOK, task.progress())
}

func (s *Server) handleTaskCancel(w http.ResponseWriter, r *http.Request) {
	var req proto.TaskCancel
	if !decodeJSON(w, r, &req) {
		return
	}
	task := s.currentTask(w, req.TaskID)
	if task == nil {
		return
	}
	task.cancel()
	logger.Printf("[agent] 任务取消: %s", req.TaskID)
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) handleTaskResult(w http.ResponseWriter, r *http.Request) {
	task := s.currentTask(w, r.URL.Query().Get("task_id"))
	if task == nil {
		return
	}
	result, runErr, done := task.takeResult()
	if !done {
		writeErr(w, http.StatusConflict, "任务尚未完成: "+task.id)
		return
	}
	if runErr != nil {
		writeErr(w, http.StatusInternalServerError, runErr.Error())
		return
	}

	// 原始样本可能有几十 MB（高并发长档位），gzip 后内网传输毫无压力。
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Encoding", "gzip")
	gz := gzip.NewWriter(w)
	defer func() { _ = gz.Close() }()
	if err := json.NewEncoder(gz).Encode(result); err != nil {
		logger.Printf("[agent] 结果序列化失败: %s: %v", task.id, err)
	}
}

// ── JSON helpers ─────────────────────────────────────────────────────────────

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, proto.ErrorResponse{Error: msg})
}
