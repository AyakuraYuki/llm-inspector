// Package config 负责从 YAML 配置文件加载压测运行参数，并转换为
// types.BenchmarkConfig。配置文件是唯一的参数来源，命令行不再提供业务开关。
package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/prompts"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// PromptMode 定义文本端点的 prompt 生成方式，三者互斥。
type PromptMode string

const (
	// PromptModeText 使用固定的 prompt.text 文本。
	PromptModeText PromptMode = "text"
	// PromptModeDynamic 每次请求现场拼装目标长度的随机长文本，用于长上下文压测。
	PromptModeDynamic PromptMode = "dynamic"
	// PromptModeCodex 使用类 Codex 系统提示词加随机简短提问，模拟高相似度请求场景。
	PromptModeCodex PromptMode = "codex"
	// PromptModeDataset 从 dataset_path 指向的文本文件逐行取 prompt（每个非空行一条），
	// 对标 evalscope 的 line_by_line 数据集。
	PromptModeDataset PromptMode = "dataset"
)

// LoadMode 定义档位的负载生成方式。
type LoadMode string

const (
	// LoadModeClosed 是现有行为：固定协程数的 closed-loop，等响应才发下一个。
	LoadModeClosed LoadMode = "closed"
	// LoadModeOpen 按目标 RPS 泊松到达发送请求（不等响应），用于避免
	// Coordinated Omission——closed-loop 在过载时会自动降速，系统性低估
	// 真实流量下的排队延迟和尾延迟。
	LoadModeOpen LoadMode = "open"
)

// 各字段的默认值，与旧命令行 flag 的默认值保持一致。
const (
	defaultBaseURL        = "https://api.openai.com"
	defaultDuration       = 60 * time.Second
	defaultWarmupDuration = 10 * time.Second
	defaultCooldown       = 5 * time.Second
	defaultPromptTokens   = 2000

	defaultPromptText  = "Explain in plain English what API latency and throughput mean for a developer integrating LLM APIs. Write about 120 words. Do not use bullet points."
	defaultImagePrompt = "A cute fluffy kitten playing with a ball of yarn, soft lighting, adorable, high detail."

	// defaultThinkTime 保持历史硬编码值，改动它会让新旧报告的吞吐数字失去可比性。
	// 与 evalscope 对拍时应显式配 think_time: 0s。
	defaultThinkTime = 300 * time.Millisecond

	defaultMaxErrorRate = 0.5
	defaultMinSamples   = 20

	// defaultUsageDriftPct 是 usage 对拍的默认阈值（%）。分词器与服务端一致时
	// 可见文本的计数偏差通常只有个位数 token，10% 足以放过边界合并的正常抖动、
	// 抓住「usage 统计的是另一套东西」这类真问题。
	defaultUsageDriftPct = 10.0

	defaultPollInterval = time.Second
	defaultAgentTimeout = 10 * time.Second
)

var defaultConcurrency = []int{10, 20, 30, 40, 50, 75, 100, 120, 150}

