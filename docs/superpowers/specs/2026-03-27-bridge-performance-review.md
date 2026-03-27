# Bridge 高连接数性能优化设计方案

## 1. 背景与问题定义

现有 bridge 的核心模型是“每个 token 一个 Redis inbox + 一个 poll goroutine”。在连接规模较小时，这种模型实现简单，但在连接数上升到数千、数万后，系统瓶颈不再是单次 Redis 操作或单条 WebSocket，而是连接管理架构本身。

现网压测和问题排查中已经暴露出几类典型症状：

- `bridge:bot_alive` 增长缓慢，活连接在线标记刷新不及时。
- 滚动更新时旧 owner 清理共享状态导致新 owner 被误伤。
- cleanup 与 heartbeat 耦合，慢 cleanup 会拖住 heartbeat。
- heartbeat 中逐 token 的 generation 校验为 `O(n)`，规模上来后刷新周期显著拉长。
- 在大规模连接压测下，连接注册、注销和 per-token poll 对 Redis 和进程调度带来线性放大压力。
- 现网 2 万连接压测已出现成批 `websocket: close 1006 unexpected EOF`，说明系统正在逼近连接链路或接入层容量边界。

因此，这次优化的目标不是继续对旧模型做局部修补，而是替换 bridge 的消息分发数据平面。

## 2. 目标与非目标

### 2.1 目标

- 将 bridge 从“per-token inbox / per-token poll”重构为“per-worker inbox / local route dispatch”。
- 降低 Redis 常驻阻塞消费数量，避免随 token 数量线性增长。
- 将后台 goroutine 数量从与 token 数量相关，改为与 worker 数量和固定 consumer 数量相关。
- 保留现有 `bot_alive`、`bot_gen` 的在线与代际语义。
- 支持全网切换上线，允许秒级不可用，失败后可整体回滚。

### 2.2 非目标

- 不追求与旧版本 backend 混部兼容。
- 不在本次方案中解决 ingress/LB 层的 2 万连接异常 EOF 问题，只将应用侧架构瓶颈先降下去。
- 不在本次方案中引入全新的消息中间件，仍基于 Redis Cluster。

## 3. 方案选型与取舍

### 方案 A：保留 per-token inbox，仅做局部优化

保留 `bridge:bot_inbox:<token>` 和 `pollBotInbox()` 主路径，只优化 heartbeat、cleanup、generation 校验。

优点：

- 改动最小。
- 上线面最窄。

缺点：

- 核心瓶颈仍在。
- Redis Stream 数量、poll goroutine 数量、注册清理路径成本都还是随 token 线性增长。
- 无法支撑 10 万级连接目标。

### 方案 B：改成分片 shared inbox

把 token inbox 改成按 shard 分桶的共享 inbox。

优点：

- 相比 per-token inbox 有所缓解。

缺点：

- 仍然需要 token 到 shard 的额外映射和消费分配逻辑。
- shard 粒度和热点分布会变成新的复杂点。

### 方案 C：改成 per-worker shared inbox

本方案采用这一方案。

优点：

- 每个 worker 只维护一个共享 inbox。
- consumer 数量固定，与 token 数量解耦。
- token 只作为消息路由字段，不再拥有自己的 Redis stream。
- 责任边界更清晰：Redis 负责 owner 定位与 worker 级消息投递，worker 内部负责本地连接路由。

缺点：

- 需要引入新的 `bot_owner` 路由索引。
- 需要补一条空闲 token 的 ownership reconcile 机制，避免旧 owner 长时间留存。

综合来看，方案 C 最能从根上解决现有扩展性问题。

## 4. 新旧架构对比

### 4.1 旧架构

- 注册时：`INCR bot_gen`、写 `bot_alive`、创建 `bridge:bot_inbox:<token>`、起 `pollBotInbox()`。
- chat 下发时：直接向 `bridge:bot_inbox:<token>` 写入消息。
- bot 侧消费：每个 token 单独 `XREADGROUP BLOCK`。
- ownership 收敛：依赖 heartbeat 或 poll 路径上的 generation 检查。

问题在于：

