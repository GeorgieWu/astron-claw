package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReconcileEvictsIdleOlderOwner(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	token := "sk-reconcile-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, token)

	queue := NewRedisStreamQueue(rdb, 100)
	oldBridge := NewConnectionBridge(rdb, nil, queue)
	newBridge := NewConnectionBridge(rdb, nil, queue)
	oldBridge.reconcileInterval = 100 * time.Millisecond
	oldBridge.reconcileBatchSize = 1
	oldBridge.workerInboxConsumers = 0
	newBridge.workerInboxConsumers = 0
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

	oldBridge.Start()

	if !oldBridge.waitUntilLocalBotCount(0, 2*time.Second) {
		t.Fatal("expected idle older owner to be evicted by reconcile")
	}
	if _, ok := oldBridge.bots.Load(token); ok {
		t.Fatal("expected old bridge local bot to be removed")
	}
	if _, ok := newBridge.bots.Load(token); !ok {
		t.Fatal("expected newer owner to remain locally connected")
	}
}

func TestReconcileChecksFixedBatchSize(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	queue := NewRedisStreamQueue(rdb, 100)
	bridge := NewConnectionBridge(rdb, nil, queue)
	bridge.reconcileInterval = 100 * time.Millisecond
	bridge.reconcileBatchSize = 1
	bridge.workerInboxConsumers = 0

	tokens := []string{
		"sk-batch-a-" + uuid.NewString()[:8],
		"sk-batch-b-" + uuid.NewString()[:8],
		"sk-batch-c-" + uuid.NewString()[:8],
	}
	defer func() {
		bridge.Shutdown()
		for _, token := range tokens {
			cleanupBridgeTestKeys(t, context.Background(), rdb, token)
		}
	}()

	clients := make([]interface{ Close() error }, 0, len(tokens))
	for _, token := range tokens {
		cleanupBridgeTestKeys(t, ctx, rdb, token)
		bot, client := newTestBotConn(t)
		clients = append(clients, client)
		if err := bridge.RegisterBot(ctx, token, bot); err != nil {
			t.Fatalf("RegisterBot %s: %v", token, err)
		}
		if err := bridge.SetBotOwner(ctx, token, "other-worker"); err != nil {
			t.Fatalf("SetBotOwner %s: %v", token, err)
		}
	}
	defer func() {
		for _, client := range clients {
			_ = client.Close()
		}
	}()

	bridge.Start()

	if !bridge.waitUntilLocalBotCount(2, 2*time.Second) {
		t.Fatalf("expected first reconcile pass to evict exactly one token, local=%d", bridge.localBotCount())
	}
	if !bridge.waitUntilLocalBotCount(1, 2*time.Second) {
		t.Fatalf("expected second reconcile pass to evict exactly one more token, local=%d", bridge.localBotCount())
	}
	if !bridge.waitUntilLocalBotCount(0, 2*time.Second) {
		t.Fatalf("expected reconcile to eventually evict all stale tokens, local=%d", bridge.localBotCount())
	}
}
