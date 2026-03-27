# Public Metrics Endpoint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `GET /api/metrics` publicly scrapeable by Prometheus while keeping `DELETE /api/metrics` admin-protected.

**Architecture:** Keep routing structure unchanged and remove only the redundant auth check inside the GET metrics handler. Prove the behavior with a route regression test that exercises both methods through `SetupRouter`.

**Tech Stack:** Go, Gin, go-redis, httptest

---

### Task 1: Add regression coverage for metrics auth behavior

**Files:**
- Create: `backend/internal/router/metrics_test.go`
- Test: `backend/internal/router/metrics_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestMetricsEndpointAuthBehavior(t *testing.T) {
	// GET /api/metrics should return 200 without auth.
	// DELETE /api/metrics should still return 401 without auth.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && GOCACHE=/tmp/go-build GOTMPDIR=/tmp/go-tmp go test ./internal/router -run TestMetricsEndpointAuthBehavior -count=1`
Expected: FAIL because `GET /api/metrics` still returns unauthorized.

- [ ] **Step 3: Write minimal implementation**

Update `backend/internal/router/metrics.go` so `getMetrics` no longer validates admin cookies or bearer tokens before rendering telemetry.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && GOCACHE=/tmp/go-build GOTMPDIR=/tmp/go-tmp go test ./internal/router -run TestMetricsEndpointAuthBehavior -count=1`
Expected: PASS

- [ ] **Step 5: Run focused regression suite**

Run: `cd backend && GOCACHE=/tmp/go-build GOTMPDIR=/tmp/go-tmp go test ./internal/router ./internal/middleware -count=1`
Expected: PASS
