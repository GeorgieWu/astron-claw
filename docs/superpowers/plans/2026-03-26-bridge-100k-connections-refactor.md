# Bridge 10万连接全网切换重构计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 bridge 从当前“每 token 一个 Redis inbox + 一个 poll goroutine”的模型重构为“每 worker 一个共享 inbox + 本地连接路由”的模型，支持单节点 `10万+` 长连接保活，并允许通过一次全网切换完成上线，接受秒级不可用。

**Architecture:** 保留 `bridge:bot_alive` 和 `bridge:bot_gen:<token>` 作为在线与代际语义，新增 `bridge:bot_owner:<token>` 和 `bridge:worker_inbox:<workerID>`。bot 注册后只写 owner 和 liveness；chat 下发消息时先查 owner，再投递到 worker 级共享 inbox，由 worker 内部固定数量 consumer 分发到本地 ws 连接。移除 per-token inbox/poll 主路径。

**Tech Stack:** Go、Redis Cluster、Redis Streams、WebSocket、Gin、Go test

---

## 一、问题定义

当前架构的核心瓶颈：

- 每个 token 一个 `bridge:bot_inbox:<token>`
- 每个 token 一个 `pollBotInbox()` goroutine
- `RegisterBot()` 在全局锁下串行执行 Redis 初始化
- `UnregisterBot()` 同步执行较重清理

这导致：

- 连接数上来后 `bridge:bot_alive` 增长缓慢
- websocket 写超时
- 大量 `XREADGROUP BLOCK` 常驻连接压在 Redis 上
- 单节点连接能力受限于“连接管理架构”而不是单个局部操作

目标不是继续微调 heartbeat，而是替换消息分发数据平面。

---

## 二、目标架构

### 1. 保留的 key

- `bridge:bot_alive`
  bot 在线心跳，继续用于 chat 查询 bot 是否在线
- `bridge:bot_gen:<token>`
  ownership generation

### 2. 新增的 key

- `bridge:bot_owner:<token>`
  当前 token 所属 workerID
- `bridge:worker_inbox:<workerID>`
  每个 backend worker 一个共享 stream

### 3. 新的数据流

bot 注册：

- `INCR bridge:bot_gen:<token>`
- `SET bridge:bot_owner:<token> = workerID`
- `ZADD bridge:bot_alive`
- 本地 `b.bots[token] = conn`
- 不再为 token 创建独立 inbox / poll goroutine

chat 下发：

- 读取 `bridge:bot_owner:<token>`
- 组装消息并写入 `bridge:worker_inbox:<workerID>`
- worker 端少量固定 consumer 从共享 inbox 读取
- 根据消息里的 token 在本地查 `b.bots` 并转发到 ws

ownership 收敛：

- 新 owner 注册后覆盖 `bot_gen` 与 `bot_owner`
- 后台运行分批 reconcile loop
- 每轮只检查固定数量本地 token
- 发现 `remote owner` 或 `remote gen` 不匹配时执行 `evictLocal(token)`

---

## 三、与旧架构的关键差异

### 旧架构

- Redis 压力与 token 数量近似线性增长
- goroutine 数量与 token 数量近似线性增长
- 每个 token 自己维护一条 inbox/poll 通路

### 新架构

- Redis block consumer 数量与 worker 数量近似线性增长
- 每个 worker 只保留少量固定 consumer
- token 只作为消息路由字段，不再拥有独立 stream

目标效果：

- `5000`、`50000`、`100000` token 的主要差异体现在本地连接 map 大小和 heartbeat 批量写大小
- 不再体现在 `XREADGROUP` 数量和 poll goroutine 数量

---

## 四、全网切换约束

本方案明确不考虑新旧版本混部兼容，只考虑：

- 一次全网升级
- 升级窗口内允许秒级不可用
- 如失败，允许全网回滚到旧版本

因此可以接受：

- 新版本直接不再消费 `bridge:bot_inbox:<token>`
- chat 直接切到 `bot_owner -> worker_inbox`
- 不保留 legacy 与新路径的双写/双读逻辑

但仍需保证：

- 新旧 key 语义不冲突
- 回滚时旧版本不会被新 key 干扰
- 切换前后 Redis 清理动作可控

---

