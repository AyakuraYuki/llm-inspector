# llm-inspector Web 测试平台设计方案

> 状态：待评审。本文档只做设计，不含实现代码。确认后按第 7 章分期实施。

## 0. 一页概览

- 在本仓库新增 `cmd/server`：一个常驻 Go 服务，内嵌前端静态页面，对外提供 Web UI + HTTP API。
- 三类测试任务（benchmark / evaluation / performance）以**子进程**方式执行现有的三个 CLI 二进制，服务负责生成 YAML 配置、捕获日志、解析进度、收集报告。
- 调度器有三道闸门限制平台载荷：**最大并行任务数**、**全局在途请求预算**、**独占任务模式**。
- 报告入库（SQLite）+ 文件落盘，任务完成后可调用**裁判模型**自动生成 ≤500 字评判结论。
- 前端用 Vue 3 + Element Plus + ECharts（组件库直出，不需要美术设计），4 个页面覆盖全部功能。
- 部署产物：4 个二进制 + 1 个配置文件，systemd/launchd 托管，可选 Docker。

## 1. 现状分析（代码事实，已核实）

### 1.1 三个工具的形态

| 工具 | 测试内容 | 配置方式 | 并发控制参数 | 报告产物 | 进度输出 |
|---|---|---|---|---|---|
| benchmark | 答题正确率 + TTFT/TPS/TPM | YAML（`-config`） | `max_workers`（默认 1） | `benchmark_results.json`（机器可读）+ 逐题 txt | 30s 心跳：`Progress: n/total completed, k in progress` |
| evaluation | L1–L6 六层评测（可用性/协议/能力/稳定性/性能/边界） | YAML（`run --config`） | L3 `concurrency`（默认 4）、L5 `concurrency` 梯度 | `report.json` + `report.md` | 层粒度日志 + L3 题粒度 `[done/total]` |
| performance | 多模型×多并发档位的时长制压测 | YAML（`-config`，严格模式） | `concurrency` 档位列表（默认 9 档） | **只有 Excel**（7 个 sheet）+ 终端汇总 | 10s 一行 `progress: N requests...`，档位 `[seq/total]` |

共同点：CLI 只有 `-config` 一个业务参数；退出码语义清晰（evaluation 0/1/2）；stdout 是 `[HH:MM:SS]` 前缀的逐行文本日志，极易按行捕获；都有周期性进度输出，可解析出百分比。

### 1.2 影响架构选择的关键事实

1. **进程级全局状态**：`internal/logger` 的日志文件是全局单例、performance 的 `sharedClient` 会在压测前原地重建 Transport、Excel 导出器有全局变量。**同一进程内无法安全地并发执行多个任务**。
2. **internal 可见性**：核心逻辑全在 `cmd/<tool>/internal/` 下，外部模块无法 import；想库化只能在本仓库内改。
3. **无 HTTP 框架、无并发库、无重试机制**：三个工具都是手写 net/http + WaitGroup。benchmark/evaluation 用的是默认连接池（MaxIdleConnsPerHost=2）；performance 自己调过连接池。
4. benchmark 的题库（AIME/MMLU-Pro）经 `go:embed` 编进二进制，构建前需 `make setup`（用 hf CLI 下载数据集）；MMLU-Pro parquet 运行时整份读入内存。
5. evaluation 的 YAML 已原生支持 `judge.*` 裁判模型（用于 L3 打分）——这与我们要做的"报告评判"是两回事，但说明裁判端点配置形态可以复用。
6. 发现的文档不一致（顺手记录，实现阶段修正）：evaluation README 宣称的 `--layers`/`--dataset` flag 实际不存在；benchmark README 的报告目录描述与代码不符。

### 1.3 结论

现有三个工具是设计得很自洽的 CLI：**YAML 进 → 退出码 + stdout + 报告目录出**。平台化不需要动它们的核心逻辑。

## 2. 总体架构

