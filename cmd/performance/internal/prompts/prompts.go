package prompts

import (
	"bufio"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AyakuraYuki/llm-inspector/internal/tokenizers"
)

// approxCharsPerToken 是英文文本下字符数换算 token 数的经验比例，
// 仅用于将 -prompt-tokens 换算为目标字符数，不代表任何模型的真实 tokenizer 行为。
const approxCharsPerToken = 4

// dynamicPromptCorpus 是用于动态长文本测试的语料库。压测长上下文场景时，
// 从中随机乱序抽取段落拼接成目标长度的 prompt，每次请求内容都不同，
// 避免被上游/网关按内容命中缓存，同时能观察输入 token 数变化对 TTFT/TPOT 的影响。
var dynamicPromptCorpus = []string{
	"Distributed systems trade consistency, availability, and partition tolerance against each other, and no architecture can maximize all three at once. Engineers building large-scale services must decide upfront which guarantees matter most for their workload, then design retries, timeouts, and failover paths around that choice.",
	"A load balancer's job is deceptively simple: spread incoming requests across a pool of backends without any single one becoming a bottleneck. In practice this involves health checks, connection draining, weighted routing, and careful handling of slow or partially failed nodes so that one bad instance does not degrade the whole fleet.",
	"Caching improves perceived latency by serving previously computed results instead of recomputing them, but every cache introduces the risk of staleness. Systems that cache aggressively need clear invalidation rules, sensible TTLs, and monitoring for cache hit ratio, otherwise stale data silently leaks into production responses.",
	"Connection pooling reduces the overhead of repeatedly establishing TCP and TLS handshakes for every outbound request. Under high concurrency, an undersized pool causes queueing and artificially inflated latency, while an oversized pool can exhaust file descriptors or overwhelm a downstream service that was not provisioned for that load.",
	"Streaming APIs return partial output as soon as it becomes available rather than waiting for the entire response to be generated. This lowers time-to-first-byte dramatically for long-running generation tasks, at the cost of more complex client-side parsing and the need to handle partial or interrupted streams gracefully.",
	"Rate limiting protects shared infrastructure from being overwhelmed by any single client, but the algorithm chosen matters. Token bucket implementations smooth out bursts while still allowing short spikes, whereas fixed window counters are simpler to reason about but can allow twice the intended rate at window boundaries.",
	"Observability is more than logging; it requires structured traces, metrics, and logs that can be correlated across service boundaries. Without a shared request identifier propagated through every hop, debugging a slow request in a microservices architecture becomes a matter of guesswork rather than evidence.",
	"Idempotency keys let clients safely retry a request that may have partially succeeded, without risking duplicate side effects such as double charges or duplicate resource creation. Designing an API to be idempotent from the start is far cheaper than retrofitting it after clients begin relying on at-least-once delivery semantics.",
	"Horizontal scaling adds more machines to handle increased load, while vertical scaling makes existing machines more powerful. Horizontal scaling generally offers better fault tolerance and cost flexibility, but it demands that application state be externalized so that any instance can serve any request interchangeably.",
	"Backpressure mechanisms allow a slow consumer to signal an upstream producer to slow down, preventing unbounded queues and out-of-memory failures. Systems that ignore backpressure tend to work fine under light load and then fail catastrophically the moment traffic spikes beyond what downstream services can absorb.",
	"Circuit breakers stop a service from repeatedly calling a dependency that is already failing, giving the dependency time to recover and preventing cascading failures across the system. A well-tuned circuit breaker trips quickly on sustained errors but resets cautiously, probing with limited traffic before fully reopening.",
	"Database connection limits are a common hidden bottleneck: an application server pool sized for high concurrency can easily exceed what the database allows, leading to connection errors that look like application bugs but are really a capacity mismatch between two independently configured layers of the stack.",
	"Retry storms occur when many clients simultaneously retry failed requests at the same interval, synchronizing their load and making an already struggling service worse. Adding randomized jitter to retry delays spreads the retries out over time and significantly reduces the odds of a coordinated overload.",
	"Latency and throughput are related but distinct: a system can have low latency for individual requests while still having poor throughput under concurrency if it cannot process many requests in parallel, and conversely a batch-oriented system might have excellent throughput with unacceptably high latency per item.",
	"Token-based authentication schemes typically separate a short-lived access token from a longer-lived refresh token, limiting the blast radius if an access token is ever leaked. Rotating refresh tokens on each use further reduces the window during which a stolen token remains useful to an attacker.",
	"Blue-green deployments run two identical production environments and switch traffic between them atomically, making rollbacks nearly instantaneous if a new release misbehaves. The tradeoff is running double the infrastructure during the transition window, which can be costly for stateful or resource-intensive services.",
	"Chaos engineering deliberately injects failures, such as killing instances or introducing network latency, into a system to validate that its resilience mechanisms actually work under realistic conditions rather than only in theory. Teams that skip this step often discover their failover logic is broken only during a real incident.",
	"Sharding partitions a dataset across multiple database instances so that no single node has to store or serve the entire dataset. Choosing a good shard key is critical, because a poor choice can create hot shards that receive disproportionate traffic while others sit nearly idle.",
	"Autoscaling policies react to metrics like CPU utilization or queue depth to add or remove capacity automatically, but naive policies can oscillate rapidly if the metric is noisy or the scaling cooldown is too short, wasting resources on constant scale-up and scale-down cycles instead of settling into a stable state.",
	"API versioning strategies range from URL path versions to header-based negotiation, and each comes with different maintenance costs. Whatever scheme is chosen, the harder problem is usually organizational: deciding how long old versions must be supported and communicating deprecation timelines clearly to every downstream consumer.",
}