- Redis stream 数量与 token 数量线性增长。
- 阻塞消费连接数与 token 数量线性增长。
- goroutine 数量与 token 数量线性增长。
- 注册、清理、恢复路径都容易被规模放大。

### 4.2 新架构

- 注册时：`INCR bot_gen`、`SET bot_owner`、`ZADD bot_alive`、本地保存 `b.bots[token]`。
- chat 下发时：先查 `bot_owner:<token>`，再写 `worker_inbox:<workerID>`。
- worker 消费：每个 worker 用固定数量 consumer 消费 `worker_inbox`。
- 本地分发：按消息中的 token 查 `b.bots[token]` 转发。
- ownership 收敛：新增 batched reconcile loop，对空闲 token 做分批驱逐。

### 4.3 对比图

```text
旧架构:

  [chat]
     |
     v
  [bridge:bot_inbox:<token>]
     |
     v
  [pollBotInbox goroutine]
     |
     v
  [bot ws]

新架构:

  [chat]
     |
     v
  [bridge:bot_owner:<token>]
     |
     v
  [bridge:worker_inbox:<workerID>]
     |
     v
  [固定数量 consumer]
     |
     v
  [b.bots[token]]
     |
     v
  [bot ws]
```

新架构把“消息通道数量”降到了 worker 粒度。

## 5. 核心数据结构

### 5.1 保留的 key

- `bridge:bot_alive`
  bot 在线心跳，继续用于 chat 查询 bot 是否在线。
- `bridge:bot_gen:<token>`
  ownership generation。

### 5.2 新增的 key

- `bridge:bot_owner:<token>`
  当前 token 所属 workerID。
- `bridge:worker_inbox:<workerID>`
  每个 backend worker 一个共享 stream。

## 6. 核心数据流

### 6.1 Bot 注册与路由建立

```text
[Bot 建立 WebSocket]
          |
          v
   [Bridge.RegisterBot]
          |
          v
 [INCR bridge:bot_gen:<token>]
          |
          v
    [ZADD bridge:bot_alive]
          |
          v
[SET bridge:bot_owner:<token> = workerID]
          |
          v
    [本地保存 b.bots[token]]
          |
          v
 [trackReconcileToken(token)]
```

说明：

- 新 owner 建立后，最关键的共享状态是 `bot_gen`、`bot_alive`、`bot_owner`。
- 不再创建 `bridge:bot_inbox:<token>`，也不再起 per-token poll goroutine。

### 6.2 Chat 下发流程

```text
[Chat Client: POST /chat/sse]
              |
              v
         [SSE Router]
              |
              v
      { IsBotConnected? }
         |           |
        否           是
         |           |
         v           v
[返回 400 No bot] [创建/解析 session]
                      |
                      v
               [Bridge.SendToBot]
                      |
                      v
       [GET bridge:bot_owner:<token>]
                      |
                      v
              [Resolve workerID]
                      |
                      v
     [XADD bridge:worker_inbox:<workerID>]
                      |
                      v
           [Worker Inbox Consumer]
                      |
                      v
         { 本地 b.bots[token] 是否存在? }
               |                   |
              否                   是
               |                   |
               v                   v
      [记录告警并丢弃]    [转发 JSON-RPC 到 Bot WebSocket]
```

说明：

- chat 不再直接写 `bridge:bot_inbox:<token>`。
- 路由决策点变成 `bridge:bot_owner:<token>`。
- Redis stream 的粒度从 `per-token` 变成 `per-worker`。

### 6.3 Bot 回包流程

```text
[Bot WebSocket]
       |
       v
[websocket.go ReadMessage]
       |
       v
[Bridge.HandleBotMessage]
       |
       v
   { message type }
      |         |
     ping   session/update
      |         |
      v         v
 [直接回 pong] [TranslateBotEvent]
                   |
                   v
             [提取 sessionId]
                   |
                   v
              [sendToSession]
                   |
                   v
           { Chat inbox 是否存在? }
                |             |
               否             是
                |             |
                v             v
            [跳过发送] [XADD bridge:chat_inbox:<token>:<sessionId>]
                                  |
                                  v
                             [SSE Consumer]
                                  |
                                  v
                         [返回给 Chat Client]
```

说明：