```
┌─────────────────────────────────────────────────────────┐
│ 浏览器  Vue3 + Element Plus SPA（go:embed 进 server）     │
└──────────────┬──────────────────────────────────────────┘
               │ HTTP API + SSE（日志/进度推送）
┌──────────────▼──────────────────────────────────────────┐
│ cmd/server（单进程常驻服务）                              │
│  ├─ api        HTTP 路由（stdlib mux）、SSE 广播          │
│  ├─ scheduler  FIFO 队列 + 三道闸门（§3.3）               │
│  ├─ executor   生成 YAML → 启动子进程 → 捕获 stdout      │
│  ├─ progress   按任务类型正则解析日志 → 进度事件          │
│  ├─ collector  收集报告文件 → 解析摘要 → 入库             │
│  ├─ judge      报告摘要 + 提示词模板 → 裁判模型 → 结论    │
│  ├─ preflight  启动/派生前资源检查（ulimit、内存、端口）  │
│  └─ store      SQLite（任务/预设/评判结论/设置）          │
└──────┬───────────────┬───────────────┬──────────────────┘
       │ 子进程         │ 子进程         │ 子进程
┌──────▼──────┐ ┌───────▼──────┐ ┌──────▼───────┐
│ benchmark   │ │ evaluation   │ │ performance  │
│ (现有二进制) │ │ (现有二进制)  │ │ (现有二进制)  │
└─────────────┘ └──────────────┘ └──────────────┘
```

### 2.1 关键决策：子进程隔离执行，而不是进程内库化

| | 子进程（推荐） | 进程内库化 |
|---|---|---|
| 改动量 | **三个工具零改动** | 需先拆掉 3 处全局状态、重构 config 入口 |
| 并发安全 | 天然隔离（各自进程空间） | 必须串行队列化或大改 |
| 崩溃隔离 | 一个任务 panic 不影响平台 | panic 拖垮整个服务 |
| 取消 | kill 进程树，干净彻底 | 依赖各工具 context 传递（目前不全） |
| 资源控制 | 可按进程设 rlimit / 内存上限 | 难以按任务隔离 |
| 进度获取 | 解析 stdout（三类都有周期日志，够用） | 回调更精确 |

代价是进度粒度受限于日志频率（benchmark 30s、performance 10s、evaluation 层粒度），对"看进度"场景完全够。后续如果想要更细粒度，可以在 executor 接口不变的前提下逐步把某个工具改为库化调用。

### 2.2 技术选型

| 层 | 选型 | 理由 |
|---|---|---|
| HTTP 路由 | Go stdlib `http.ServeMux`（Go 1.26 支持方法+路径模式） | 零新依赖，API 规模小（~15 个端点） |
| 实时推送 | SSE（`text/event-stream`） | 单向推送场景，stdlib 即可实现，前端 `EventSource` 原生支持，无需 WebSocket 库 |
| 存储 | SQLite（`modernc.org/sqlite`，纯 Go 无 cgo） | 单文件零运维；任务元数据有状态查询需求，比 JSON 文件省心；交叉编译无障碍 |
| 报告文件 | 原样保留在磁盘 `data/tasks/<id>/`，DB 只存路径和摘要 | 报告可能是几 MB 的 Excel/JSON，不入库 |
| 前端 | Vue 3 + Element Plus + ECharts + Vite | 见 §4.1 |
| 前端分发 | 构建产物 `go:embed` 进 server 二进制 | 单二进制部署，无静态文件路径问题 |

## 3. 后端设计

### 3.1 任务模型与状态机

```
created → queued → running → succeeded / failed / canceled
                                └→ (judge: pending → done / failed)
```

任务记录核心字段：

```
id, type(benchmark|evaluation|performance), name,
config_yaml        -- 提交时生成的完整 YAML（可复现）
endpoint_desc      -- base_url + model 摘要（列表页展示）
peak_concurrency   -- 提交时算出的峰值并发（调度预算用，§3.3）
exclusive          -- 是否独占运行
status, progress(0-100), progress_text, queue_position,
pid, exit_code, error,
report_dir, report_summary(JSON),
judge_status, judge_conclusion, judge_model,
created_at, started_at, finished_at
```

### 3.2 峰值并发计算（提交时）

- benchmark：`max_workers`
- evaluation：`max(L3.concurrency, max(L5.concurrency[]))`，默认 `max(4,16)=16`
- performance：`max(concurrency[])`（默认 150）

表单提交时前端就显示该值（"本任务峰值并发 ≈ 150"），后端落库，调度器据此做预算。

