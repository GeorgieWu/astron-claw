# MySQL DDL-Aware Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow the Go backend to start without running database DDL when the MySQL account lacks DDL privileges.

**Architecture:** Add a small MySQL privilege-detection helper in `backend/internal/infra`, use it before startup DDL actions, and keep failure behavior unchanged when the database is genuinely unavailable or privilege detection errors out. Cover the change with focused unit tests and keep the migration/connection flow otherwise unchanged.

**Tech Stack:** Go, `database/sql`, MySQL, `golang-migrate`, zerolog, Go testing

---

### Task 1: Add failing tests for DDL detection and skip behavior

**Files:**
- Create: `backend/internal/infra/database_test.go`
- Create: `backend/internal/infra/migrate_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestHasDDLPrivilegesFromGrants(t *testing.T) {}
func TestEnsureDatabaseSkipsCreateWithoutDDL(t *testing.T) {}
func TestRunMigrationsSkipsWithoutDDL(t *testing.T) {}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/infra -run 'Test(HasDDLPrivileges|EnsureDatabase|RunMigrations)' -count=1`
Expected: FAIL because helpers and skip logic do not exist yet.

### Task 2: Implement DDL detection and startup guards

**Files:**
- Modify: `backend/internal/infra/database.go`
- Modify: `backend/internal/infra/migrate.go`
- Modify: `backend/cmd/server/main.go`

- [ ] **Step 1: Add privilege parsing and detection**

Implement a helper that reads `SHOW GRANTS FOR CURRENT_USER()` and determines whether the current account has sufficient DDL privileges for the target database.

- [ ] **Step 2: Guard database creation**

Make `ensureDatabase()` skip `CREATE DATABASE IF NOT EXISTS` when DDL privileges are absent.

- [ ] **Step 3: Guard migrations**

Make `RunMigrations()` skip migration setup and execution when DDL privileges are absent.

- [ ] **Step 4: Keep startup call sites aligned**

Pass context as needed so both startup DDL paths can use the same privilege-detection flow cleanly.

### Task 3: Verify and clean up

**Files:**
- Modify: `backend/internal/infra/database.go`
- Modify: `backend/internal/infra/migrate.go`
- Test: `backend/internal/infra/database_test.go`
- Test: `backend/internal/infra/migrate_test.go`

- [ ] **Step 1: Run formatting**

Run: `gofmt -w backend/internal/infra/database.go backend/internal/infra/migrate.go backend/internal/infra/database_test.go backend/internal/infra/migrate_test.go backend/cmd/server/main.go`

- [ ] **Step 2: Run focused tests**

Run: `go test ./internal/infra -count=1`
Expected: PASS

- [ ] **Step 3: Run startup-adjacent package tests if needed**

Run: `go test ./cmd/server ./internal/infra -count=1`
Expected: PASS or no test files in `cmd/server`
