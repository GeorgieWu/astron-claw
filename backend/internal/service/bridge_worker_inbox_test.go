package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestWorkerInboxForwardsMessageToLocalBot(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token := "sk-worker-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, token)

	queue := NewRedisStreamQueue(rdb, 100)
	bridge := NewConnectionBridge(rdb, nil, queue)
	bridge.workerInboxConsumers = 1
	defer func() {
		bridge.Shutdown()
		cleanupBridgeTestKeys(t, context.Background(), rdb, token)
	}()

	bot, client := newTestBotConn(t)
	defer client.Close()
	if err := bridge.RegisterBot(ctx, token, bot); err != nil {
		t.Fatalf("RegisterBot: %v", err)
	}

	bridge.Start()

	rpcReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "req-" + uuid.NewString()[:8],
		"method":  "session/prompt",
	}
	if err := bridge.PublishToWorkerInbox(ctx, bridge.workerID, token, rpcReq); err != nil {
		t.Fatalf("PublishToWorkerInbox: %v", err)
	}

	_, raw, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got["method"] != rpcReq["method"] {
		t.Fatalf("method = %v, want %v", got["method"], rpcReq["method"])
	}
}

func TestWorkerInboxForwardsMessagesForMultipleTokens(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tokenA := "sk-worker-a-" + uuid.NewString()[:8]
	tokenB := "sk-worker-b-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, tokenA)
	cleanupBridgeTestKeys(t, ctx, rdb, tokenB)

	queue := NewRedisStreamQueue(rdb, 100)
	bridge := NewConnectionBridge(rdb, nil, queue)
	bridge.workerInboxConsumers = 1
	defer func() {
		bridge.Shutdown()
		cleanupBridgeTestKeys(t, context.Background(), rdb, tokenA)
		cleanupBridgeTestKeys(t, context.Background(), rdb, tokenB)
	}()

	botA, clientA := newTestBotConn(t)
	defer clientA.Close()
	if err := bridge.RegisterBot(ctx, tokenA, botA); err != nil {
		t.Fatalf("RegisterBot tokenA: %v", err)
	}

	botB, clientB := newTestBotConn(t)
	defer clientB.Close()
	if err := bridge.RegisterBot(ctx, tokenB, botB); err != nil {
		t.Fatalf("RegisterBot tokenB: %v", err)
	}

	bridge.Start()

	reqA := map[string]interface{}{"jsonrpc": "2.0", "id": "req-a", "method": "session/prompt"}
	reqB := map[string]interface{}{"jsonrpc": "2.0", "id": "req-b", "method": "session/cancel"}
	if err := bridge.PublishToWorkerInbox(ctx, bridge.workerID, tokenA, reqA); err != nil {
		t.Fatalf("PublishToWorkerInbox tokenA: %v", err)
	}
	if err := bridge.PublishToWorkerInbox(ctx, bridge.workerID, tokenB, reqB); err != nil {
		t.Fatalf("PublishToWorkerInbox tokenB: %v", err)
	}

	assertWSMethod(t, clientA, "session/prompt")
	assertWSMethod(t, clientB, "session/cancel")
}

func TestWorkerInboxUsesFixedConsumerCount(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	queue := newConsumerSpyQueue()
	bridge := NewConnectionBridge(rdb, nil, queue)
	bridge.workerInboxConsumers = 3
	defer bridge.Shutdown()

	bridge.Start()
	if !queue.WaitForConsumers(bridge.workerInboxConsumers, 2*time.Second) {
		t.Fatalf("consumers = %d, want %d", queue.ConsumerCount(), bridge.workerInboxConsumers)
	}
}

func TestSendToBotRoutesViaWorkerInbox(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token := "sk-send-worker-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, token)

	queue := NewRedisStreamQueue(rdb, 100)
	bridge := NewConnectionBridge(rdb, nil, queue)
	bridge.workerInboxConsumers = 1
	defer func() {
		bridge.Shutdown()
		cleanupBridgeTestKeys(t, context.Background(), rdb, token)
	}()

	bot, client := newTestBotConn(t)
	defer client.Close()
	if err := bridge.RegisterBot(ctx, token, bot); err != nil {
		t.Fatalf("RegisterBot: %v", err)
	}

	bridge.Start()

	if _, err := bridge.SendToBot(ctx, token, "hello", nil, "session-1"); err != nil {
		t.Fatalf("SendToBot: %v", err)
	}

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	assertWSMethod(t, client, "session/prompt")
}

