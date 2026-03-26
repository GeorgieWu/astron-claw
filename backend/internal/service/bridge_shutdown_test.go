package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

func TestShutdownPreservesNewerWorkerLiveness(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token := "sk-shutdown-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, token)

	queue := NewRedisStreamQueue(rdb, 100)
	oldBridge := NewConnectionBridge(rdb, nil, queue)
	newBridge := NewConnectionBridge(rdb, nil, queue)
	defer func() {
		oldBridge.Shutdown()
		newBridge.Shutdown()
		cleanupBridgeTestKeys(t, context.Background(), rdb, token)
	}()

	oldBot, oldClient := newTestBotConn(t)
	defer oldClient.Close()
	if err := oldBridge.RegisterBot(ctx, token, oldBot); err != nil {
		t.Fatalf("oldBridge.RegisterBot: %v", err)
	}

	newBot, newClient := newTestBotConn(t)
	defer newClient.Close()
	if err := newBridge.RegisterBot(ctx, token, newBot); err != nil {
		t.Fatalf("newBridge.RegisterBot: %v", err)
	}

	oldBridge.Shutdown()

	if !newBridge.IsBotConnected(ctx, token) {
		t.Fatal("expected newer worker to remain connected after old worker shutdown")
	}

	gen, err := rdb.Get(ctx, BotGenPrefix+token).Result()
	if err != nil {
		t.Fatalf("expected generation key to survive shutdown: %v", err)
	}
	if gen != "2" {
		t.Fatalf("generation = %s, want 2", gen)
	}
}

func TestShutdownDoesNotResetGenerationForNextTakeover(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token := "sk-next-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, token)

	queue := NewRedisStreamQueue(rdb, 100)
	bridgeA := NewConnectionBridge(rdb, nil, queue)
	bridgeB := NewConnectionBridge(rdb, nil, queue)
	bridgeC := NewConnectionBridge(rdb, nil, queue)
	defer func() {
		bridgeA.Shutdown()
		bridgeB.Shutdown()
		bridgeC.Shutdown()
		cleanupBridgeTestKeys(t, context.Background(), rdb, token)
	}()

	botA, clientA := newTestBotConn(t)
	defer clientA.Close()
	if err := bridgeA.RegisterBot(ctx, token, botA); err != nil {
		t.Fatalf("bridgeA.RegisterBot: %v", err)
	}

	botB, clientB := newTestBotConn(t)
	defer clientB.Close()
	if err := bridgeB.RegisterBot(ctx, token, botB); err != nil {
		t.Fatalf("bridgeB.RegisterBot: %v", err)
	}

	bridgeA.Shutdown()

	botC, clientC := newTestBotConn(t)
	defer clientC.Close()
	if err := bridgeC.RegisterBot(ctx, token, botC); err != nil {
		t.Fatalf("bridgeC.RegisterBot: %v", err)
	}

	gen, err := rdb.Get(ctx, BotGenPrefix+token).Result()
	if err != nil {
		t.Fatalf("get generation after third takeover: %v", err)
	}
	if gen != "3" {
		t.Fatalf("generation = %s, want 3", gen)
	}
}

func TestHeartbeatRefreshContinuesWhileCleanupRuns(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	liveToken := "sk-live-" + uuid.NewString()[:8]
	expiredToken := "sk-expired-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, liveToken)
	cleanupBridgeTestKeys(t, ctx, rdb, expiredToken)

	queue := newBlockingQueue()
	bridge := NewConnectionBridge(rdb, nil, queue)
	bridge.heartbeatInterval = 100 * time.Millisecond
	bridge.cleanupInterval = 100 * time.Millisecond
	bridge.cleanupLockTTL = 300 * time.Millisecond
	defer func() {
		queue.Release()
		bridge.Shutdown()
		cleanupBridgeTestKeys(t, context.Background(), rdb, liveToken)
		cleanupBridgeTestKeys(t, context.Background(), rdb, expiredToken)
	}()

	bot, client := newTestBotConn(t)
	defer client.Close()
	if err := bridge.RegisterBot(ctx, liveToken, bot); err != nil {
		t.Fatalf("RegisterBot: %v", err)
	}

	if err := rdb.ZAdd(ctx, BotAliveKey, redis.Z{
		Score:  float64(time.Now().Add(-BotTTL - time.Second).Unix()),
		Member: expiredToken,
	}).Err(); err != nil {
		t.Fatalf("seed expired token: %v", err)
	}

	bridge.Start()

	if !queue.WaitForDeletes(1, 2*time.Second) {
		t.Fatal("expected cleanup to start and block")
	}

	firstScore, err := rdb.ZScore(ctx, BotAliveKey, liveToken).Result()
	if err != nil {
		t.Fatalf("first ZScore: %v", err)
	}

	time.Sleep(1500 * time.Millisecond)

	secondScore, err := rdb.ZScore(ctx, BotAliveKey, liveToken).Result()
	if err != nil {
		t.Fatalf("second ZScore: %v", err)
	}

	if secondScore <= firstScore {
		t.Fatalf("expected heartbeat to keep refreshing during cleanup, first=%v second=%v", firstScore, secondScore)
	}
}