// Config 是 YAML 配置文件的完整结构。
type Config struct {
	BaseURL        string              `yaml:"base_url"`
	Duration       time.Duration       `yaml:"duration"`
	Concurrency    []int               `yaml:"concurrency"`
	Prompt         PromptConfig        `yaml:"prompt"`
	ImagePrompt    string              `yaml:"image_prompt"`
	Warmup         *bool               `yaml:"warmup"` // 指针以区分「显式 false」与「未配置」
	WarmupDuration time.Duration       `yaml:"warmup_duration"`
	WarmupPerLevel bool                `yaml:"warmup_per_level"` // 每个并发档位前都预热（默认只在模型首档前），仅 warmup 为真时有效
	Cooldown       *time.Duration      `yaml:"cooldown"`         // 指针以区分「显式 0s（档位间不等待）」与「未配置」
	ThinkTime      *time.Duration      `yaml:"think_time"`       // 指针以区分「显式 0s（完成即发）」与「未配置（取默认 300ms）」
	Output         string              `yaml:"output"`
	NoExcel        bool                `yaml:"no_excel"`
	NoTUI          bool                `yaml:"no_tui"`
	Models         []ModelConfig       `yaml:"models"`
	Tokens         []string            `yaml:"tokens"`
	TokenGroups    map[string][]string `yaml:"token_groups"`
	EarlyStop      EarlyStopConfig     `yaml:"early_stop"`
	Cluster        *ClusterConfig      `yaml:"cluster"` // 仅 performance-cluster run 使用，单机版忽略

	// LoadMode/RequestRate 均可选，不配置时等价于现有 closed-loop 行为。
	LoadMode    LoadMode  `yaml:"load_mode"`
	RequestRate []float64 `yaml:"request_rate"`

	// OpenLoopUnbounded 为真时 open 模式不受 concurrency 在途上限约束（严格开环），
	// 仅 load_mode: open 下合法。默认 false 保留有界模式作为安全默认。
	OpenLoopUnbounded bool `yaml:"open_loop_unbounded"`

	// RequestsPerLevel 是各档位请求数上限：长度为 1 时作用于全部档位，否则必须与
	// concurrency 等长一一对应。档位在「发满」与「到达 duration」两者先到者结束。
	// 留空为纯时长制（历史行为）。
	RequestsPerLevel []int `yaml:"requests_per_level"`

	// Tokenizer 是可选的本地分词器配置，留空不启用。
	Tokenizer *TokenizerConfig `yaml:"tokenizer"`

	// SLO 达标率（goodput）判定阈值，留空（nil）不计算 goodput，不影响现有报表。
	SLO *SLOConfig `yaml:"slo"`

	// 结构化输出路径，留空不导出，不影响现有行为。
	JSONOutput string `yaml:"json_output"`
	CSVOutput  string `yaml:"csv_output"`

	// SampleOutput 是原始样本 JSONL 的导出路径（一行一条请求），留空不导出。
	// 报表里的分位数是聚合结果；留下原始样本才能在不重跑压测的前提下换口径重算。
	SampleOutput string `yaml:"sample_output"`

	// MaxOutputTokens 是各协议输出长度上限的统一取值，未配置（0）时取
	// types.DefaultMaxOutputTokens（8192），与历史固定值一致。
	MaxOutputTokens int `yaml:"max_output_tokens"`

	// ShowHistogram 为真时在终端/Excel 额外输出延迟分布直方图，默认 false 不影响现有报表。
	ShowHistogram bool `yaml:"show_histogram"`
}

// TokenizerConfig 描述本地分词器：path 指向 configs/tokenizers/<name> 这类目录。
// 启用后 dynamic prompt 用它精确收敛输入 token 数、usage 缺失时用它代替字符估算，
// 并按 usage_drift_pct 对拍服务端 usage。
type TokenizerConfig struct {
	Path          string   `yaml:"path"`
	UsageDriftPct *float64 `yaml:"usage_drift_pct"` // 指针以区分「显式 0（关闭对拍）」与「未配置（默认 10）」
}

// SLOConfig 描述 goodput 判定用的 SLO 阈值（毫秒），三项均可选，0 表示该维度不参与判定。
type SLOConfig struct {
	TTFTMs int `yaml:"ttft_ms"`
	TPOTMs int `yaml:"tpot_ms"`
	E2EMs  int `yaml:"e2e_ms"`
}

// ClusterConfig 描述分布式压测的 coordinator 侧参数（agent 列表与调度节奏）。
// 单机 performance 加载时该段合法但不使用。
type ClusterConfig struct {
	Agents       []string      `yaml:"agents"`        // agent 守护进程地址列表（host:port），必填
	AuthToken    string        `yaml:"auth_token"`    // 可选，与 agent -token 一致时通过 X-Cluster-Token 鉴权
	PollInterval time.Duration `yaml:"poll_interval"` // 进度轮询间隔，默认 1s
	AgentTimeout time.Duration `yaml:"agent_timeout"` // 连续无响应判定失联的窗口，默认 10s
}

// EarlyStopConfig 描述基于错误率的档位早停与跳档策略，默认关闭，不影响现有配置。
type EarlyStopConfig struct {
	Enabled               bool    `yaml:"enabled"`                 // 总开关，默认 false
	MaxErrorRate          float64 `yaml:"max_error_rate"`          // 档位失败率超过该值判定为不可用，(0,1]，默认 0.5
	MinSamples            int     `yaml:"min_samples"`             // 至少凑够这么多请求才评估错误率，避免开局抖动误判，默认 20
	SkipHigherConcurrency *bool   `yaml:"skip_higher_concurrency"` // 判定不可用时是否跳过该模型剩余的更高并发档位，默认 true
}