## 五、回滚策略

### 1. 新增 key 不影响旧版本

旧版本只依赖：

- `bridge:bot_alive`
- `bridge:bot_gen:<token>`
- `bridge:bot_inbox:<token>`

因此新版本新增：

- `bridge:bot_owner:<token>`
- `bridge:worker_inbox:<workerID>`

不会影响旧版本启动和运行。

### 2. 回滚动作

若新版本上线失败：

1. 停止 chat 流量进入新版本
2. 全网回滚到旧版本 backend
3. bot 重新建连后，旧版本重新建立 `bridge:bot_inbox:<token>` 与 per-token poll
4. 新 key 可保留，不需要阻塞回滚

### 3. 回滚代价

- 会有一次全量 bot 重连
- 会有短时间不可用
- 但不会因为 Redis key 语义冲突导致旧版本逻辑损坏

---

## 六、模块改造边界

### 需要修改

- Modify: `backend/internal/service/bridge.go`
  注册、注销、heartbeat、本地连接路由、ownership reconcile
- Modify: `backend/internal/service/queue.go`
  worker inbox 消费辅助
- Modify: `backend/internal/router/sse.go`
  chat -> bot 路由改为 owner 路由
- Modify: `backend/internal/router/websocket.go`
  bot 注册路径改为写 owner，不再初始化 per-token inbox
- Modify: `backend/cmd/server/main.go`
  启动 worker inbox consumer

### 建议新增

- Create: `backend/internal/service/bridge_worker_inbox.go`
- Create: `backend/internal/service/bridge_owner_store.go`
- Create: `backend/internal/service/bridge_reconcile.go`
- Create: `backend/internal/service/bridge_cleanup.go`

### 测试文件

- Modify: `backend/internal/service/bridge_shutdown_test.go`
- Create: `backend/internal/service/bridge_worker_inbox_test.go`
- Create: `backend/internal/service/bridge_owner_store_test.go`
- Create: `backend/internal/service/bridge_reconcile_test.go`

---

## 七、实施任务

### 任务 1：抽象 owner 存储与新路由入口

**Files:**
- Create: `backend/internal/service/bridge_owner_store.go`
- Modify: `backend/internal/service/bridge.go`
- Modify: `backend/internal/router/sse.go`
- Test: `backend/internal/service/bridge_owner_store_test.go`

- [ ] **步骤 1：写失败测试**

覆盖：

- 注册后可读取 `bot_owner`
- chat 可根据 `bot_owner` 解析出目标 worker inbox
- owner 不存在时返回明确错误

- [ ] **步骤 2：运行测试，确认它先失败**

Run: `cd backend && go test ./internal/service -run 'TestBotOwnerStore|TestResolveBotRoute' -count=1`
Expected: FAIL

- [ ] **步骤 3：写最小实现**

实现：

- `SetBotOwner(token, workerID)`
- `GetBotOwner(token)`
- chat 路由解析函数

- [ ] **步骤 4：运行测试，确认它通过**

Run: `cd backend && go test ./internal/service -run 'TestBotOwnerStore|TestResolveBotRoute' -count=1`
Expected: PASS

- [ ] **步骤 5：提交**

```bash
git add backend/internal/service/bridge_owner_store.go backend/internal/service/bridge.go backend/internal/router/sse.go backend/internal/service/bridge_owner_store_test.go
git commit -m "feat: add bridge owner routing store"
```

### 任务 2：实现 worker 级共享 inbox

**Files:**
- Create: `backend/internal/service/bridge_worker_inbox.go`
- Modify: `backend/internal/service/queue.go`
- Modify: `backend/cmd/server/main.go`
- Test: `backend/internal/service/bridge_worker_inbox_test.go`

- [ ] **步骤 1：写失败测试**

覆盖：

- 一个 worker inbox 可承载多个 token 消息
- consumer 数量固定，不按 token 增长
- 本地找不到 token 时消息被安全丢弃并记录日志/指标

- [ ] **步骤 2：运行测试，确认它先失败**

Run: `cd backend && go test ./internal/service -run 'TestWorkerInbox' -count=1`
Expected: FAIL

- [ ] **步骤 3：写最小实现**

实现：