func TestSendToBotKeepsTokenPayloadsIsolated(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tokenA := "sk-send-a-" + uuid.NewString()[:8]
	tokenB := "sk-send-b-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, tokenA)
	cleanupBridgeTestKeys(t, ctx, rdb, tokenB)

	queue := NewRedisStreamQueue(rdb, 100)
	bridge := NewConnectionBridge(rdb, nil, queue)
	bridge.workerInboxConsumers = 1
	defer func() {
		bridge.Shutdown()
		cleanupBridgeTestKeys(t, context.Background(), rdb, tokenA)
		cleanupBridgeTestKeys(t, context.Background(), rdb, tokenB)
	}()

	botA, clientA := newTestBotConn(t)
	defer clientA.Close()
	if err := bridge.RegisterBot(ctx, tokenA, botA); err != nil {
		t.Fatalf("RegisterBot tokenA: %v", err)
	}

	botB, clientB := newTestBotConn(t)
	defer clientB.Close()
	if err := bridge.RegisterBot(ctx, tokenB, botB); err != nil {
		t.Fatalf("RegisterBot tokenB: %v", err)
	}

	bridge.Start()

	reqIDA, err := bridge.SendToBot(ctx, tokenA, "hello from A", nil, "session-a")
	if err != nil {
		t.Fatalf("SendToBot tokenA: %v", err)
	}
	reqIDB, err := bridge.SendToBot(ctx, tokenB, "hello from B", nil, "session-b")
	if err != nil {
		t.Fatalf("SendToBot tokenB: %v", err)
	}

	msgA := readWSJSON(t, clientA)
	msgB := readWSJSON(t, clientB)

	assertPromptPayload(t, msgA, reqIDA, "session-a", "hello from A")
	assertPromptPayload(t, msgB, reqIDB, "session-b", "hello from B")

	if msgA["id"] == msgB["id"] {
		t.Fatalf("expected isolated request ids, both got %v", msgA["id"])
	}

	assertNoExtraWSMessage(t, clientA, 200*time.Millisecond)
	assertNoExtraWSMessage(t, clientB, 200*time.Millisecond)
}

func TestRegisterBotDoesNotCreateLegacyPollTask(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token := "sk-no-poll-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, token)

	bridge := NewConnectionBridge(rdb, nil, noopQueue{})
	bot, client := newTestBotConn(t)
	defer client.Close()
	defer func() {
		bridge.Shutdown()
		cleanupBridgeTestKeys(t, context.Background(), rdb, token)
	}()

	if err := bridge.RegisterBot(ctx, token, bot); err != nil {
		t.Fatalf("RegisterBot: %v", err)
	}

	if _, ok := bridge.bots.Load(token); !ok {
		t.Fatal("expected RegisterBot to keep local bot registered")
	}
}

func TestWorkerInboxDisconnectEvictsLocalBot(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token := "sk-disconnect-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, token)

	queue := NewRedisStreamQueue(rdb, 100)
	bridge := NewConnectionBridge(rdb, nil, queue)
	bridge.workerInboxConsumers = 1
	defer func() {
		bridge.Shutdown()
		cleanupBridgeTestKeys(t, context.Background(), rdb, token)
	}()

	bot, client := newTestBotConn(t)
	defer client.Close()
	if err := bridge.RegisterBot(ctx, token, bot); err != nil {
		t.Fatalf("RegisterBot: %v", err)
	}

	bridge.Start()

	if err := bridge.PublishDisconnectToWorkerInbox(ctx, bridge.workerID, token); err != nil {
		t.Fatalf("PublishDisconnectToWorkerInbox: %v", err)
	}

	if !bridge.waitUntilLocalBotCount(0, 2*time.Second) {
		t.Fatalf("expected local bot to be evicted, local=%d", bridge.localBotCount())
	}
	if _, ok := bridge.bots.Load(token); ok {
		t.Fatal("expected local bot to be removed after disconnect")
	}
}

func TestWorkerInboxDisconnectDoesNotEvictOtherToken(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tokenA := "sk-disconnect-a-" + uuid.NewString()[:8]
	tokenB := "sk-disconnect-b-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, tokenA)
	cleanupBridgeTestKeys(t, ctx, rdb, tokenB)

	queue := NewRedisStreamQueue(rdb, 100)
	bridge := NewConnectionBridge(rdb, nil, queue)
	bridge.workerInboxConsumers = 1
	defer func() {
		bridge.Shutdown()
		cleanupBridgeTestKeys(t, context.Background(), rdb, tokenA)
		cleanupBridgeTestKeys(t, context.Background(), rdb, tokenB)
	}()

	botA, clientA := newTestBotConn(t)
	defer clientA.Close()
	if err := bridge.RegisterBot(ctx, tokenA, botA); err != nil {
		t.Fatalf("RegisterBot tokenA: %v", err)
	}

	botB, clientB := newTestBotConn(t)
	defer clientB.Close()
	if err := bridge.RegisterBot(ctx, tokenB, botB); err != nil {
		t.Fatalf("RegisterBot tokenB: %v", err)
	}

	bridge.Start()

	if err := bridge.PublishDisconnectToWorkerInbox(ctx, bridge.workerID, tokenA); err != nil {
		t.Fatalf("PublishDisconnectToWorkerInbox tokenA: %v", err)
	}

	if !bridge.waitUntilLocalBotCount(1, 2*time.Second) {
		t.Fatalf("expected only one local bot after disconnect, local=%d", bridge.localBotCount())
	}
	if _, ok := bridge.bots.Load(tokenA); ok {
		t.Fatal("expected tokenA to be removed after disconnect")
	}
	if _, ok := bridge.bots.Load(tokenB); !ok {
		t.Fatal("expected tokenB to remain connected")
	}

	if _, err := bridge.SendToBot(ctx, tokenB, "still here", nil, "session-b"); err != nil {
		t.Fatalf("SendToBot tokenB after tokenA disconnect: %v", err)
	}
	assertPromptPayload(t, readWSJSON(t, clientB), "", "session-b", "still here")
	assertNoExtraWSMessage(t, clientA, 200*time.Millisecond)
}

