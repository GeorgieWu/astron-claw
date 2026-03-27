package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"astron-claw/backend/internal/config"
	"astron-claw/backend/internal/service"
)

func TestMetricsEndpointAuthBehavior(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:0",
		DialTimeout:  10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
		MaxRetries:   0,
		PoolSize:     1,
	})
	t.Cleanup(func() {
		_ = rdb.Close()
	})

	app := &App{
		Config:    &config.AppConfig{},
		RDB:       rdb,
		AdminAuth: service.NewAdminAuth(nil, rdb),
	}
	engine := SetupRouter(app)

	getReq := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	getRec := httptest.NewRecorder()
	engine.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/metrics status = %d, want %d; body=%s", getRec.Code, http.StatusOK, getRec.Body.String())
	}
	if got := getRec.Header().Get("Content-Type"); got != prometheusContentType {
		t.Fatalf("GET /api/metrics content-type = %q, want %q", got, prometheusContentType)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/metrics", nil)
	deleteRec := httptest.NewRecorder()
	engine.ServeHTTP(deleteRec, deleteReq)

	if deleteRec.Code != http.StatusUnauthorized {
		t.Fatalf("DELETE /api/metrics status = %d, want %d; body=%s", deleteRec.Code, http.StatusUnauthorized, deleteRec.Body.String())
	}
}