- bot 回包路径没有切到 `worker_inbox`，它仍然是“当前 ws 连接 -> session inbox”。
- chat 结果隔离边界仍然是 `token + sessionId`，所以不同 token 不会串会话。

### 6.4 空闲 Token 的 Ownership 收敛流程

```text
[Reconcile Ticker]
        |
        v
[取固定批次本地 token]
        |
        v
[读取 bridge:bot_owner:<token>]
        |
        v
 { owner == local worker? }
      |                |
     否                是
      |                |
      v                v
[evictLocal(token)] [读取 bridge:bot_gen:<token>]
                           |
                           v
                   { remoteGen > localGen? }
                        |               |
                       是               否
                        |               |
                        v               v
              [evictLocal(token)] [保留本地连接]
```

说明：

- 这条路径负责处理“空闲 token 没有业务流量时，旧 owner 如何退出”。
- 这里不是全量扫，而是固定批次扫描，避免回到旧的 `O(n)` heartbeat 模型。

### 6.5 远端断连 / 管理删除流程

```text
[Admin RemoveBotSessions]
            |
            v
        { token 在本机? }
          |           |
         是           否
          |           |
          v           v
    [关闭本地 ws] [ResolveBotRoute]
          |           |
          v           v
   [UnregisterBot] [PublishDisconnectToWorkerInbox]
          |           |
          |           v
          |    [目标 worker consumer]
          |           |
          |           v
          |    [evictLocal(token)]
          |           |
          +-----+-----+
                |
                v
 [删除 bot_alive / bot_owner / bot_gen]
                |
                v
    [异步 artifact cleanup]
```

说明：

- 远端 disconnect 不再依赖旧的 `bridge:bot_inbox:<token>` 控制消息。
- 控制面统一走 `worker_inbox`。

## 7. 性能收益预期

这次优化的核心收益不在单次延迟，而在复杂度下降。

旧架构的主要复杂度：

- Redis stream 数量：`O(token)`
- 阻塞消费数量：`O(token)`
- poll goroutine 数量：`O(token)`
- heartbeat generation 校验：旧实现曾达到 `O(token)` 读 Redis

新架构的主要复杂度：

- worker inbox stream 数量：`O(worker)`
- consumer 数量：`O(worker * fixedConsumers)`
- 本地路由查询：`O(1)` map lookup
- ownership reconcile：`O(batch)` 每轮固定上限
- heartbeat：只保留 `bot_alive` 批量刷新

预期收益：

- Redis Cluster 不再承受 1 token 1 stream 1 consumer 的压力。
- Go 进程不再为每个 token 常驻一个 poll goroutine。
- 注册路径不再初始化 per-token inbox/group。
- 清理路径异步化后，不再阻塞断连关键路径。
- 在 `5000`、`20000`、`50000` 连接规模下，应用侧瓶颈将从“连接管理架构”转移到更真实的系统边界，如 ingress/LB、socket、网络栈。

## 8. 已落地的优化点

当前实现已经完成以下核心改造：

- 修复 old worker shutdown 误删新 owner 状态的问题。
- 将 heartbeat 和 cleanup 解耦，避免 cleanup 拖住心跳。
- 去掉 heartbeat 里的逐 token generation 串行读取，降低刷新成本。
- 引入 `bridge:bot_owner:<token>` 作为 owner 路由索引。
- 引入 `bridge:worker_inbox:<workerID>` 作为共享 Redis stream。
- chat 下发改为 `bot_owner -> worker_inbox` 路由。
- 删除注册路径中的 per-token poll 主路径。
- 引入 batched reconcile loop，处理空闲 token 的旧 owner 驱逐。
- 将重型 artifact cleanup 从连接关闭关键路径中拆出，改为异步执行。
- 增加多组回归测试，覆盖 shutdown、heartbeat、worker inbox 路由隔离、disconnect 精确作用范围、reconcile 等关键行为。

## 9. 风险分析

### 9.1 应用内风险

- `bot_owner` 与本地连接状态可能短时间不一致，需要 reconcile 收敛。
- `worker_inbox` 是共享队列，如果 consumer 数量过少，可能形成单 worker 内部转发瓶颈。
- `worker_inbox` 和 `bot_owner` 当前没有 TTL，会形成一定历史残留，需要后续治理。
- Redis Cluster 下，owner 路由查找和 worker inbox 写入虽然比旧架构轻，但仍需关注热点 worker 的负载分布。