### 3.3 调度器：三道闸门

新任务进入 FIFO 队列，队首任务须同时满足三个条件才派发：

1. **任务数闸门**：`running_count < max_concurrent_tasks`（默认 2，可配）。这是"平台最大载荷上限"的直接体现。
2. **请求预算闸门**：`Σ running.peak_concurrency + 新任务.peak_concurrency ≤ max_inflight_requests`（默认 200，可配）。防止"3 个各 150 并发的压测把机器打满"。
3. **独占闸门**：运行中有 `exclusive` 任务则全员等待；队首是 `exclusive` 任务则等运行中任务全部结束。压测类任务对同机负载最敏感，建议压测默认勾选独占。

配套保护：

- 队列长度上限（默认 100），满了拒绝提交并提示。
- 单任务峰值并发硬上限（默认 500，可配），表单侧直接校验拦截。
- 派发前 preflight：检查 `ulimit -n` 是否 ≥ 预估 socket 需求（峰值并发 × 2 + 2000 余量）、可用内存是否 ≥ 任务预估（MMLU-Pro 全集约需数 GB，按题目规模分档），不满足则**阻止派发并给出修复指引**（而不是跑挂了再收尸）。

### 3.4 任务执行器（executor）

```
data/
  server.db
  tasks/<task-id>/
    config.yaml        -- 生成的配置
    stdout.log         -- 完整子进程输出
    reports/           -- 工具的报告输出目录（软链或原目录）
```

流程：

1. 按任务类型把表单参数渲染成该工具的 YAML，落盘 `config.yaml`（API key 支持 `${ENV}` 展开——evaluation 原生支持，benchmark/performance 由 executor 在生成时替换，**密钥不进数据库、不进前端回显**）。
2. `exec.CommandContext` 启动对应二进制，CWD 设为任务目录，stdout/stderr 合并写入 `stdout.log` 并同时按行推给 progress 解析器和 SSE 订阅者。
3. 取消 = `context.Cancel` → SIGTERM/SIGINT（performance 支持优雅中止并保留部分结果）→ 超时后 SIGKILL。
4. 进程退出后 collector 按类型收集报告：
   - benchmark：解析 `benchmark_results.json` → 聚合统计（准确率、平均 TTFT/TPS、finish_reason 分布）入库；
   - evaluation：直接读 `report.json`（verdict、total_score、sections、层分数）入库；
   - performance：用 excelize（项目已有依赖）读 Excel `总览` + 各数据 sheet 的首行摘要入库，Excel 原文件提供下载。**v1 不改 performance 代码**；后续可给它加一个 `-json` 输出（小 patch，列入 P3）。
5. 更新任务状态，触发 judge（若该任务类型启用了自动评判）。

### 3.5 进度解析规则（正则即可，不改工具代码）

| 类型 | 规则 | 产出 |
|---|---|---|
| benchmark | `Progress: (\d+)/(\d+) completed, (\d+) in progress` | 百分比 = done/total |
| evaluation | `执行 (L\d)` / `✓ (L\d) 完成` + L3 的 `\[(\d+)/(\d+)\]` | 6 级步骤条 + 层内百分比 |
| performance | `\[(\d+)/(\d+)\] Model=.*Concurrency=` + `(\d+)s remaining` | 档位进度 + 剩余秒数（时长制，ETA 精确可算） |

解析失败不影响任务本身，进度条只是停在最后已知值。

### 3.6 存储 schema（SQLite，4 张表）

- `tasks`：见 §3.1。
- `presets`：`id, name, type, config_json, created_at` —— 参数预设。
- `judge_profiles`：`id, name, base_url, model, api_key_ref(env名), prompt_template, max_tokens, is_default`。
- `settings`：`key, value` —— 调度参数、自动评判开关等，运行时改、无需重启。

### 3.7 HTTP API 一览

