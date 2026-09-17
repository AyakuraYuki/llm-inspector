 ▐▛███▛█   Claude Code v2.1.274
▝▜██████▀  kimi-k3 · API Usage Billing
  ▝▝ ▝▝    ~/go/src/github.com/AyakuraYuki/llm-inspector


❯ @cmd/performance/configs/config.example.yaml 的                               
  request_rate、load_mode、concurrency 的关系令人困惑，在 load_mode: open 时    
  request_rate                                                                  
  到底代表什么？是「预期RPS」还是「启动时的concurrency」？请你解释一下，并给出  
  @cmd/performance/internal/runner/runner.go 的 runLevelOpenLoop 和             
  runLevelClosedLoop 的区别。                                                   
  ⎿  Read cmd/performance/configs/config.example.yaml (162 lines)
  ⎿  Read cmd/performance/internal/runner/runner.go (389 lines)

  Thought for 31s (ctrl+o to expand)

⏺ load_mode: open 时 request_rate 是什么

  是**「预期 RPS」（目标到达速率）**，不是启动并发数。这两个参数在 open
  模式下是「双控」关系，注释和代码都写得很明确：

  - request_rate[i] = 第 i 档要维持的每秒请求数——压测端按泊松过程以这个速率持续"
    到达"新请求，不等已发出的请求返回（config.example.yaml:50-56）
  - concurrency[i] = 第 i 档同时在途请求数的上限——只起保险丝作用，防止服务端跟不
    上时压测机本地协程/连接无限堆积

  困惑的根源在于 concurrency 在两种模式下语义不同：

  ┌─────────────┬────────────────────┬─────────────────────────────────────┐
  │             │   closed（默认）   │                open                 │
  ├─────────────┼────────────────────┼─────────────────────────────────────┤
  │             │ 负载强度本身：work │                                     │
  │ concurrency │ er                 │ 在途上限：只是安全阀，不决定发多快  │
  │             │ 协程数，决定发多快 │                                     │
  ├─────────────┼────────────────────┼─────────────────────────────────────┤
  │ request_rat │ 不用               │ 负载强度本身：真正的目标 RPS        │
  │ e           │                    │                                     │
  ├─────────────┼────────────────────┼─────────────────────────────────────┤
  │ 谁决定实际  │ 并发数 ÷           │ request_rate（主动），除非撞上在途  │
  │ QPS         │ 单请求耗时（被动） │ 上限                                │
  └─────────────┴────────────────────┴─────────────────────────────────────┘

  代码上的衔接在 levelTargetRate（runner.go:102-107）：closed 模式恒返回 0，open
  模式取 cfg.RequestRate[i]；RunLevel（runner.go:250-254）据此分流——targetRate 
  > 0 走 open-loop，否则走 closed-loop。

  runLevelClosedLoop vs runLevelOpenLoop

  runLevelClosedLoop（runner.go:286-331）——固定 worker 池，等响应才发下一个

  - 恰好启动 concurrency 个协程，每个循环执行「发请求 → 等完整响应 →
    think_time（默认 300ms）→ 发下一个」
  - 有 ramp 错峰启动：第 i 个 worker 延迟 ramp*i/concurrency 再发首请求，把 TLS
    握手摊开，避免冷启动污染 P95/P99（runner.go:290-297）
  - 致命弱点是 Coordinated Omission：服务变慢时每个 worker 都在等响应，发送端自
    动降速，测出来的吞吐/延迟偏乐观——那不是服务扛住了，是压测端在让路
  - 适用：粗看「并发越高慢多少」的对比压测

  runLevelOpenLoop（runner.go:344-388）——泊松到达，到点即发

  - 单个分发循环，用逆变换采样 dt = -ln(1-U)/rate
    生成指数分布到达间隔（泊松过程的定义），每到一个点就新起一个协程发请求（runn
    er.go:364-370），从不在发送路径上等响应
  - concurrency 退化为带缓冲 channel 信号量（runner.go:345-347）：在途数满时，下
    一次到达阻塞等空位——保留开环到达语义的同时给本地堆积兜底（对标 AIPerf 的
    request-rate + max-concurrency 双控）
  - open_loop_unbounded: true
    时直接跳过信号量（runner.go:346），严格开环、永不阻塞，对标 evalscope
    --open-loop，代价是过载时本地无限堆积
  - 不需要 ramp：到达时刻本身已被泊松过程打散（runner.go:236-238 注释）
  - think_time 不适用——开环语义里请求到达节奏只由 request_rate 决定

  一句话总结：closed 测「N 个永不疲倦的用户能压出多少」，open 测「线上每秒真来 X
  个请求时服务表现如何」。已知生产 QPS、要验证扛不扛得住或找饱和点时用
  open；request_rate 就是那个 X。

✻ Cooked for 55s · done 10:08 AM