// PromptConfig 描述文本端点的 prompt 生成方式。
type PromptConfig struct {
	Mode   PromptMode `yaml:"mode"`   // text | dynamic | codex | dataset，默认 text
	Text   string     `yaml:"text"`   // mode=text 时使用的固定文本
	Tokens int        `yaml:"tokens"` // mode=dynamic 时生成文本的目标近似 token 数

	// TokensMin/TokensMax 让 dynamic 的目标 token 数按请求在区间内均匀采样，
	// 两者须同时配置且 min <= max，配置后覆盖 tokens；仅 mode=dynamic 下合法。
	TokensMin int `yaml:"tokens_min"`
	TokensMax int `yaml:"tokens_max"`

	// DatasetPath 是 mode=dataset 时的文本文件路径（每个非空行一条 prompt），必填。
	DatasetPath string `yaml:"dataset_path"`

	// datasetLines 是 validate 阶段读入的数据集内容，ToBenchmark 内联进配置。
	datasetLines []string
}

// ModelConfig 描述一个待测模型。
type ModelConfig struct {
	Name       string `yaml:"name"`
	Provider   string `yaml:"provider"`
	TokenGroup string `yaml:"token_group"`
}

// Load 从 path 读取 YAML 配置，填充默认值并做完整校验。
// NoTUI/NoExcel/Output 等输出偏好保留在 Config 上，压测参数通过
// ToBenchmark 转换为 types.BenchmarkConfig。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // 拒绝未知字段，及早暴露拼写错误
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyDefaults 为未配置的字段填充默认值。
func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.BaseURL) == "" {
		c.BaseURL = defaultBaseURL
	}
	if c.Duration <= 0 {
		c.Duration = defaultDuration
	}
	if len(c.Concurrency) == 0 {
		c.Concurrency = append([]int(nil), defaultConcurrency...)
	}
	if c.Prompt.Mode == "" {
		c.Prompt.Mode = PromptModeText
	}
	if strings.TrimSpace(c.Prompt.Text) == "" {
		c.Prompt.Text = defaultPromptText
	}
	if c.Prompt.Tokens <= 0 {
		c.Prompt.Tokens = defaultPromptTokens
	}
	if strings.TrimSpace(c.ImagePrompt) == "" {
		c.ImagePrompt = defaultImagePrompt
	}
	if c.Warmup == nil {
		enabled := true
		c.Warmup = &enabled
	}
	if c.WarmupDuration <= 0 {
		c.WarmupDuration = defaultWarmupDuration
	}
	if c.Cooldown == nil {
		d := defaultCooldown
		c.Cooldown = &d
	}
	if c.ThinkTime == nil {
		d := defaultThinkTime
		c.ThinkTime = &d
	}
	// 只在「未配置」（零值）时填默认：负数留给 validate 报错，
	// 否则 -1 会被悄悄当成 8192。
	if c.MaxOutputTokens == 0 {
		c.MaxOutputTokens = types.DefaultMaxOutputTokens
	}
	if c.EarlyStop.Enabled {
		if c.EarlyStop.MaxErrorRate <= 0 {
			c.EarlyStop.MaxErrorRate = defaultMaxErrorRate
		}
		if c.EarlyStop.MinSamples <= 0 {
			c.EarlyStop.MinSamples = defaultMinSamples
		}
		if c.EarlyStop.SkipHigherConcurrency == nil {
			enabled := true
			c.EarlyStop.SkipHigherConcurrency = &enabled
		}
	}
	if c.Cluster != nil {
		if c.Cluster.PollInterval <= 0 {
			c.Cluster.PollInterval = defaultPollInterval
		}
		if c.Cluster.AgentTimeout <= 0 {
			c.Cluster.AgentTimeout = defaultAgentTimeout
		}
	}
	if c.LoadMode == "" {
		c.LoadMode = LoadModeClosed
	}
	if c.Tokenizer != nil && strings.TrimSpace(c.Tokenizer.Path) != "" && c.Tokenizer.UsageDriftPct == nil {
		d := defaultUsageDriftPct
		c.Tokenizer.UsageDriftPct = &d
	}
}

