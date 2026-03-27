# Bridge Cleanup 解耦实施计划

> **给执行型 agent 的要求：** 必须使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans` 按任务逐步执行。步骤使用 `- [ ]` 复选框语法跟踪。

**目标：** 即使过期 bot 清理很慢，也要保证活跃 bot 的 heartbeat 持续刷新，并阻止多 worker 重叠执行 cleanup。

**架构：** 将 bridge 的维护逻辑拆成两个独立循环。heartbeat 循环只负责跨 worker 驱逐检查和 `bridge:bot_alive` 刷新；cleanup 循环独立持有更长生命周期的 cleanup lock，专门处理过期 bot 删除。

**技术栈：** Go、Redis、WebSocket bridge、Go test

---

### 任务 1：先用测试锁住回归

**Files:**
- Modify: `backend/internal/service/bridge_shutdown_test.go`
- Test: `backend/internal/service/bridge_shutdown_test.go`

- [ ] **步骤 1：先写失败测试**

补一个偏集成的测试，人为让 cleanup 变慢，并断言在 cleanup 执行期间，活跃 bot 的 `bridge:bot_alive` 分值仍然持续推进。

- [ ] **步骤 2：运行测试，确认它先失败**

Run: `GOCACHE=/tmp/go-build GOTMPDIR=/tmp/go-tmp go test ./backend/internal/service -run TestHeartbeatRefreshContinuesWhileCleanupRuns -count=1`
Expected: FAIL，因为当前 heartbeat 循环会被 cleanup 阻塞。

- [ ] **步骤 3：写最小实现**

重构 `ConnectionBridge`，让 heartbeat 刷新和 cleanup 跑在独立的 ticker/goroutine 中，并把 cleanup 抢锁逻辑移动到 cleanup 循环内部。

- [ ] **步骤 4：运行测试，确认它通过**

Run: `GOCACHE=/tmp/go-build GOTMPDIR=/tmp/go-tmp go test ./backend/internal/service -run TestHeartbeatRefreshContinuesWhileCleanupRuns -count=1`
Expected: PASS

- [ ] **步骤 5：提交**

```bash
git add backend/internal/service/bridge.go backend/internal/service/bridge_shutdown_test.go docs/superpowers/plans/2026-03-26-bridge-cleanup-decoupling.md
git commit -m "fix: decouple bridge cleanup from heartbeat"
```

### 任务 2：防止 cleanup 重叠执行

**Files:**
- Modify: `backend/internal/service/bridge.go`
- Modify: `backend/internal/service/bridge_shutdown_test.go`
- Test: `backend/internal/service/bridge_shutdown_test.go`

- [ ] **步骤 1：先写失败测试**

补一个测试：启动一个很慢的 cleanup，让它执行时间超过旧的 `10s` 锁 TTL，并断言第二个 worker 不会并发执行同一轮 cleanup。

- [ ] **步骤 2：运行测试，确认它先失败**

Run: `GOCACHE=/tmp/go-build GOTMPDIR=/tmp/go-tmp go test ./backend/internal/service -run TestCleanupLoopDoesNotOverlapAcrossWorkers -count=1`
Expected: FAIL，因为当前 cleanup lock 会在 cleanup 尚未完成时过期。

- [ ] **步骤 3：写最小实现**

为 cleanup 引入独立的锁 TTL，并把 cleanup 执行从 heartbeat 循环中拆出来，保证同一时间只有一个 worker 持有 cleanup 执行权。

- [ ] **步骤 4：运行测试，确认它通过**

Run: `GOCACHE=/tmp/go-build GOTMPDIR=/tmp/go-tmp go test ./backend/internal/service -run TestCleanupLoopDoesNotOverlapAcrossWorkers -count=1`
Expected: PASS

- [ ] **步骤 5：提交**

```bash
git add backend/internal/service/bridge.go backend/internal/service/bridge_shutdown_test.go docs/superpowers/plans/2026-03-26-bridge-cleanup-decoupling.md
git commit -m "test: cover bridge cleanup coordination"
```

### 任务 3：跑聚焦回归用例

**Files:**
- Modify: `backend/internal/service/bridge.go`
- Modify: `backend/internal/service/bridge_shutdown_test.go`

- [ ] **步骤 1：运行针对性的 bridge 测试**

Run: `GOCACHE=/tmp/go-build GOTMPDIR=/tmp/go-tmp go test ./backend/internal/service -run 'Test(ShutdownPreservesNewerWorkerLiveness|ShutdownDoesNotResetGenerationForNextTakeover|HeartbeatRefreshContinuesWhileCleanupRuns|CleanupLoopDoesNotOverlapAcrossWorkers)' -count=1`
Expected: PASS

- [ ] **步骤 2：运行包级验证**

Run: `GOCACHE=/tmp/go-build GOTMPDIR=/tmp/go-tmp go test ./backend/internal/service ./backend/internal/router -count=1`
Expected: PASS
