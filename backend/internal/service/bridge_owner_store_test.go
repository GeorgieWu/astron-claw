package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBotOwnerStoreSetAndGet(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token := "sk-owner-" + uuid.NewString()[:8]
	workerID := "worker-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, token)

	bridge := NewConnectionBridge(rdb, nil, noopQueue{})

	if err := bridge.SetBotOwner(ctx, token, workerID); err != nil {
		t.Fatalf("SetBotOwner: %v", err)
	}

	got, err := bridge.GetBotOwner(ctx, token)
	if err != nil {
		t.Fatalf("GetBotOwner: %v", err)
	}
	if got != workerID {
		t.Fatalf("owner = %q, want %q", got, workerID)
	}
}

func TestResolveBotRouteReturnsWorkerInbox(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token := "sk-route-" + uuid.NewString()[:8]
	workerID := "worker-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, token)

	bridge := NewConnectionBridge(rdb, nil, noopQueue{})
	if err := bridge.SetBotOwner(ctx, token, workerID); err != nil {
		t.Fatalf("SetBotOwner: %v", err)
	}

	route, err := bridge.ResolveBotRoute(ctx, token)
	if err != nil {
		t.Fatalf("ResolveBotRoute: %v", err)
	}
	if route.Token != token {
		t.Fatalf("route token = %q, want %q", route.Token, token)
	}
	if route.WorkerID != workerID {
		t.Fatalf("route worker = %q, want %q", route.WorkerID, workerID)
	}
	wantInbox := WorkerInboxPrefix + workerID
	if route.Inbox != wantInbox {
		t.Fatalf("route inbox = %q, want %q", route.Inbox, wantInbox)
	}
}

func TestResolveBotRouteErrorsWithoutOwner(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token := "sk-missing-" + uuid.NewString()[:8]
	cleanupBridgeTestKeys(t, ctx, rdb, token)

	bridge := NewConnectionBridge(rdb, nil, noopQueue{})
	_, err := bridge.ResolveBotRoute(ctx, token)
	if err == nil {
		t.Fatal("expected ResolveBotRoute to fail without owner")
	}
	if !strings.Contains(err.Error(), "owner") {
		t.Fatalf("expected missing owner error, got %v", err)
	}
}

func TestRegisterBotWritesOwner(t *testing.T) {
	rdb := newLocalRedisForBridgeTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token := "sk-register-owner-" + uuid.NewString()[:8]
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

	got, err := bridge.GetBotOwner(ctx, token)
	if err != nil {
		t.Fatalf("GetBotOwner: %v", err)
	}
	if got != bridge.workerID {
		t.Fatalf("owner = %q, want %q", got, bridge.workerID)
	}
}
