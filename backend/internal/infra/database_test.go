package infra

import (
	"context"
	"database/sql"
	"testing"

	"astron-claw/backend/internal/config"
)

func TestHasDatabaseDDLPrivilegesAcceptsDatabaseScopedDDL(t *testing.T) {
	grants := []string{
		"GRANT SELECT, INSERT, UPDATE ON `astron_claw`.* TO `app`@`%`",
		"GRANT CREATE, ALTER, DROP, INDEX ON `astron_claw`.* TO `app`@`%`",
	}

	if !hasDatabaseDDLPrivileges(grants, "astron_claw") {
		t.Fatal("expected database-scoped DDL privileges to be accepted")
	}
}

func TestHasDatabaseDDLPrivilegesRejectsDMLOnlyGrants(t *testing.T) {
	grants := []string{
		"GRANT SELECT, INSERT, UPDATE, DELETE ON `astron_claw`.* TO `app`@`%`",
	}

	if hasDatabaseDDLPrivileges(grants, "astron_claw") {
		t.Fatal("expected DML-only grants to be rejected")
	}
}

func TestHasDatabaseDDLPrivilegesAcceptsGlobalAllPrivileges(t *testing.T) {
	grants := []string{
		"GRANT ALL PRIVILEGES ON *.* TO `app`@`%`",
	}

	if !hasDatabaseDDLPrivileges(grants, "astron_claw") {
		t.Fatal("expected global ALL PRIVILEGES grant to be accepted")
	}
}

func TestHasCreateDatabasePrivilegeAcceptsGlobalCreate(t *testing.T) {
	grants := []string{
		"GRANT CREATE ON *.* TO `app`@`%`",
	}

	if !hasCreateDatabasePrivilege(grants) {
		t.Fatal("expected global CREATE privilege to be accepted for database creation")
	}
}

func TestEnsureDatabaseSkipsCreateWithoutCreatePrivilege(t *testing.T) {
	oldDetect := detectDDLPrivileges
	oldOpen := sqlOpen
	t.Cleanup(func() {
		detectDDLPrivileges = oldDetect
		sqlOpen = oldOpen
	})

	detectDDLPrivileges = func(context.Context, config.MysqlConfig) (ddlPrivileges, error) {
		return ddlPrivileges{
			CanCreateDatabase: false,
			CanManageSchema:   false,
		}, nil
	}

	opened := false
	sqlOpen = func(driverName, dsn string) (*sql.DB, error) {
		opened = true
		t.Fatalf("sqlOpen should not be called, got driver=%q dsn=%q", driverName, dsn)
		return nil, nil
	}

	cfg := config.MysqlConfig{
		Host:     "127.0.0.1",
		Port:     3306,
		User:     "app",
		Password: "secret",
		Database: "astron_claw",
	}

	if err := ensureDatabase(context.Background(), cfg); err != nil {
		t.Fatalf("ensureDatabase returned error: %v", err)
	}
	if opened {
		t.Fatal("expected ensureDatabase to skip CREATE DATABASE when CREATE privilege is unavailable")
	}
}