- `bridge:worker_inbox:<workerID>`
- 固定数量 consumer goroutine
- 消息按 token 转发到本地 ws

- [ ] **步骤 4：运行测试，确认它通过**

Run: `cd backend && go test ./internal/service -run 'TestWorkerInbox' -count=1`
Expected: PASS

- [ ] **步骤 5：提交**

```bash
git add backend/internal/service/bridge_worker_inbox.go backend/internal/service/queue.go backend/cmd/server/main.go backend/internal/service/bridge_worker_inbox_test.go
git commit -m "feat: add bridge worker inbox consumers"
```

### 任务 3：重写 bot 注册/注销路径

**Files:**
- Modify: `backend/internal/service/bridge.go`
- Modify: `backend/internal/router/websocket.go`
- Test: `backend/internal/service/bridge_shutdown_test.go`

- [ ] **步骤 1：写失败测试**

覆盖：

- 注册后不再创建 per-token inbox consumer
- 注册后写入 `bot_gen`、`bot_owner`、`bot_alive`
- 注销时不再依赖删除 per-token inbox 才完成本地清理

- [ ] **步骤 2：运行测试，确认它先失败**

Run: `cd backend && go test ./internal/service -run 'TestRegisterBotWorkerRouting|TestUnregisterBotWithoutLegacyInbox' -count=1`
Expected: FAIL

- [ ] **步骤 3：写最小实现**

删除/替换：

- `RegisterBot()` 内的 `EnsureGroup(bot_inbox)` 和 `pollBotInbox()` 启动逻辑
- per-token inbox 作为主路径的依赖

保留：

- `bot_gen`
- `bot_alive`
- `bot_owner`
- 本地连接 map

- [ ] **步骤 4：运行测试，确认它通过**

Run: `cd backend && go test ./internal/service -run 'TestRegisterBotWorkerRouting|TestUnregisterBotWithoutLegacyInbox' -count=1`
Expected: PASS

- [ ] **步骤 5：提交**

```bash
git add backend/internal/service/bridge.go backend/internal/router/websocket.go backend/internal/service/bridge_shutdown_test.go
git commit -m "refactor: move bridge registration to worker routing"
```

### 任务 4：新增分批 ownership reconcile

**Files:**
- Create: `backend/internal/service/bridge_reconcile.go`
- Modify: `backend/internal/service/bridge.go`
- Test: `backend/internal/service/bridge_reconcile_test.go`

- [ ] **步骤 1：写失败测试**

覆盖：

- 空闲 token 的旧 owner 能在有限时间内被驱逐
- 每轮只检查固定 batch 数量 token
- reconcile 不阻塞 heartbeat

- [ ] **步骤 2：运行测试，确认它先失败**

Run: `cd backend && go test ./internal/service -run 'TestBridgeReconcile' -count=1`
Expected: FAIL

- [ ] **步骤 3：写最小实现**

实现：

- 固定 batch 大小的 reconcile loop
- 检查 `bot_owner` 和 `bot_gen`
- mismatch 时 `evictLocal(token)`

- [ ] **步骤 4：运行测试，确认它通过**

Run: `cd backend && go test ./internal/service -run 'TestBridgeReconcile' -count=1`
Expected: PASS

- [ ] **步骤 5：提交**

```bash
git add backend/internal/service/bridge_reconcile.go backend/internal/service/bridge.go backend/internal/service/bridge_reconcile_test.go
git commit -m "feat: add batched bridge reconcile loop"
```

### 任务 5：瘦身清理路径

**Files:**
- Create: `backend/internal/service/bridge_cleanup.go`
- Modify: `backend/internal/service/bridge.go`
- Test: `backend/internal/service/bridge_shutdown_test.go`

- [ ] **步骤 1：写失败测试**

覆盖：

- `UnregisterBot()` 不再同步依赖重清理
- chat inbox index 清理可异步执行
- 大量断连时不会在关键路径上堆积 Redis 慢操作

- [ ] **步骤 2：运行测试，确认它先失败**

Run: `cd backend && go test ./internal/service -run 'TestBridgeCleanup' -count=1`
Expected: FAIL

- [ ] **步骤 3：写最小实现**

将：

- `cleanupChatInboxes()` 从连接关闭关键路径移出
- 清理改为异步 scavenger 或后台批处理