// dynamicPromptSuffix 是动态长文本末尾的固定指令，让请求有一个明确的任务，
// 避免模型面对一堆无问题的段落时输出不可控。
const dynamicPromptSuffix = "\n\nBased on the passages above, write a concise summary in your own words."

// Counter 是绑定到一份词表的 token 计数器，并缓存语料库各段落的 token 数：
// 段落是固定的，逐请求重复分词纯属浪费；有了缓存，拼装一条 prompt 只需对
// 「最后一个被截断的段落」做少量分词，其余全是整数加法。
type Counter struct {
	tk *tokenizers.Tokenizer

	once       sync.Once
	paraTokens []int // 与 dynamicPromptCorpus 一一对应，带前导空格计数
	suffix     int
}

var counters sync.Map // map[string]*Counter，key 为 tokenizer 目录路径

// CounterFor 返回指向 path 的分词计数器；path 为空或加载失败时返回 nil，
// 调用方据此回退到字符估算。tokenizers.New 本身按绝对路径缓存解析结果，
// 这里再按原始路径缓存 Counter 是为了复用段落计数。
func CounterFor(path string) *Counter {
	if path == "" {
		return nil
	}
	if v, ok := counters.Load(path); ok {
		return v.(*Counter)
	}
	tk, err := tokenizers.New(path)
	if err != nil {
		return nil
	}
	c := &Counter{tk: tk}
	v, _ := counters.LoadOrStore(path, c)
	return v.(*Counter)
}

// Count 返回文本的 token 数（不含特殊 token），编码失败时返回 0。
func (c *Counter) Count(text string) int {
	if c == nil || c.tk == nil {
		return 0
	}
	return c.tk.Count(text)
}

// Name 返回分词器名称（配置目录名）。
func (c *Counter) Name() string {
	if c == nil {
		return ""
	}
	return c.tk.Name()
}

// Fingerprint 返回词表文件的 SHA-256。
func (c *Counter) Fingerprint() string {
	if c == nil {
		return ""
	}
	return c.tk.Fingerprint()
}

// ValidateTokenizer 校验分词器目录可加载；fingerprint 非空时还要求词表指纹一致。
// 单机版在配置加载时调用（fail fast），分布式 agent 在预检时调用：coordinator
// 下发的是路径而非词表本体，各节点必须自己证明手里的词表和 coordinator 的是同一份。
func ValidateTokenizer(path, fingerprint string) error {
	if path == "" {
		return nil
	}
	tk, err := tokenizers.New(path)
	if err != nil {
		return err
	}
	if fingerprint != "" && tk.Fingerprint() != fingerprint {
		return fmt.Errorf("tokenizer %q 词表指纹不一致：本地 %s，期望 %s（各节点需预置同一份词表文件）",
			path, shortHash(tk.Fingerprint()), shortHash(fingerprint))
	}
	return nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// BuildDynamicPrompt 从语料库中随机乱序拼接段落到目标 token 数，并在开头附加
// 随机 nonce 防止上游按内容命中缓存。targetTokens <= 0 时退化为默认长度（约 2000 token）。
//
// ctr 为 nil 时按 approxCharsPerToken 的字符数近似（历史行为，误差随模型词表
// 可达 ±30%）；非 nil 时用本地分词器逐段累加并截断最后一段，结果与目标的偏差
// 通常在个位数 token 以内——这是对标 evalscope `--tokenizer-path` 精确控长的关键。
func BuildDynamicPrompt(targetTokens int, ctr *Counter) string {
	if targetTokens <= 0 {
		targetTokens = 2000
	}
	if ctr == nil {
		return buildDynamicByChars(targetTokens)
	}
	return buildDynamicByTokens(targetTokens, ctr)
}

// buildDynamicByChars 是无分词器时的字符数近似实现。
func buildDynamicByChars(targetTokens int) string {
	targetChars := targetTokens * approxCharsPerToken

	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "[bench-nonce %d-%d] ", time.Now().UnixNano(), rand.IntN(1_000_000_000))

	order := rand.Perm(len(dynamicPromptCorpus))
	for i := 0; sb.Len() < targetChars; i++ {
		if i > 0 && i%len(order) == 0 {
			order = rand.Perm(len(dynamicPromptCorpus))
		}
		sb.WriteString(dynamicPromptCorpus[order[i%len(order)]])
		sb.WriteString(" ")
	}

	sb.WriteString(dynamicPromptSuffix)
	return sb.String()
}