// validate 校验必填项与取值合法性。默认值已在 applyDefaults 中填充，
// 此处只需检查用户可能填错的内容。
func (c *Config) validate() error {
	switch c.Prompt.Mode {
	case PromptModeText, PromptModeDynamic, PromptModeCodex, PromptModeDataset:
	default:
		return fmt.Errorf("prompt.mode 非法：%q（合法值：%s、%s、%s、%s）",
			c.Prompt.Mode, PromptModeText, PromptModeDynamic, PromptModeCodex, PromptModeDataset)
	}
	if err := c.validatePromptExtras(); err != nil {
		return err
	}
	if err := c.validateTokenizer(); err != nil {
		return err
	}
	if err := c.validateRequestsPerLevel(); err != nil {
		return err
	}

	for _, v := range c.Concurrency {
		if v <= 0 {
			return fmt.Errorf("concurrency 必须为正整数，发现非法值：%d", v)
		}
	}

	if len(c.Models) == 0 {
		return fmt.Errorf("models 为必填项，至少配置一个模型")
	}
	for i, m := range c.Models {
		if strings.TrimSpace(m.Name) == "" {
			return fmt.Errorf("models[%d].name 不能为空", i)
		}
		p := types.Provider(strings.ToLower(strings.TrimSpace(m.Provider)))
		if !runner.IsSupportedProvider(p) {
			return fmt.Errorf("models[%d]：未知 provider %q（合法值：%s）", i, m.Provider, runner.RegisteredProviders())
		}
	}

	groups := c.normalizedTokenGroups()
	for i, m := range c.Models {
		group := strings.TrimSpace(m.TokenGroup)
		if group == "" {
			group = defaultTokenGroup
		}
		if len(groups[group]) == 0 {
			if group == defaultTokenGroup {
				return fmt.Errorf("models[%d] 未指定 token_group，但 tokens 中没有有效 token", i)
			}
			return fmt.Errorf("models[%d].token_group %q 不存在或不含有效 token", i, group)
		}
	}

	if c.EarlyStop.Enabled {
		if c.EarlyStop.MaxErrorRate <= 0 || c.EarlyStop.MaxErrorRate > 1 {
			return fmt.Errorf("early_stop.max_error_rate 必须在 (0, 1] 区间内，发现非法值：%v", c.EarlyStop.MaxErrorRate)
		}
		if c.EarlyStop.MinSamples <= 0 {
			return fmt.Errorf("early_stop.min_samples 必须为正整数，发现非法值：%d", c.EarlyStop.MinSamples)
		}
	}

	if c.Cluster != nil {
		if err := c.Cluster.validate(); err != nil {
			return err
		}
	}

	switch c.LoadMode {
	case LoadModeClosed, LoadModeOpen:
	default:
		return fmt.Errorf("load_mode 非法：%q（合法值：%s、%s）", c.LoadMode, LoadModeClosed, LoadModeOpen)
	}
	if c.OpenLoopUnbounded && c.LoadMode != LoadModeOpen {
		return fmt.Errorf("open_loop_unbounded 仅在 load_mode 为 %s 时有效", LoadModeOpen)
	}
	if c.LoadMode == LoadModeOpen {
		if len(c.RequestRate) == 0 {
			return fmt.Errorf("load_mode 为 %s 时 request_rate 为必填项，至少配置一个目标 RPS", LoadModeOpen)
		}
		if len(c.RequestRate) != len(c.Concurrency) {
			return fmt.Errorf("request_rate 长度（%d）必须与 concurrency 长度（%d）一致：open 模式下两者逐档位一一对应",
				len(c.RequestRate), len(c.Concurrency))
		}
		for i, r := range c.RequestRate {
			if r <= 0 {
				return fmt.Errorf("request_rate 必须为正数，发现非法值：request_rate[%d]=%v", i, r)
			}
		}
	}

	if c.SLO != nil {
		if c.SLO.TTFTMs < 0 || c.SLO.TPOTMs < 0 || c.SLO.E2EMs < 0 {
			return fmt.Errorf("slo.ttft_ms/tpot_ms/e2e_ms 不能为负数")
		}
	}

	if c.ThinkTime != nil && *c.ThinkTime < 0 {
		return fmt.Errorf("think_time 不能为负数，发现非法值：%v", *c.ThinkTime)
	}
	if c.MaxOutputTokens < 0 {
		return fmt.Errorf("max_output_tokens 不能为负数，发现非法值：%d", c.MaxOutputTokens)
	}

	return nil
}

