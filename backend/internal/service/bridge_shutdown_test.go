package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