- [ ] **步骤 4：运行测试，确认它通过**

Run: `cd backend && go test ./internal/service -run 'TestBridgeCleanup' -count=1`
Expected: PASS

- [ ] **步骤 5：提交**

```bash
git add backend/internal/service/bridge_cleanup.go backend/internal/service/bridge.go backend/internal/service/bridge_shutdown_test.go
git commit -m "refactor: slim bridge unregister cleanup path"
```

### 任务 6：删除 legacy 主路径

**Files:**
- Modify: `backend/internal/service/bridge.go`
- Modify: `backend/internal/router/sse.go`
- Modify: `backend/internal/router/websocket.go`
- Test: `backend/internal/service/...`

- [ ] **步骤 1：写失败测试**

覆盖：

- chat 下发完全走 `worker_inbox`
- backend 不再依赖 `bridge:bot_inbox:<token>`
- 启动后 goroutine 数量与 token 数量解耦

- [ ] **步骤 2：运行测试，确认它先失败**

Run: `cd backend && go test ./internal/service ./internal/router -run 'TestBridgeWithoutLegacyInbox' -count=1`
Expected: FAIL

- [ ] **步骤 3：写最小实现**

删除 legacy 主路径依赖：

- per-token inbox 发送
- per-token poll 主路径
- legacy 相关清理代码

- [ ] **步骤 4：运行测试，确认它通过**

Run: `cd backend && go test ./internal/service ./internal/router -run 'TestBridgeWithoutLegacyInbox' -count=1`
Expected: PASS

- [ ] **步骤 5：提交**

```bash
git add backend/internal/service/bridge.go backend/internal/router/sse.go backend/internal/router/websocket.go
git commit -m "refactor: remove legacy per-token bridge inbox path"
```

### 任务 7：压测与切换 runbook

**Files:**
- Create: `docs/runbooks/bridge-100k-cutover.md`
- Modify: `backend/cmd/botload/...` 如有必要

- [ ] **步骤 1：补压测命令与验收指标**

包含：

- `5000 / 20000 / 50000 / 100000` 分层压测
- 连接成功率
- `bridge:bot_alive` 收敛速度
- goroutine 数量
- Redis ops / latency

- [ ] **步骤 2：补全网切换步骤**

必须包含：

- 摘流顺序
- 停服顺序
- Redis key 预清理范围
- 新版本起服顺序
- bot 回连观察指标
- 回滚动作

- [ ] **步骤 3：提交**

```bash
git add docs/runbooks/bridge-100k-cutover.md
git commit -m "docs: add bridge 100k cutover runbook"
```

---

## 八、验收指标

### 架构级指标

- backend goroutine 数量不再与 token 数量近似线性增长
- Redis `XREADGROUP BLOCK` 数量近似为 `worker 数 * consumer 数`
- `RegisterBot` p95 显著下降

### 连接级指标

- `5000` 连接时 `bridge:bot_alive` 增长速度接近建连速度
- `50000` 连接时无大规模 websocket write timeout
- `100000` 连接时仍能维持稳定 heartbeat 和重连

### 业务级指标

- chat 发消息到 bot 的转发成功率稳定
- ownership 切换后旧 owner 能被有限时间内驱逐

---

## 九、全网切换步骤

### 正式切换前

1. 完成 pre 压测
2. 确认 Redis Cluster 指标稳定
3. 确认新版本 worker inbox 消费正常
4. 预先准备 bot 大规模重连窗口

### 切换时

1. 先摘 chat 入口流量
2. 再停旧 backend
3. 如有必要，清理旧的 `bridge:bot_inbox:*` 残留 key
4. 启动新 backend
5. 放开 bot 回连
6. 最后恢复 chat 入口流量

### 回滚时

1. 再次摘 chat 流量
2. 停新 backend
3. 启动旧 backend
4. 等待 bot 重连回旧路径
5. 恢复 chat 流量

---

## 十、明确不做的事

- 不再为每个 token 保留独立 inbox/poll 作为长期主路径
- 不通过“每 token 一个定时器”解决 ownership 问题
- 不恢复 heartbeat 全量扫描 `bot_gen`
- 不承诺在旧架构上通过局部优化实现单节点 `10万+`