type consumerSpyQueue struct {
	mu        sync.Mutex
	consumers map[string]struct{}
	notifyCh  chan struct{}
}

func newConsumerSpyQueue() *consumerSpyQueue {
	return &consumerSpyQueue{
		consumers: make(map[string]struct{}),
		notifyCh:  make(chan struct{}, 16),
	}
}

func (q *consumerSpyQueue) Publish(ctx context.Context, queueName, message string) (string, error) {
	return "", nil
}

func (q *consumerSpyQueue) Consume(ctx context.Context, queueName, group, consumer string, blockMs int) (*QueueMessage, error) {
	q.mu.Lock()
	if _, ok := q.consumers[consumer]; !ok {
		q.consumers[consumer] = struct{}{}
		select {
		case q.notifyCh <- struct{}{}:
		default:
		}
	}
	q.mu.Unlock()

	<-ctx.Done()
	return nil, nil
}

func (q *consumerSpyQueue) Ack(ctx context.Context, queueName, group, messageID string) error {
	return nil
}

func (q *consumerSpyQueue) DeleteMessage(ctx context.Context, queueName, messageID string) error {
	return nil
}

func (q *consumerSpyQueue) DeleteQueue(ctx context.Context, queueName string) error {
	return nil
}

func (q *consumerSpyQueue) Purge(ctx context.Context, queueName string) error {
	return nil
}

func (q *consumerSpyQueue) EnsureGroup(ctx context.Context, queueName, group string) error {
	return nil
}

func (q *consumerSpyQueue) ConsumerCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.consumers)
}

func (q *consumerSpyQueue) WaitForConsumers(target int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		if q.ConsumerCount() >= target {
			return true
		}

		select {
		case <-deadline.C:
			return q.ConsumerCount() >= target
		case <-q.notifyCh:
		}
	}
}

func assertWSMethod(t *testing.T, clientConn wsReader, wantMethod string) {
	t.Helper()

	_, raw, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got["method"] != wantMethod {
		t.Fatalf("method = %v, want %v", got["method"], wantMethod)
	}
}

func readWSJSON(t *testing.T, clientConn wsReader) map[string]interface{} {
	t.Helper()

	_, raw, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return got
}

func assertPromptPayload(t *testing.T, got map[string]interface{}, wantReqID, wantSessionID, wantText string) {
	t.Helper()

	if got["method"] != "session/prompt" {
		t.Fatalf("method = %v, want session/prompt", got["method"])
	}
	if wantReqID != "" && got["id"] != wantReqID {
		t.Fatalf("id = %v, want %v", got["id"], wantReqID)
	}

	params, ok := got["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("params type = %T, want object", got["params"])
	}
	if params["sessionId"] != wantSessionID {
		t.Fatalf("sessionId = %v, want %v", params["sessionId"], wantSessionID)
	}

	prompt, ok := params["prompt"].(map[string]interface{})
	if !ok {
		t.Fatalf("prompt type = %T, want object", params["prompt"])
	}
	content, ok := prompt["content"].([]interface{})
	if !ok {
		t.Fatalf("content type = %T, want array", prompt["content"])
	}
	if len(content) != 1 {
		t.Fatalf("content len = %d, want 1", len(content))
	}
	item, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatalf("content[0] type = %T, want object", content[0])
	}
	if item["type"] != "text" {
		t.Fatalf("content[0].type = %v, want text", item["type"])
	}
	if item["content"] != wantText {
		t.Fatalf("content[0].content = %v, want %v", item["content"], wantText)
	}
}

func assertNoExtraWSMessage(t *testing.T, clientConn *websocket.Conn, timeout time.Duration) {
	t.Helper()

	_ = clientConn.SetReadDeadline(time.Now().Add(timeout))
	defer clientConn.SetReadDeadline(time.Time{})

	if _, _, err := clientConn.ReadMessage(); err == nil {
		t.Fatal("expected no extra websocket message")
	} else if !websocket.IsUnexpectedCloseError(err) {
		if nerr, ok := err.(interface{ Timeout() bool }); ok && nerr.Timeout() {
			return
		}
		if strings.Contains(err.Error(), "i/o timeout") {
			return
		}
		t.Fatalf("unexpected websocket read result: %v", err)
	}
}

type wsReader interface {
	ReadMessage() (messageType int, p []byte, err error)
}