```
POST   /api/tasks                     创建并入队（body 为表单 JSON，服务端渲染 YAML）
GET    /api/tasks?status=&type=&page= 列表
GET    /api/tasks/{id}                详情（含报告摘要、评判结论）
POST   /api/tasks/{id}/stop           停止
POST   /api/tasks/{id}/rerun          克隆参数重新入队
DELETE /api/tasks/{id}                删除记录及产物
GET    /api/tasks/{id}/logs           SSE 实时日志（含历史回放）
GET    /api/tasks/{id}/artifacts/{name} 下载报告文件（json/md/xlsx/txt）
POST   /api/tasks/{id}/judge          手动触发/重新评判
GET    /api/presets?type=             预设列表
POST   /api/presets                   保存预设
GET    /api/settings                  读取调度/裁判设置
PUT    /api/settings                  修改（即时生效）
GET    /api/system/status             运行数/队列数/ulimit/内存/预算水位
```

前端不知道 YAML 细节：表单 JSON → 服务端校验并渲染 YAML → 回显给前端"配置预览"（高级用户可核对）。

### 3.8 裁判模型（报告评判）

- 触发方式：任务成功后自动触发（按类型可配开关）+ 详情页"生成评判 / 重新评判"按钮（可换 judge profile）。
- 输入：collector 产出的**报告摘要**（不是原始全量报告）。benchmark 喂聚合统计 + 失败题样例（≤3 条）；evaluation 喂 verdict/sections/层分数 + fail 检查项清单；performance 喂总览 + 各档位聚合行。摘要裁剪到约 8k token 以内。
- 提示词模板（默认，可改）：

```
你是大模型推理服务评测专家。以下是{任务类型}测试的报告摘要，请给出评判结论。
要求：不超过500字；先给总体结论（优秀/良好/及格/不达标），
再列关键指标，再指出主要问题与风险，最后给1-3条改进建议。
---
{报告摘要}
```

- 调用：OpenAI 兼容端点（复用 `pkg/go-openai`），超时 120s，失败重试 1 次，结论存 `tasks.judge_conclusion`。
- **防循环依赖**：裁判端点配置与被测端点完全独立；裁判任务本身不占用测试闸门（它只是一次普通 LLM 调用）。

## 4. 前端设计

### 4.1 为什么选 Vue 3 + Element Plus

- 你是前端小白——这套组合**不需要任何美术设计**：表单、表格、步骤条、进度条、标签、卡片全是现成组件，配色用库自带的默认主题（含暗黑模式开关）。
- 中文文档最完善，admin 类界面是它的主场，社区示例抄得到。
- 图表用 ECharts（同样零设计成本，官方示例改数据即用）。
- 构建后是一堆静态文件，`go:embed` 进 Go 二进制，部署无感。

### 4.2 页面结构（4 页 + 1 抽屉）

**P1 任务列表 / 仪表盘**（首页）

```
┌────────────────────────────────────────────────────┐
│ llm-inspector 控制台        [+ 新建任务]  [系统状态●]│
├────────────────────────────────────────────────────┤
│ 运行中 2/3 │ 队列 4 │ 在途并发 180/200 │ ulimit 65535│
├────────────────────────────────────────────────────┤
│ ▶ 压测-gpt5    performance  [██████░░] 档位6/9  12:31│
│ ▶ MMLU抽样    benchmark    [██░░░░░░] 45/120题 08:02│
│ ⏸ L6评测-deepseek  queued #1  (等待预算释放)         │
├────────────────────────────────────────────────────┤
│ 最近完成                                          │
│ ✓ benchmark-kimi   准确率92.3% TTFT 320ms [评判:良好]│
│ ✗ evaluation-glm   verdict=fail          [评判:不达标]│
└────────────────────────────────────────────────────┘
```

列表行内操作：停止 / 克隆重跑 / 查看 / 下载报告。verdict 和评判结论用彩色 Tag 直出。

**P2 新建任务**（向导，三步）

```
第1步：选类型（三张卡片平铺，各配一句人话说明）
 ┌─────────┐ ┌─────────┐ ┌─────────┐
 │答题基准  │ │六层评测  │ │并发压测  │
 │测准不准+ │ │全面体检， │ │能扛多少  │
 │跑多快    │ │出pass/fail│ │并发     │
 └─────────┘ └─────────┘ └─────────┘
第2步：动态表单（按类型切换，全部带默认值+问号提示）
  ┌─ 基本信息：任务名 / base_url / api_key / model
  ├─ 类型参数：（见 §4.3，分区折叠，高级项默认收起）
  ├─ 运行策略：☑独占运行  峰值并发≈150（实时计算展示）
  └─ [存为预设] [预览配置] [提交]
第3步：确认页（参数摘要 + 预计耗时）→ 提交 → 跳详情页
```

