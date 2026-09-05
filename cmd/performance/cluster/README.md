# performance-cluster 多机分布式压测工具

[performance](../README.md) 压测工具的多机分布式版本：coordinator/agent 强协调架构，把并发档位切分到多台内网机器分摊发压负载，原始样本统一回收后按与单机版**完全一致的口径**聚合、输出终端报告与 Excel。适用于单机顶不住的高并发档位（如 5000 全局并发）。

## 架构

```
┌────────────────────┐   HTTP+JSON (内网)   ┌──────────────────┐
│ coordinator (run)  │ ───────────────────► │ agent :7070      │──► 上游 API
│  - 读配置、切分并发 │                      ├──────────────────┤
│  - 轮询聚合进度/TUI │ ───────────────────► │ agent :7070      │──► 上游 API
│  - 回收样本统一聚合 │                      ├──────────────────┤
│  - 报告 + Excel    │ ───────────────────► │ agent :7070      │──► 上游 API
└────────────────────┘                      └──────────────────┘
```

- **agent**：常驻守护进程，单任务互斥；档位任务在本机执行单机版同款的 `runner.RunLevel`，热路径只做本地原子计数
- **coordinator**：镜像单机版主循环（preflight → warmup → 逐档 → cooldown），每档把全局并发按节点数切分下发、1s 轮询各 agent 的计数快照喂 TUI、档位结束后回收原始样本合并聚合
- **正确性关键**：分位数不可跨机合并——agent 回传原始 `RequestMetrics` 样本，coordinator 拼接后统一走单机版的 `metrics.AggregateMetrics`，数值口径与单机版逐字段一致
- **时钟**：不要求节点间 NTP 对齐。agent 样本按「相对本机档位起点的偏移」归一到 coordinator 时间轴（同机 wall-clock 相减消去跨机偏斜）
- **全局早停**：agent 本地早停判定关闭，coordinator 汇总所有节点的累计错误率统一判定，触发后广播取消该档位的全部分片

## 构建

在仓库根目录执行：

```bash
make build-performance-cluster                          # coordinator 侧（默认 darwin/amd64）
make build-performance-cluster GOOS=linux GOARCH=amd64  # agent 部署到 Linux 节点
```

产物：`build/performance-cluster/performance-cluster-<GOOS>_<GOARCH>` 与配置模板 `config.yaml`。agent 与 coordinator 是**同一个二进制**，只需按平台各编译一份。

## 使用

### 1. 各压测节点启动 agent

```bash
./performance-cluster-linux_amd64 agent -listen :7070
# 可选共享密钥（与配置里的 cluster.auth_token 一致）：
./performance-cluster-linux_amd64 agent -listen :7070 -token my-secret
```

agent 无配置文件，压测参数（含 API token）随任务由 coordinator 通过内网明文下发——**请确保集群运行在可信内网**。

节点准备建议：`ulimit -n` 调到远高于本机最大并发分片（如 `ulimit -n 65536`），否则高并发档位会先撞上 fd 上限而不是被测服务的真实瓶颈。

### 2. coordinator 发起压测

```bash
cp configs/config.example.yaml config.yaml   # 修改 cluster.agents / models / token_groups
./performance-cluster-darwin_amd64 run -config config.yaml
```

配置与单机版一致，新增 `cluster` 段；`concurrency` 档位语义变为**全局总并发**，coordinator 按节点数均分（余数逐台 +1）：

```yaml
cluster:
  agents: ["10.0.0.1:7070", "10.0.0.2:7070", "10.0.0.3:7070", "10.0.0.4:7070"]
  auth_token: ""      # 可选
  poll_interval: 1s   # 进度轮询间隔
  agent_timeout: 10s  # 连续无响应判定失联
concurrency: [1000, 2000, 3000, 4000, 5000]  # 5000 ÷ 4 节点 = 每台 1250
```

TUI/纯文本控制台、终端汇总报告、Excel 导出（默认 `bench-cluster-<时间戳>.xlsx`）均与单机版相同；Excel 总览 sheet 额外包含节点数与各节点最大并发分片。

## 运行流程

1. **探活**：逐台 `ping`，校验协议版本一致且空闲，任一不可达即中止
2. **会话建立**：下发各节点在整个 run 中的最大并发分片，agent 据此一次性配置连接池
3. **预检**：**每台 agent** 都对全部模型做一次连通性预检（验证各机到上游的网络路径），任一失败即中止；终端里模型名会标注 `@agent地址`
4. **正式测试**：逐档切分下发 → 各 agent 在统一的全局 ramp 窗口内错峰启动 worker → coordinator 轮询聚合进度 → 档位结束回收原始样本合并聚合；warmup/cooldown/跳档语义与单机版一致
5. **收尾**：通知各 agent 结束会话，打印各节点本机的请求错误日志位置（`bench-agent-<runID>-request-errors.jsonl`，在 agent 的工作目录）

`Ctrl+C` 两段式与单机版一致：第一次广播取消并输出已完成部分的报告，第二次直接终止。

## 容错语义（v1）

- 探活/会话/预检阶段任一 agent 不可达 → **开测前中止**
- 档位运行中某 agent 连续 `agent_timeout` 无响应 → 判失联 → 广播取消 → **整轮以错误终止**（已完成档位照常出报告）。不做部分接受：标称 5000 并发实际只剩 3750 的数据有误导性
- 结果回收失败自动重试 3 次
- agent 侧看门狗：运行中任务 60s 收不到任何 progress 轮询（coordinator 消失）→ 自我取消止损

## 与单机版的关系

- 复用 `cmd/performance/internal/*` 的全部核心逻辑（请求发压、SSE 解析、指标聚合、报表、TUI），本工具只新增编排与传输层（`cluster/internal/{proto,agentd,coord}`）
- 单机版行为不受影响；`cluster` 配置段在单机版中合法但被忽略
- `gpt-image-2` 硬编码排除名单、token_group 校验规则等与单机版一致
