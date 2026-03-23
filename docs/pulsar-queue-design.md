# Pulsar 消息队列替换方案设计

## 一、问题分析

### 当前架构
```
前端 ←→ SSE (HTTP长连接) ←→ Redis Stream Queue ←→ WebSocket ←→ Bot
```

数据流:
1. 前端 POST `/bridge/chat` → SSE handler 接收请求
2. SSE handler 通过 `SendToBot()` 将消息发布到 `bridge:bot_inbox:{token}` (Redis Stream)
3. `pollBotInbox()` goroutine 从 Redis Stream 消费并通过 WebSocket 转发给 Bot
4. Bot 回复通过 WebSocket → `HandleBotMessage()` → `sendToSession()` 发布到 `bridge:chat_inbox:{token}:{sessionId}` (Redis Stream)
5. SSE handler 的循环通过 `Queue.Consume()` 从 chat inbox 消费事件并推送给前端

### 核心问题
当 WebSocket 断开时, `UnregisterBot()` 调用 `NotifyBotDisconnected()` — **这是一个空操作(仅打日志)**。然后调用 `cleanupChatInboxes()` 直接删除所有 chat inbox Redis Stream。

导致 SSE handler 中的 `Queue.Consume()` 循环:
- 发现 stream 被删除 → 触发 NOGROUP → 自动重建空 stream → 无限空轮询
- 直到 10 分钟超时才关闭, 前端完全不知道 Bot 已断开

## 二、方案设计: 引入 Apache Pulsar

### 为什么使用 Pulsar 替代 Redis Stream

| 维度 | Redis Stream | Pulsar |
|------|-------------|--------|
| 持久化 | 内存为主, maxlen 截断 | 分层存储 (BookKeeper), 天然持久 |
| 多消费者 | Consumer Group 但手动管理 | 原生 Subscription (Exclusive/Shared/Failover) |
| 断开感知 | 无原生机制, 需手动注入 | Producer 关闭 → 可监听 Topic 终止信号 |
| 跨进程/跨节点 | 依赖同一 Redis 实例 | 天然分布式, 多租户隔离 |
| 消息回溯 | 有限 | 支持按时间/ID 回溯 |
| TTL/过期 | 需手动 XTRIM | 原生 TTL (retention policy) |

### 架构变更概览

```
前端 ←→ SSE Handler ←→ Pulsar (chat_inbox topic) ←→ Bridge ←→ Pulsar (bot_inbox topic) ←→ WS Handler ←→ Bot
                              ↑                                        ↑
                              └── Bot断开时: 发送 error event ──────────┘
                                  + Producer.Close() 触发消费侧感知
```

### 2.1 MessageQueue 接口扩展

现有接口 (`service/queue.go`):
```go
type MessageQueue interface {
    Publish(ctx context.Context, queueName, message string) (string, error)
    Consume(ctx context.Context, queueName, group, consumer string, blockMs int) (*QueueMessage, error)
    Ack(ctx context.Context, queueName, group, messageID string) error
    DeleteMessage(ctx context.Context, queueName, messageID string) error
    DeleteQueue(ctx context.Context, queueName string) error
    Purge(ctx context.Context, queueName string) error
    EnsureGroup(ctx context.Context, queueName, group string) error
}
```

扩展方案:
```go
type MessageQueue interface {
    // 现有方法保持不变
    Publish(ctx context.Context, queueName, message string) (string, error)
    Consume(ctx context.Context, queueName, group, consumer string, blockMs int) (*QueueMessage, error)
    Ack(ctx context.Context, queueName, group, messageID string) error
    DeleteMessage(ctx context.Context, queueName, messageID string) error
    DeleteQueue(ctx context.Context, queueName string) error
    Purge(ctx context.Context, queueName string) error
    EnsureGroup(ctx context.Context, queueName, group string) error

    // 新增: 关闭队列 (Pulsar 用于关闭 Producer/发送终止标记)
    Close() error
}
```

### 2.2 新增 PulsarQueue 实现

新文件: `internal/service/queue_pulsar.go`

```go
type PulsarQueue struct {
    client    pulsar.Client
    producers sync.Map  // queueName -> pulsar.Producer
    consumers sync.Map  // "queueName:group:consumer" -> pulsar.Consumer
    mu        sync.RWMutex
}

func NewPulsarQueue(serviceURL string, options ...pulsar.ClientOptions) (*PulsarQueue, error)
```

**Topic 命名映射:**
- `bridge:bot_inbox:{token}` → `persistent://astron-claw/bridge/bot-inbox-{token}`
- `bridge:chat_inbox:{token}:{sessionId}` → `persistent://astron-claw/bridge/chat-inbox-{token}-{sessionId}`

**关键实现细节:**

1. **Publish**: 懒创建 Producer, 缓存在 sync.Map
2. **Consume**: 懒创建 Consumer (使用 Subscription Name = group), 使用 `consumer.Receive(ctx)` 阻塞接收, 超时通过 `context.WithTimeout`
3. **Ack**: 保存 `pulsar.MessageID` 映射, 调用 `consumer.AckID(msgID)`
4. **EnsureGroup**: Pulsar 中自动创建 Subscription, 此方法变为 no-op
5. **DeleteQueue/Purge**: 使用 Pulsar Admin API 删除/清空 topic
6. **Close**: 关闭所有 Producer 和 Consumer

