package infra

import (
	"context"
	"database/sql"
	"testing"

	"astron-claw/backend/internal/config"
)

func TestRunMigrationsSkipsWithoutDDL(t *testing.T) {
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

	if err := RunMigrations(context.Background(), cfg, nil); err != nil {
		t.Fatalf("RunMigrations returned error: %v", err)
	}
	if opened {
		t.Fatal("expected RunMigrations to skip migration setup when DDL is unavailable")
	}
}