**P3 任务详情**

```
┌────────────────────────────────────────────────────┐
│ ← benchmark / MMLU抽样        ●running  [停止][重跑] │
├─ 进度 ─────────────────────────────────────────────┤
│ [████████░░░░] 45/120 题 · 已运行 8m · 预计剩余 ~14m │
├─ 结果（完成后显示）─────────────────────────────────┤
│ 准确率 92.3% │ 平均TTFT 320ms │ 平均TPS 85 │ 失败 2  │
│ [按数据集分布柱状图] [TTFT/TPS 散点或分位表]          │
│ [下载 JSON 报告] [下载逐题明细]                       │
├─ 裁判结论 ─────────────────────────────────────────┤
│ ┌──────────────────────────────────────────────┐   │
│ │ 总体：良好。准确率达标，TTFT 处于 SLO 内……(≤500字)│   │
│ └──────────────────────────────────────────────┘   │
│ 评判模型: deepseek-judge  [重新评判] [换裁判▼]        │
├─ 实时日志 ─────────────────────────────────────────┤
│ [12:31:02] Question 45 completed ... ✓  (自动滚动)   │
└────────────────────────────────────────────────────┘
```

evaluation 详情换成：verdict 大横幅 + L1–L6 层分数条形图 + 检查项树（pass/fail 标签）；performance 详情换成：档位×模型汇总表 + TTFT/TPS 随并发变化折线图 + Excel 下载。

**P4 系统设置**

- 调度：最大并行任务数、全局在途并发预算、单任务并发上限、队列长度（附一句"改这里会影响什么"）。
- 裁判：judge profile 列表（增删改）、各类型自动评判开关、提示词模板编辑。
- 资源状态：当前 ulimit -n、建议值、内存、磁盘；不达标时黄色警告条。
- 预设管理：列表、删除。

**抽屉**：任意页面点"系统状态"滑出——运行中任务、队列、预算水位、最近 20 条平台事件（派发/阻止/失败原因）。

### 4.3 表单分区（全部字段都有默认值，用户最少只填 3 项：base_url、api_key、model）

- **benchmark**：基本信息｜数据集（AIME25/AIME26 开关、MMLU-Pro 分类与抽样数——滑块+数字输入）｜采样参数（max_tokens/temperature/top_p/reasoning_effort/思考开关）｜并发（max_workers，带预算联动提示）｜自定义问题（可粘贴 JSON 或逐条添加）
- **evaluation**：基本信息（含 protocol 三选一）｜层开关（L1–L6 六个 Switch，各带一句说明）｜层参数（L3 并发/数据集、L4 采样数/浸测数、L5 次数/并发梯度/SLO 阈值）｜高级（constraints、fail_fast、超时）
- **performance**：基本信息（模型列表可多个，每行 name/provider/token_group）｜压测策略（每档时长、并发档位列表——标签式编辑、预热/冷却）｜Prompt 模式（text/dynamic/codex 三选一联动）｜token 池（textarea 每行一个，密钥只写不进库）

交互原则：所有默认值直接来自现有工具的默认值，行为与 CLI 一致；危险操作（停止任务、删除记录）二次确认；校验失败在提交前内联提示。

## 5. 资源与并发保护（对应你的两个核心诉求）

1. **"同时进行的测试不超过平台最大载荷"** → §3.3 三道闸门：任务数上限 + 在途并发预算 + 独占模式。预算按每个任务提交时算出的峰值并发记账，宁可排队不超发。
2. **"不因 open_files / socket 限制影响高并发测试"**：
   - server 启动时 `Setrlimit` 把 soft NOFILE 提到 hard，并在日志和设置页展示实际值；部署层再给足（systemd `LimitNOFILE=1048576` / launchd `maxfiles`）。子进程继承 server 的 rlimit。
   - 派发前 preflight 校验：`ulimit -n ≥ 在途预算 × 2 + 2000`，不达标**阻止派发**并给出具体修复命令。
   - socket 复用：performance 已调连接池；给 benchmark/evaluation 各加一个~10 行的 Transport 调优补丁（MaxIdleConnsPerHost 提到与并发匹配）——可选，列入 P2，不改测量语义。
   - TIME_WAIT/端口耗尽：长连接复用后并发 500 也就 ~500 个 socket，远低于 6 万+ 临时端口，文档里给 Linux 部署的 `ip_local_port_range` 建议值即可，macOS 本机使用无需处理。
   - 内存：MMLU-Pro 大抽样按题数分档预估内存，preflight 校验可用内存；tokenizer 只在 evaluation 用到时按路径懒加载。
