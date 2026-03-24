# Go 移植计划：Gauge Worker 隔离 + Bot WS 优雅关闭

本文档描述将 Python 端两个提交移植到 Go 后端的方案。

## 原始提交

1. **76ab134** `fix: use hostname:pid for gauge worker isolation in clusters`
2. **d61020d** `Harden bot WS shutdown and draining`

---

## 提交 1：Gauge Worker 隔离（hostname:pid）

### 问题

多 Pod 部署时，不同容器的 PID 可能相同（如容器 PID 1），导致 gauge 数据互相覆盖。

### Python 改动摘要

- `redis_exporter.py`：将 `self._pid = os.getpid()` 改为 `self._worker_id = f"{socket.gethostname()}:{os.getpid()}"`, `GaugeKey` 参数从 `pid` 改名为 `worker_id`
- `reader.py`：变量名从 `pid` 改为 `worker_id`（纯重命名，逻辑不变）

### Go 改动计划

**文件：`backend/internal/infra/telemetry/redis_exporter.go`**

1. 新增 `import "os"` (已有) 和 `"os"` (已有)。需要新增 `"os"` (已有) — 实际只需新增 `"os"` 和确认有没有 hostname 函数。Go 的 `os.Hostname()` 即可。
2. 新增 `workerID()` 函数：返回 `hostname:pid` 格式字符串。
3. `RedisMetricExporter.pid` 字段重命名为 `workerID`。
4. `NewRedisMetricExporter` 中 `pid: strconv.Itoa(os.Getpid())` 改为 `workerID: workerID()`。
5. `GaugeKey` 函数参数名从 `pid` 改为 `workerID`（保持兼容，函数签名参数名无外部影响）。
6. `Export` 方法中 `e.pid` 改为 `e.workerID`。

**文件：`backend/internal/infra/telemetry/reader.go`**

7. 变量名重命名（与 Python 对应）：`gaugePIDs` → `workerIDs`, `deadPIDs` → `deadWorkers`, `pid` → `wid`，日志信息中的 "pid" 改为 "worker"。

### 测试

运行 `cd backend && go test ./internal/infra/telemetry/...`，确认编译通过。这个改动主要是变量重命名 + hostname:pid 格式变化，没有逻辑变更。

---

## 提交 2：Harden Bot WS Shutdown and Draining

### 问题

关闭时直接清理 Redis 状态，但如果另一个 worker 已经接管了某个 token，当前 worker 的 shutdown 会错误地删除新 owner 的 Redis 数据。

### Python 改动摘要

- `bridge.py`：
  - 新增 `_draining` 布尔标志和 `begin_drain()` / `is_draining()` 方法
  - `shutdown()` 设置 `_draining = True`，关闭连接后改用 `unregister_bot()` 替代手动 Redis 清理，利用其中的 generation 守卫
- `websocket.py`：在 token 验证通过后、`accept()` 前检查 `is_draining()`，若是则 accept 后立即关闭并返回

### Go 改动计划

**文件：`backend/internal/service/bridge.go`**

1. 新增 `draining atomic.Bool` 字段到 `ConnectionBridge` 结构体。
2. 新增 `BeginDrain()` 方法：`b.draining.Store(true)`。
3. 新增 `IsDraining() bool` 方法：`return b.draining.Load()`。
4. 修改 `Shutdown()` 方法：
   - 在设置 `shuttingDown` 后也设置 `draining`
   - 先遍历所有 bot 发送 close frame
   - 然后再次遍历用 `UnregisterBot()` 做 Redis 清理（利用 generation 守卫防止清理新 owner 数据）
   - 保留已有的 cancel poll tasks 和 `wg.Wait()` 逻辑

**文件：`backend/internal/router/websocket.go`**

5. 在 token 验证通过后、WS upgrade 前，检查 `app.Bridge.IsDraining()`。若 draining：升级连接 → 发送 close frame (4000 ServerRestart) → return。

**文件：`backend/internal/service/bridge_test.go`**

6. 新增 `TestBeginDrain` 测试：验证初始 `IsDraining() == false`，`BeginDrain()` 后 `IsDraining() == true`。

**文件：`backend/cmd/server/main.go`**

7. 在收到 shutdown 信号后、调用 `srv.Shutdown()` 前，调用 `bridge.BeginDrain()`。这样在 HTTP server 的 graceful shutdown 期间（等待现有连接结束），新的 bot 连接会被拒绝。

### 测试

运行 `cd backend && go test ./internal/service/...`，确认编译和测试通过。

---

## 实施顺序

按原始提交顺序创建两个独立提交：

1. **Commit 1**: `fix: use hostname:pid for gauge worker isolation in clusters` — 修改 `redis_exporter.go` + `reader.go`
2. **Commit 2**: `feat: harden bot WS shutdown and draining` — 修改 `bridge.go` + `websocket.go` + `bridge_test.go` + `main.go`

每个提交完成后运行对应的测试验证。