// buildDynamicByTokens 用分词器把 prompt 收敛到目标 token 数。
//
// 段落以「前导空格 + 段落」为单位拼接：BPE 的预切分把空格归给后一个词，
// 因此单独计数的片段与拼接后整体切分的边界一致，逐段累加不会因边界合并而漂移。
// 最后一段按词数二分截断，只需 O(log 词数) 次分词。
func buildDynamicByTokens(targetTokens int, ctr *Counter) string {
	ctr.once.Do(func() {
		ctr.paraTokens = make([]int, len(dynamicPromptCorpus))
		for i, p := range dynamicPromptCorpus {
			ctr.paraTokens[i] = ctr.Count(" " + p)
		}
		ctr.suffix = ctr.Count(dynamicPromptSuffix)
	})

	prefix := fmt.Sprintf("[bench-nonce %d-%d]", time.Now().UnixNano(), rand.IntN(1_000_000_000))
	budget := targetTokens - ctr.Count(prefix) - ctr.suffix

	var sb strings.Builder
	sb.WriteString(prefix)

	used := 0
	order := rand.Perm(len(dynamicPromptCorpus))
	for i := 0; used < budget; i++ {
		if i > 0 && i%len(order) == 0 {
			order = rand.Perm(len(dynamicPromptCorpus))
		}
		idx := order[i%len(order)]
		if c := ctr.paraTokens[idx]; used+c <= budget {
			sb.WriteString(" ")
			sb.WriteString(dynamicPromptCorpus[idx])
			used += c
			continue
		}
		// 整段放不下：按词截断补足剩余预算后结束
		sb.WriteString(takeWords(dynamicPromptCorpus[idx], budget-used, ctr))
		break
	}

	sb.WriteString(dynamicPromptSuffix)
	return sb.String()
}

// takeWords 返回段落 p 的前若干个词（带前导空格），使其 token 数不超过 budget
// 且尽可能接近。二分查找词数，每次探测分词一次。
func takeWords(p string, budget int, ctr *Counter) string {
	if budget <= 0 {
		return ""
	}
	words := strings.Fields(p)
	lo, hi := 0, len(words)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if ctr.Count(" "+strings.Join(words[:mid], " ")) <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return ""
	}
	return " " + strings.Join(words[:lo], " ")
}

// PickTargetTokens 在 [lo, hi] 内均匀采样一个目标 token 数；区间未配置
// （lo/hi 任一 <= 0）时返回 fallback。对标 evalscope 的 min/max-prompt-length：
// 真实流量的输入长度是分布而非单点，固定长度测出的时延分位数偏「干净」。
func PickTargetTokens(lo, hi, fallback int) int {
	if lo <= 0 || hi <= 0 {
		return fallback
	}
	if hi < lo {
		lo, hi = hi, lo
	}
	return lo + rand.IntN(hi-lo+1)
}

// LoadDatasetLines 读取 line_by_line 数据集：每个非空行是一条独立 prompt，
// 首尾空白被裁剪。对标 evalscope 的同名数据集（仅纯文本行，不解析 JSON 消息体）。
func LoadDatasetLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取数据集失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024) // 单行上限 16MB，容纳超长上下文 prompt
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取数据集 %s 失败: %w", path, err)
	}
	if len(lines) == 0 {
		return nil, errors.New("数据集 " + path + " 中没有任何非空行")
	}
	return lines, nil
}

