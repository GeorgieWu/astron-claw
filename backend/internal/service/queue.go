package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"astron-claw/backend/internal/config"
)

// MessageQueue is the backend-agnostic message queue interface.
type MessageQueue interface {
	Publish(ctx context.Context, queueName, message string) (string, error)
	Consume(ctx context.Context, queueName, group, consumer string, blockMs int) (*QueueMessage, error)
	Ack(ctx context.Context, queueName, group, messageID string) error
	DeleteMessage(ctx context.Context, queueName, messageID string) error
	DeleteQueue(ctx context.Context, queueName string) error
	Purge(ctx context.Context, queueName string) error
	EnsureGroup(ctx context.Context, queueName, group string) error
	Close() error
}

// QueueMessage represents a consumed message.
type QueueMessage struct {
	ID   string
	Data string
}

// RedisStreamQueue implements MessageQueue backed by Redis Streams.
type RedisStreamQueue struct {
	rdb    redis.UniversalClient
	maxLen int64
}

// NewRedisStreamQueue creates a new RedisStreamQueue.
func NewRedisStreamQueue(rdb redis.UniversalClient, maxStreamLen int) *RedisStreamQueue {
	return &RedisStreamQueue{
		rdb:    rdb,
		maxLen: int64(maxStreamLen),
	}
}

func (q *RedisStreamQueue) Publish(ctx context.Context, queueName, message string) (string, error) {
	entryID, err := q.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: queueName,
		MaxLen: q.maxLen,
		Approx: true,
		Values: map[string]interface{}{"data": message},
	}).Result()
	if err != nil {
		return "", fmt.Errorf("xadd: %w", err)
	}
	log.Debug().Str("stream", queueName).Str("msg_id", entryID).Msg("Queue publish")
	return entryID, nil
}

func (q *RedisStreamQueue) Consume(ctx context.Context, queueName, group, consumer string, blockMs int) (*QueueMessage, error) {
	result, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{queueName, ">"},
		Count:    1,
		Block:    time.Duration(blockMs) * time.Millisecond,
	}).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		// NOGROUP — group hasn't been created yet
		errMsg := err.Error()
		if strings.Contains(errMsg, "NOGROUP") {
			// If context is cancelled, don't bother recreating (caller is shutting down)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			log.Warn().Str("stream", queueName).Str("group", group).Msg("Queue NOGROUP, recreating")
			_ = q.EnsureGroup(ctx, queueName, group)
			return nil, nil
		}
		return nil, fmt.Errorf("xreadgroup: %w", err)
	}

	if len(result) == 0 || len(result[0].Messages) == 0 {
		return nil, nil
	}

	msg := result[0].Messages[0]
	data, _ := msg.Values["data"].(string)
	log.Debug().Str("stream", queueName).Str("group", group).Str("msg_id", msg.ID).Msg("Queue consume")
	return &QueueMessage{ID: msg.ID, Data: data}, nil
}

func (q *RedisStreamQueue) Ack(ctx context.Context, queueName, group, messageID string) error {
	_, err := q.rdb.XAck(ctx, queueName, group, messageID).Result()
	if err != nil {
		return fmt.Errorf("xack: %w", err)
	}
	log.Debug().Str("stream", queueName).Str("msg_id", messageID).Msg("Queue ack")
	return nil
}

func (q *RedisStreamQueue) DeleteMessage(ctx context.Context, queueName, messageID string) error {
	_, err := q.rdb.XDel(ctx, queueName, messageID).Result()
	if err != nil {
		return fmt.Errorf("xdel: %w", err)
	}
	log.Debug().Str("stream", queueName).Str("msg_id", messageID).Msg("Queue delete_message")
	return nil
}

func (q *RedisStreamQueue) DeleteQueue(ctx context.Context, queueName string) error {
	_, err := q.rdb.Del(ctx, queueName).Result()
	if err != nil {
		return fmt.Errorf("del: %w", err)
	}
	log.Debug().Str("stream", queueName).Msg("Queue delete_queue")
	return nil
}

func (q *RedisStreamQueue) Purge(ctx context.Context, queueName string) error {
	_, err := q.rdb.XTrimMaxLen(ctx, queueName, 0).Result()
	if err != nil {
		return fmt.Errorf("xtrim: %w", err)
	}
	log.Debug().Str("stream", queueName).Msg("Queue purge")
	return nil
}

func (q *RedisStreamQueue) EnsureGroup(ctx context.Context, queueName, group string) error {
	err := q.rdb.XGroupCreateMkStream(ctx, queueName, group, "$").Err()
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "BUSYGROUP") {
			log.Debug().Str("stream", queueName).Str("group", group).Msg("Queue ensure_group: already exists")
			return nil
		}
		return fmt.Errorf("xgroup create: %w", err)
	}
	return nil
}

func (q *RedisStreamQueue) Close() error {
	// Redis Stream doesn't need explicit cleanup
	return nil
}

// NewQueue creates a MessageQueue implementation based on config.
func NewQueue(cfg config.QueueConfig, rdb redis.UniversalClient) (MessageQueue, error) {
	switch cfg.Type {
	case "redis_stream":
		return NewRedisStreamQueue(rdb, cfg.MaxStreamLen), nil
	case "pulsar":
		return NewPulsarQueue(cfg.PulsarURL, cfg.PulsarTenant, cfg.PulsarNS)
	default:
		return nil, fmt.Errorf("unsupported queue type: %q", cfg.Type)
	}
}