// validatePromptExtras 校验 dynamic 的长度区间与 dataset 模式的数据集文件。
// 数据集在这里一次读入并缓存：既是校验（文件存在、非空），也避免运行期再读磁盘。
func (c *Config) validatePromptExtras() error {
	lo, hi := c.Prompt.TokensMin, c.Prompt.TokensMax
	if lo != 0 || hi != 0 {
		if c.Prompt.Mode != PromptModeDynamic {
			return fmt.Errorf("prompt.tokens_min/tokens_max 仅在 prompt.mode 为 %s 时有效", PromptModeDynamic)
		}
		if lo <= 0 || hi <= 0 {
			return fmt.Errorf("prompt.tokens_min 与 tokens_max 必须同时为正整数，发现 min=%d max=%d", lo, hi)
		}
		if lo > hi {
			return fmt.Errorf("prompt.tokens_min（%d）不能大于 tokens_max（%d）", lo, hi)
		}
	}

	if strings.TrimSpace(c.Prompt.DatasetPath) != "" && c.Prompt.Mode != PromptModeDataset {
		return fmt.Errorf("prompt.dataset_path 仅在 prompt.mode 为 %s 时有效", PromptModeDataset)
	}
	if c.Prompt.Mode == PromptModeDataset {
		path := strings.TrimSpace(c.Prompt.DatasetPath)
		if path == "" {
			return fmt.Errorf("prompt.mode 为 %s 时 prompt.dataset_path 为必填项", PromptModeDataset)
		}
		lines, err := prompts.LoadDatasetLines(path)
		if err != nil {
			return fmt.Errorf("prompt.dataset_path: %w", err)
		}
		c.Prompt.datasetLines = lines
	}
	return nil
}

// validateTokenizer 校验分词器目录可加载、阈值非负。加载失败要在这里报出来：
// 运行期静默回退到字符估算会让「配了分词器」的报告口径其实和没配一样。
func (c *Config) validateTokenizer() error {
	if c.Tokenizer == nil {
		return nil
	}
	path := strings.TrimSpace(c.Tokenizer.Path)
	if path == "" {
		if c.Tokenizer.UsageDriftPct != nil {
			return fmt.Errorf("tokenizer.usage_drift_pct 需要同时配置 tokenizer.path")
		}
		return nil
	}
	c.Tokenizer.Path = path
	if err := prompts.ValidateTokenizer(path, ""); err != nil {
		return fmt.Errorf("tokenizer.path: %w", err)
	}
	if c.Tokenizer.UsageDriftPct != nil && *c.Tokenizer.UsageDriftPct < 0 {
		return fmt.Errorf("tokenizer.usage_drift_pct 不能为负数，发现非法值：%v", *c.Tokenizer.UsageDriftPct)
	}
	return nil
}

// validateRequestsPerLevel 校验请求数制配置：长度 1 或与 concurrency 等长，逐项为正。
func (c *Config) validateRequestsPerLevel() error {
	n := len(c.RequestsPerLevel)
	if n == 0 {
		return nil
	}
	if n != 1 && n != len(c.Concurrency) {
		return fmt.Errorf("requests_per_level 长度（%d）必须为 1（作用于全部档位）或与 concurrency 长度（%d）一致",
			n, len(c.Concurrency))
	}
	for i, v := range c.RequestsPerLevel {
		if v <= 0 {
			return fmt.Errorf("requests_per_level 必须为正整数，发现非法值：requests_per_level[%d]=%d", i, v)
		}
	}
	return nil
}

// validate 校验 cluster 段：agents 非空、逐项非空且不重复。
func (c *ClusterConfig) validate() error {
	if len(c.Agents) == 0 {
		return fmt.Errorf("cluster.agents 为必填项，至少配置一个 agent 地址")
	}
	seen := make(map[string]struct{}, len(c.Agents))
	for i, addr := range c.Agents {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return fmt.Errorf("cluster.agents[%d] 不能为空", i)
		}
		if _, dup := seen[addr]; dup {
			return fmt.Errorf("cluster.agents[%d] 重复：%s", i, addr)
		}
		seen[addr] = struct{}{}
		c.Agents[i] = addr
	}
	return nil
}