func TestCleanupLoopDoesNotOverlapAcrossWorkers(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	expiredToken := "sk-expired-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, expiredToken)

	queueState := newBlockingQueueState()
	bridgeA := NewConnectionBridge(rdb, nil, newBlockingQueueWithState(queueState))
	bridgeB := NewConnectionBridge(rdb, nil, newBlockingQueueWithState(queueState))
	bridgeA.heartbeatInterval = 100 * time.Millisecond
	bridgeA.cleanupInterval = 100 * time.Millisecond
	bridgeA.cleanupLockTTL = 300 * time.Millisecond
	bridgeB.heartbeatInterval = 100 * time.Millisecond
	bridgeB.cleanupInterval = 100 * time.Millisecond
	bridgeB.cleanupLockTTL = 300 * time.Millisecond
	defer func() {
		queueState.Release()
		bridgeA.Shutdown()
		bridgeB.Shutdown()
		cleanupBridgeTestKeys(t, context.Background(), rdb, expiredToken)
	}()

	if err := rdb.ZAdd(ctx, BotAliveKey, redis.Z{
		Score:  float64(time.Now().Add(-BotTTL - time.Second).Unix()),
		Member: expiredToken,
	}).Err(); err != nil {
		t.Fatalf("seed expired token: %v", err)
	}

	bridgeA.Start()
	bridgeB.Start()

	if !queueState.WaitForDeletes(1, 2*time.Second) {
		t.Fatal("expected first cleanup to start")
	}

	if queueState.WaitForDeletes(2, bridgeA.cleanupLockTTL+250*time.Millisecond) {
		t.Fatalf("expected cleanup lock to prevent overlapping deletes, got %d calls", queueState.DeleteCalls())
	}
}

func newLocalRedisForBridgeTest(t *testing.T) redis.UniversalClient {
	t.Helper()

	addrs := splitBridgeCSV(strings.TrimSpace(os.Getenv("REDIS_ADDRS")))
	if len(addrs) == 0 {
		addrs = []string{"127.0.0.1:6371", "127.0.0.1:6372", "127.0.0.1:6373"}
	}
	password := os.Getenv("REDIS_PASSWORD")
	if password == "" {
		password = "7|0[naI0X4"
	}

	rdb := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:    addrs,
		Password: password,
	})
	t.Cleanup(func() {
		_ = rdb.Close()
	})

	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		t.Skipf("redis unavailable for bridge shutdown integration test: %v", err)
	}
	return rdb
}

func splitBridgeCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cleanupBridgeTestKeys(t *testing.T, ctx context.Context, rdb redis.UniversalClient, token string) {
	t.Helper()
	mustBridgeRedisDel(t, ctx, rdb, BotAliveKey)
	mustBridgeRedisDel(t, ctx, rdb, CleanupLockKey)
	mustBridgeRedisDel(t, ctx, rdb, BotGenPrefix+token)
	mustBridgeRedisDel(t, ctx, rdb, BotInboxPrefix+token)
}

func mustBridgeRedisDel(t *testing.T, ctx context.Context, rdb redis.UniversalClient, key string) {
	t.Helper()
	if err := rdb.Del(ctx, key).Err(); err != nil {
		t.Fatalf("redis DEL %s: %v", key, err)
	}
}

func newTestBotConn(t *testing.T) (*BotConn, *websocket.Conn) {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	serverConnCh := make(chan *websocket.Conn, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		serverConnCh <- conn
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}

	var serverConn *websocket.Conn
	select {
	case serverConn = <-serverConnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for test websocket")
	}

	return &BotConn{Conn: serverConn}, clientConn
}

type blockingQueue struct {
	state *blockingQueueState
}

func newBlockingQueue() *blockingQueue {
	return &blockingQueue{state: newBlockingQueueState()}
}

func newBlockingQueueWithState(state *blockingQueueState) *blockingQueue {
	return &blockingQueue{state: state}
}

func (q *blockingQueue) Publish(ctx context.Context, queueName, message string) (string, error) {
	return "", nil
}

func (q *blockingQueue) Consume(ctx context.Context, queueName, group, consumer string, blockMs int) (*QueueMessage, error) {
	select {
	case <-ctx.Done():
		return nil, nil
	case <-time.After(25 * time.Millisecond):
		return nil, nil
	}
}

func (q *blockingQueue) Ack(ctx context.Context, queueName, group, messageID string) error {
	return nil
}

func (q *blockingQueue) DeleteMessage(ctx context.Context, queueName, messageID string) error {
	return nil
}

func (q *blockingQueue) DeleteQueue(ctx context.Context, queueName string) error {
	q.state.notifyDelete()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-q.state.releaseCh:
		return nil
	}
}

func (q *blockingQueue) Purge(ctx context.Context, queueName string) error {
	return nil
}

func (q *blockingQueue) EnsureGroup(ctx context.Context, queueName, group string) error {
	return nil
}

func (q *blockingQueue) WaitForDeletes(target int, timeout time.Duration) bool {
	return q.state.WaitForDeletes(target, timeout)
}

func (q *blockingQueue) Release() {
	q.state.Release()
}

type blockingQueueState struct {
	deleteCalls atomic.Int32
	deleteCh    chan struct{}
	releaseCh   chan struct{}
	releaseOnce sync.Once
}

func newBlockingQueueState() *blockingQueueState {
	return &blockingQueueState{
		deleteCh:  make(chan struct{}, 32),
		releaseCh: make(chan struct{}),
	}
}

func (s *blockingQueueState) notifyDelete() {
	s.deleteCalls.Add(1)
	select {
	case s.deleteCh <- struct{}{}:
	default:
	}
}

func (s *blockingQueueState) WaitForDeletes(target int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		if int(s.deleteCalls.Load()) >= target {
			return true
		}

		select {
		case <-deadline.C:
			return int(s.deleteCalls.Load()) >= target
		case <-s.deleteCh:
		}
	}
}

func (s *blockingQueueState) DeleteCalls() int {
	return int(s.deleteCalls.Load())
}

func (s *blockingQueueState) Release() {
	s.releaseOnce.Do(func() {
		close(s.releaseCh)
	})
}