### 9.2 系统级风险

- 当前 2 万连接压测出现的大量 `1006 unexpected EOF`，从证据看更像 ingress/LB/转发链路异常断链，而不是 bridge 代码直接导致。
- 这意味着应用侧改造完成后，系统瓶颈可能前移到接入层。
- 单节点 10 万连接目标要成立，还需要同时满足：
  - ingress / LB 支持足够的 websocket 长连接数
  - node / pod socket 与网络栈参数足够
  - botload 或真实客户端的重连策略不会形成同步风暴

## 10. 兼容性与回滚策略

这次方案按“全网切换、允许秒级不可用”设计，不考虑新旧 backend 混部兼容。

### 10.1 兼容性原则

- 新增 key 不复用旧语义。
- 旧版本主要依赖 `bot_alive`、`bot_gen`、`bot_inbox:<token>`。
- 新版本新增 `bot_owner`、`worker_inbox`，不会破坏旧 key 的既有语义。

### 10.2 回滚策略

1. 发生严重问题时，先摘流或暂停 chat 请求进入新版本。
2. 全网回滚到旧版本 backend。
3. bot 重新建连后，旧版本会重新恢复 per-token inbox 路径。
4. 新增的 `bot_owner`、`worker_inbox` key 可暂时保留，不要求阻塞式清理。

### 10.3 回滚代价

- 一次全量 bot 重连。
- 秒级到分钟级短暂不可用窗口。
- 但不会因为 Redis key 语义冲突造成旧版本不可恢复。

## 11. 压测现状与待验证项

### 11.1 已验证

- 多 token worker inbox 路由隔离正确，不会发生 token 间串消息。
- disconnect 控制能精确作用到目标 token，不会误伤其他连接。
- shutdown / ownership / reconcile 等核心逻辑已有回归测试覆盖。
- 200 连接级别下，连接可稳定维持。

### 11.2 已暴露的新现象

- 2 万连接压测下出现批量 `1006 unexpected EOF`。
- backend pod 侧 fd、socket、conntrack 目前看并未打满。
- 异常更像 ingress/LB 层或接入链路批量断链，而不是 backend 进程资源先耗尽。

### 11.3 待验证项

- nginx ingress 同时承接 2 万以上 websocket 连接时的稳定性。
- ingress upstream 与 backend service 之间是否存在连接上限或超时重置。
- botload 是否需要指数退避 + jitter，避免重连风暴放大。
- 单 worker inbox consumer 数量在更高连接数下的最优配置。
- `bot_owner`、`worker_inbox` 的历史残留清理策略。

## 12. 发布建议

### 12.1 应用层发布

当前 bridge 架构改造可以继续灰度或全量验证，重点观察：

- `connected_now`
- `reconnect_total`
- `bridge:bot_alive` 有效在线数
- `worker_inbox` 消费延迟
- `bot_owner` 与本地连接数的匹配程度

### 12.2 接入层发布前检查

- 检查 nginx ingress 的 websocket 与 worker connection 相关配置。
- 检查 upstream 超时、keepalive、max worker connections。
- 检查 node 侧网络栈与连接上限。
- 必要时将 2 万压测拆成“直连 service”与“经 ingress”两组，对比定位瓶颈是否在接入层。

## 13. 结论

这次优化的本质，是把 bridge 从“以 token 为单位管理 Redis 通道”的模型，改成“以 worker 为单位管理消息收件箱、以 token 为字段做本地路由”的模型。

它解决的是旧架构在高连接数下必然出现的线性扩张问题，是支撑 10 万级连接目标的前提性改造。

当前实现已经基本完成应用侧主干重构，并通过了回归测试。下一阶段的重点不再只是 bridge 代码本身，而是：

- 补齐接入层容量验证
- 收敛 `worker_inbox` / `bot_owner` 的残留治理
- 基于 `20000`、`50000` 连接的真实压测结果继续调优 ingress、重连策略和 consumer 参数
