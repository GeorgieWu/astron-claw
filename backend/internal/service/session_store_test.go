package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"astron-claw/backend/internal/model"
)

type fakeSessionRedis struct {
	redis.UniversalClient
	lrangeResult []string
	lrangeErr    error
	pipeline     redis.Pipeliner
}

func (f *fakeSessionRedis) LRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	cmd := redis.NewStringSliceCmd(ctx, "lrange", key, start, stop)
	if f.lrangeErr != nil {
		cmd.SetErr(f.lrangeErr)
		return cmd
	}
	cmd.SetVal(f.lrangeResult)
	return cmd
}

func (f *fakeSessionRedis) Pipeline() redis.Pipeliner {
	return f.pipeline
}

type fakeSessionPipeline struct {
	redis.Pipeliner
	delKeys     []string
	rpushKey    string
	rpushValues []interface{}
	expireKey   string
	expireTTL   time.Duration
}

func (f *fakeSessionPipeline) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	f.delKeys = append(f.delKeys, keys...)
	cmd := redis.NewIntCmd(ctx, "del")
	cmd.SetVal(int64(len(keys)))
	return cmd
}

func (f *fakeSessionPipeline) RPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	f.rpushKey = key
	f.rpushValues = append([]interface{}(nil), values...)
	args := append([]interface{}{"rpush", key}, values...)
	cmd := redis.NewIntCmd(ctx, args...)
	cmd.SetVal(int64(len(values)))
	return cmd
}

func (f *fakeSessionPipeline) Expire(ctx context.Context, key string, expiration time.Duration) *redis.BoolCmd {
	f.expireKey = key
	f.expireTTL = expiration
	cmd := redis.NewBoolCmd(ctx, "expire", key, expiration)
	cmd.SetVal(true)
	return cmd
}

func (f *fakeSessionPipeline) Exec(ctx context.Context) ([]redis.Cmder, error) {
	return nil, nil
}

func TestSessionStoreGetSessionsCacheHitUsesPlainSessionIDs(t *testing.T) {
	store := &SessionStore{
		rdb: &fakeSessionRedis{
			lrangeResult: []string{"sid-1", "sid-2"},
		},
	}

	sessions, err := store.GetSessions(context.Background(), "tok-1")
	if err != nil {
		t.Fatalf("GetSessions returned error: %v", err)
	}

	want := []SessionInfo{
		{ID: "sid-1", Number: 1},
		{ID: "sid-2", Number: 2},
	}
	if !reflect.DeepEqual(sessions, want) {
		t.Fatalf("GetSessions mismatch: got %#v want %#v", sessions, want)
	}
}

func TestSessionStoreRepopulateCacheWritesPlainSessionIDs(t *testing.T) {
	pipe := &fakeSessionPipeline{}
	store := &SessionStore{
		rdb: &fakeSessionRedis{pipeline: pipe},
	}

	store.repopulateCache(context.Background(), "tok-1", []model.ChatSession{
		{SessionID: "sid-1", SessionNumber: 7},
		{SessionID: "sid-2", SessionNumber: 8},
	})

	if pipe.rpushKey != sessionsPrefix+"tok-1" {
		t.Fatalf("RPush key mismatch: got %q want %q", pipe.rpushKey, sessionsPrefix+"tok-1")
	}
	wantValues := []interface{}{"sid-1", "sid-2"}
	if !reflect.DeepEqual(pipe.rpushValues, wantValues) {
		t.Fatalf("RPush values mismatch: got %#v want %#v", pipe.rpushValues, wantValues)
	}
	if pipe.expireKey != sessionsPrefix+"tok-1" {
		t.Fatalf("Expire key mismatch: got %q want %q", pipe.expireKey, sessionsPrefix+"tok-1")
	}
	if pipe.expireTTL != sessionCacheTTL {
		t.Fatalf("Expire TTL mismatch: got %v want %v", pipe.expireTTL, sessionCacheTTL)
	}
}