3. **故障不扩散**：子进程崩溃/panic 只影响该任务；server 自身重启后从 SQLite 恢复队列，running 状态的孤儿任务标记为 `failed(server restarted)`，报告文件仍在。

## 6. 部署方案

### 6.1 产物与目录

```
dist/
  llm-inspector-server    # 内嵌前端
  benchmark               # 需先 make setup 拉数据集再构建
  evaluation
  performance
  server.yaml             # 监听地址/数据目录/二进制路径/闸门参数/裁判默认配置
  configs/tokenizers/     # evaluation L2 探测用（可选挂载）
```

`Makefile` 增加：`make web`（前端构建）、`make dist`（前端+四个二进制+示例配置打包）。

### 6.2 运行

- 开发：`make web && go run ./cmd/server -config server.yaml`。
- 生产（Linux）：systemd unit，`LimitNOFILE=1048576`、`Restart=always`、环境变量文件放 API key。
- 生产（macOS）：launchd plist，`SoftResourceLimits/HardResourceLimits` 设 maxfiles。
- Docker（可选，P3）：多阶段构建，前端 → Go → distroless，挂载 `data/` 卷。
- 监听默认 `127.0.0.1:8080`；要暴露给团队用就配 `listen: 0.0.0.0:8080` + `auth_token`（静态 Bearer token，前端登录页输入一次存 localStorage）。不做完整多用户体系——这是内部工具。

### 6.3 密钥管理

API key 一律以环境变量注入（`${VAR}` 写进生成的 YAML，子进程启动时展开）；数据库和日志里不出现明文 key；前端表单填的 key 仅用于当次提交，不保存（预设里也只存 env 变量名）。

## 7. 分期实施

| 期 | 内容 | 验收标准 |
|---|---|---|
| P1 核心平台 | server 骨架（API+SSE+SQLite）、调度器三闸门、executor+进度解析、任务目录与报告收集、preflight、前端 P1/P2/P3 页（结果区先用表格不画图）、Makefile dist | 网页提交三类任务各一次，排队/并发/取消行为正确，报告可下载 |
| P2 裁判+可视化 | judge 模块（profile、自动/手动触发、≤500字结论入库展示）、详情页 ECharts 图表、预设管理、设置页、benchmark/evaluation 连接池小补丁 | 任务完成自动出评判结论；图表正确渲染 |
| P3 打磨 | performance 输出 JSON 的小补丁（替代 Excel 解析）、任务对比页、系统事件抽屉、Docker 镜像、README 修正（§1.2 第 6 条） | 压测结果页不依赖 Excel 解析 |

P1 是最大的一块（约占 60% 工作量），P2/P3 各自独立可插拔。

## 8. 需要你做主的决策点

1. **子进程架构 vs 进程内库化**——推荐子进程（§2.1），代价是进度粒度 10–30s。同意吗？
2. **前端栈**——推荐 Vue3+Element Plus（中文文档友好、表单组件最全）。如果你有 React 偏好可换 Ant Design，功能等价。
3. **存储**——推荐内嵌 SQLite。若想极简到"零依赖"也可纯 JSON 文件存储，代价是列表筛选/统计要手写。
4. **部署目标**——默认按"单机（你的 Mac 或一台 Linux 服务器）+ 可选静态 token"设计。需要多用户/权限体系吗？（默认不做）
5. **独占模式默认值**——建议 performance 类型默认勾选独占（压测结果最怕同机干扰），benchmark/evaluation 不默认。认可吗？
6. **裁判模型**——默认复用一套 OpenAI 兼容端点配置，评判只发一次请求、不占测试闸门。评判结论是"自动生成"还是"默认手动点按钮"？（建议：evaluation 自动，其余手动）