// PickDatasetLine 从数据集中随机返回一行。随机而非顺序轮转：分布式下各节点
// 若都从第一行顺序发送，会同时打出完全相同的请求序列，叠加放大缓存/批处理效应。
func PickDatasetLine(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[rand.IntN(len(lines))]
}

// codexSystemPrompt 模拟标准 AI Agent 开发工具（如 Codex CLI）在每次会话中固定
// 下发的系统提示词：篇幅长、内容基本不变。真实场景下，大量用户各自使用这类工具时，
// 上游收到的请求会共享这段几乎相同的长前缀，仅在末尾附加各自简短的提问，
// 这与 -dynamic-prompt 刻意打乱内容以规避缓存正好相反，用于压测“高相似度请求”场景
// （例如网关/上游的前缀缓存命中率）。
const codexSystemPrompt = `You are a helpful assistant. You will be presented with a user prompt, and your job is to provide a short title for a task that will be created from that prompt.
The tasks typically have to do with coding-related tasks, for example requests for bug fixes or questions about a codebase. The title you generate will be shown in the UI to represent the prompt.
Generate a concise UI title (up to 36 characters) for this task.
Fill the structured title field with plain text.
Do not include quotes, markdown, formatting characters, or trailing punctuation in the title value.
If the task includes a ticket reference (e.g. ABC-123), include it verbatim.

Generate a clear, informative task title based solely on the prompt provided. Follow the rules below to ensure consistency, readability, and usefulness.

How to write a good title:
Generate a single-line title that captures the question or core change requested. The title should be easy to scan and useful in changelogs or review queues.
- Use an imperative verb first: "Add", "Fix", "Update", "Refactor", "Remove", "Locate", "Find", etc.
- Keep it under 36 characters and under 5 words where possible.
- If the user's prompt is already a short clear title, reuse it verbatim.
- Capitalize only the first word (unless locale requires otherwise).
- Write the title in the user's locale.
- Do not use punctuation at the end.
- Output the title as plain text with no surrounding quotes or backticks.
- Use precise, non-redundant language.
- Translate fixed phrases into the user's locale (e.g., "Fix bug" -> "Corrige el error" in Spanish-ES), but leave code terms in English unless a widely adopted translation exists.
- If the user provides a title explicitly, reuse it (translated if needed) and skip generation logic.
- Make it clear when the user is requesting changes (use verbs like "Fix", "Add", etc) vs asking a question (use verbs like "Find", "Locate", "Count").
- Do NOT respond to the user, answer questions, or attempt to solve the problem; just write a title that can represent the user's query.

Examples:
- User: "Can we add dark-mode support to the settings page?" -> Add dark-mode support
- User: "Fehlerbehebung: Beim Anmelden erscheint 500." (de-DE) -> Login-Fehler 500 beheben
- User: "Refactoriser le composant sidebar pour réduire le code dupliqué." (fr-FR) -> Refactoriser composant sidebar
- User: "How do I fix our login bug?" -> Troubleshoot login bug
- User: "Where in the codebase is foo_bar created" -> Locate foo_bar
- User: "what's 2+2" -> Calculate 2+2

By following these conventions, your titles will be readable, changelog-friendly, and helpful to both users and downstream tools.

User prompt:`

// codexUserQuestions 是模拟真实用户在 AI Agent 开发工具中输入的简短提问语料库，
// 长度远小于 codexSystemPrompt，用于拼接在系统提示词之后。
var codexUserQuestions = []string{
	"Why is the build failing after my last commit?",
	"Add unit tests for the new function I just wrote.",
	"Refactor this loop to run concurrently.",
	"There's a nil pointer panic somewhere in the request handler, can you find it?",
	"Explain what this regex is doing.",
	"Optimize this SQL query, it's too slow.",
	"Fix the failing test in the auth package.",
	"Can you add error handling around this network call?",
	"Why does this function return the wrong value for negative inputs?",
	"Rename this variable to something clearer.",
	"Write a docstring for this exported function.",
	"This API call is timing out, help me debug it.",
	"Split this file into smaller modules.",
	"Add a retry with backoff around this HTTP request.",
	"Why is memory usage growing over time in this service?",
}

// BuildCodexPrompt 拼接 codexSystemPrompt 与随机抽取的一条 codexUserQuestions，
// 模拟多用户使用同一类 AI Agent 开发工具、发送高相似度请求的真实流量模式。
func BuildCodexPrompt() string {
	q := codexUserQuestions[rand.IntN(len(codexUserQuestions))]
	return codexSystemPrompt + "\n" + q
}