### 2.3 解决核心问题: WebSocket 断开 → SSE 感知

**方案: Bot 断开时, 向所有活跃 chat inbox 注入 error 事件**

修改 `NotifyBotDisconnected()`:

```go
func (b *ConnectionBridge) NotifyBotDisconnected(token string) {
    log.Info().Str("token", pkg.SafePrefix(token, 10)).Msg("Bot status -> disconnected")

    // 向所有活跃的 chat inbox 发送断开通知
    ctx := context.Background()
    idxKey := ChatInboxIdxPrefix + token
    inboxes, err := b.rdb.SMembers(ctx, idxKey).Result()
    if err != nil {
        log.Warn().Err(err).Msg("Failed to read chat inbox index for disconnect notification")
        return
    }

    errorEvent := `{"type":"error","content":"Bot disconnected"}`
    for _, inbox := range inboxes {
        if _, err := b.queue.Publish(ctx, inbox, errorEvent); err != nil {
            log.Warn().Err(err).Str("inbox", inbox).Msg("Failed to send disconnect event to chat inbox")
        }
    }
}
```

**调用时序修改 (在 `UnregisterBot` 中):**

```
当前:  NotifyBotDisconnected(仅日志) → cleanupChatInboxes(删stream)
修改:  NotifyBotDisconnected(注入error) → 延迟一小段时间 → cleanupChatInboxes(删stream)
```

这样 SSE handler 在 `Consume()` 中收到 `{"type":"error","content":"Bot disconnected"}` → 触发 `eventType == "error"` 终止条件 (sse.go:313) → 立即关闭 SSE 流并通知前端。

### 2.4 配置变更

`config.go` 中的 `QueueConfig`:
```go
type QueueConfig struct {
    Type         string // "redis_stream" | "pulsar"
    MaxStreamLen int    // Redis Stream 专用
    BlockMs      int

    // Pulsar 配置
    PulsarURL    string // e.g. "pulsar://localhost:6650"
    PulsarTenant string // e.g. "astron-claw"
    PulsarNS     string // e.g. "bridge"
}
```

环境变量:
```
QUEUE_TYPE=pulsar
PULSAR_URL=pulsar://localhost:6650
PULSAR_TENANT=astron-claw
PULSAR_NAMESPACE=bridge
```

`NewQueue` 工厂方法扩展:
```go
func NewQueue(cfg QueueConfig, rdb redis.UniversalClient) (MessageQueue, error) {
    switch cfg.Type {
    case "redis_stream":
        return NewRedisStreamQueue(rdb, cfg.MaxStreamLen), nil
    case "pulsar":
        return NewPulsarQueue(cfg.PulsarURL, cfg.PulsarTenant, cfg.PulsarNS)
    default:
        return nil, fmt.Errorf("unsupported queue type: %q", cfg.Type)
    }
}
```

### 2.5 Redis 残留状态处理

即使切换到 Pulsar 作为消息队列, 以下 Redis 功能仍保留:
- **`bridge:bot_alive` (ZSET)**: Bot 心跳检测 — 与 MQ 无关
- **`bridge:bot_gen:{token}`**: 跨 worker 的 generation 号 — 与 MQ 无关
- **`bridge:chat_inbox_idx:{token}` (SET)**: 跟踪活跃 chat inbox — 仍需要, 用于断开通知
- **`bridge:cleanup_lock`**: 分布式锁 — 与 MQ 无关

## 三、实施步骤

### Step 1: 修复核心 Bug (不依赖 Pulsar)
- 修改 `NotifyBotDisconnected()` 在清理前注入 error 事件
- 修改 `UnregisterBot()` 的清理顺序, 确保 error 事件先送达
- 这一步可以立即解决问题, 使用现有 Redis Stream

### Step 2: 新增 PulsarQueue 实现
- 新建 `internal/service/queue_pulsar.go`
- 实现 `MessageQueue` 接口
- 添加 Pulsar Go client 依赖: `github.com/apache/pulsar-client-go`

### Step 3: 配置和工厂集成
- 扩展 `QueueConfig` 和 `config.go`
- 修改 `NewQueue` 工厂方法
- 修改 `main.go` 初始化逻辑

### Step 4: 测试验证
- 单元测试: PulsarQueue 各方法
- 集成测试: Bot 断开 → SSE 收到 error 事件 → 前端感知
- 兼容测试: `QUEUE_TYPE=redis_stream` 回退正常

## 四、依赖清单

| 依赖 | 说明 |
|------|------|
| `github.com/apache/pulsar-client-go` | Pulsar Go 客户端 |
| Apache Pulsar 实例 | 部署或容器化 (docker-compose 可用) |

## 五、风险和注意事项

1. **Pulsar 运维复杂度**: 需要部署 ZooKeeper + BookKeeper + Broker, 建议先用 standalone 模式
2. **Topic 自动清理**: 需配置 Pulsar 的 `retentionPolicies` 和 `messageTTL`, 避免 topic 无限增长
3. **向后兼容**: 通过 `QUEUE_TYPE` 环境变量切换, Redis Stream 作为默认和回退
4. **延迟**: Pulsar 的消息延迟略高于 Redis Stream (网络多一跳), 但对 SSE 场景可接受
