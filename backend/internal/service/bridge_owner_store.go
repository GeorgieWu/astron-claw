package service

import (
	"context"
	"fmt"
)

type BotRoute struct {
	Token    string
	WorkerID string
	Inbox    string
}

func (b *ConnectionBridge) SetBotOwner(ctx context.Context, token, workerID string) error {
	if err := b.rdb.Set(ctx, BotOwnerPrefix+token, workerID, 0).Err(); err != nil {
		return fmt.Errorf("set bot owner: %w", err)
	}
	return nil
}

func (b *ConnectionBridge) GetBotOwner(ctx context.Context, token string) (string, error) {
	workerID, err := b.rdb.Get(ctx, BotOwnerPrefix+token).Result()
	if err != nil {
		return "", fmt.Errorf("get bot owner: %w", err)
	}
	return workerID, nil
}

func (b *ConnectionBridge) ResolveBotRoute(ctx context.Context, token string) (BotRoute, error) {
	workerID, err := b.GetBotOwner(ctx, token)
	if err != nil {
		return BotRoute{}, fmt.Errorf("resolve bot route: %w", err)
	}

	return BotRoute{
		Token:    token,
		WorkerID: workerID,
		Inbox:    WorkerInboxPrefix + workerID,
	}, nil
}