// ToBenchmark 把校验通过的配置转换为 types.BenchmarkConfig。
func (c *Config) ToBenchmark() types.BenchmarkConfig {
	groups := c.normalizedTokenGroups()
	models := make([]types.ModelSpec, 0, len(c.Models))
	for _, m := range c.Models {
		group := strings.TrimSpace(m.TokenGroup)
		if group == "" {
			group = defaultTokenGroup
		}
		models = append(models, types.ModelSpec{
			Name:       strings.TrimSpace(m.Name),
			Provider:   types.Provider(strings.ToLower(strings.TrimSpace(m.Provider))),
			TokenGroup: group,
			Tokens:     append([]string(nil), groups[group]...),
		})
	}

	warmup := c.Warmup != nil && *c.Warmup

	// applyDefaults 保证非 nil；这里仍兜一层，容忍不经 Load 直接构造 Config 的调用方
	thinkTime := defaultThinkTime
	if c.ThinkTime != nil {
		thinkTime = *c.ThinkTime
	}

	var (
		tokenizerPath string
		fingerprint   string
		driftPct      float64
	)
	if c.Tokenizer != nil && c.Tokenizer.Path != "" {
		tokenizerPath = c.Tokenizer.Path
		// validate 已保证可加载；指纹随配置下发，分布式 agent 预检时据此校验词表一致
		fingerprint = prompts.CounterFor(tokenizerPath).Fingerprint()
		if c.Tokenizer.UsageDriftPct != nil {
			driftPct = *c.Tokenizer.UsageDriftPct
		}
	}

	return types.BenchmarkConfig{
		BaseURL:               c.BaseURL,
		Models:                models,
		Concurrency:           append([]int(nil), c.Concurrency...),
		Duration:              c.Duration,
		Prompt:                c.Prompt.Text,
		ImagePrompt:           c.ImagePrompt,
		DynamicPrompt:         c.Prompt.Mode == PromptModeDynamic,
		PromptTokens:          c.Prompt.Tokens,
		PromptTokensMin:       c.Prompt.TokensMin,
		PromptTokensMax:       c.Prompt.TokensMax,
		CodexPrompt:           c.Prompt.Mode == PromptModeCodex,
		DatasetPrompt:         c.Prompt.Mode == PromptModeDataset,
		DatasetLines:          append([]string(nil), c.Prompt.datasetLines...),
		TokenizerPath:         tokenizerPath,
		TokenizerFingerprint:  fingerprint,
		UsageDriftPct:         driftPct,
		Warmup:                warmup,
		WarmupDuration:        c.WarmupDuration,
		WarmupPerLevel:        warmup && c.WarmupPerLevel,
		CooldownDuration:      *c.Cooldown, // applyDefaults 保证非 nil
		ThinkTime:             thinkTime,
		MaxOutputTokens:       c.MaxOutputTokens,
		EarlyStopEnabled:      c.EarlyStop.Enabled,
		MaxErrorRate:          c.EarlyStop.MaxErrorRate,
		MinSamples:            c.EarlyStop.MinSamples,
		SkipHigherConcurrency: c.EarlyStop.Enabled && c.EarlyStop.SkipHigherConcurrency != nil && *c.EarlyStop.SkipHigherConcurrency,
		OpenLoop:              c.LoadMode == LoadModeOpen,
		OpenLoopUnbounded:     c.LoadMode == LoadModeOpen && c.OpenLoopUnbounded,
		RequestRate:           append([]float64(nil), c.RequestRate...),
		RequestsPerLevel:      append([]int(nil), c.RequestsPerLevel...),
		SLO:                   c.sloThresholds(),
		ShowHistogram:         c.ShowHistogram,
	}
}

// sloThresholds 把 YAML 里的毫秒整数阈值转换为 types.SLOThresholds。
// c.SLO 为 nil（未配置该块）时返回零值，Enabled() 恒为 false。
func (c *Config) sloThresholds() types.SLOThresholds {
	if c.SLO == nil {
		return types.SLOThresholds{}
	}
	return types.SLOThresholds{
		TTFT: time.Duration(c.SLO.TTFTMs) * time.Millisecond,
		TPOT: time.Duration(c.SLO.TPOTMs) * time.Millisecond,
		E2E:  time.Duration(c.SLO.E2EMs) * time.Millisecond,
	}
}

const defaultTokenGroup = "default"

// normalizedTokenGroups 把旧版 tokens 映射为默认分组，并裁剪各组中的空 token。
func (c *Config) normalizedTokenGroups() map[string][]string {
	groups := make(map[string][]string, len(c.TokenGroups)+1)
	for name, tokens := range c.TokenGroups {
		group := strings.TrimSpace(name)
		if group == "" {
			continue
		}
		groups[group] = cleanTokens(tokens)
	}
	groups[defaultTokenGroup] = cleanTokens(c.Tokens)
	return groups
}

func cleanTokens(tokens []string) []string {
	cleaned := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token = strings.TrimSpace(token); token != "" {
			cleaned = append(cleaned, token)
		}
	}
	return cleaned
}
