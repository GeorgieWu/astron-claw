package infra

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"astron-claw/backend/internal/config"
	"astron-claw/backend/migrations"
)

const (
	migrateLockKey      = "migrate:lock"
	migrateDoneKey      = "migrate:done"
	migrateFailKey      = "migrate:failed"
	migrateLockTTL      = 60 * time.Second
	migrateDoneTTL      = 300 * time.Second
	migrateWaitInterval = 1 * time.Second
	migrateWaitTimeout  = 60 * time.Second
)

// Lua script: atomically release lock only if we own it
var releaseLockLua = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`)

func RunMigrations(ctx context.Context, cfg config.MysqlConfig, rdb redis.UniversalClient) error {
	privileges, err := detectDDLPrivileges(ctx, cfg)
	if err != nil {
		return fmt.Errorf("detect ddl privileges: %w", err)
	}
	if !privileges.CanManageSchema {
		log.Warn().
			Str("database", cfg.Database).
			Msg("MySQL user lacks database DDL privileges, skipping migrations")
		return nil
	}

	// Open a dedicated connection with multiStatements=true for migrations
	dsn := cfg.DSN() + "&multiStatements=true"
	sqlDB, err := sqlOpen("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open migration db: %w", err)
	}
	defer sqlDB.Close()

	// Create migration source from embedded FS
	sourceDriver, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("create iofs source: %w", err)
	}

	dbDriver, err := mysql.WithInstance(sqlDB, &mysql.Config{})
	if err != nil {
		return fmt.Errorf("create mysql driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "mysql", dbDriver)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}

	// Try to acquire distributed lock
	owner := uuid.New().String()
	acquired, err := rdb.SetNX(ctx, migrateLockKey, owner, migrateLockTTL).Result()
	if err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}

	if acquired {
		// Check dirty state inside the lock
		version, dirty, vErr := m.Version()
		if vErr != nil && !errors.Is(vErr, migrate.ErrNilVersion) {
			releaseLockLua.Run(ctx, rdb, []string{migrateLockKey}, owner)
			return fmt.Errorf("get migration version: %w", vErr)
		}
		if dirty {
			log.Warn().Uint("version", version).Msg("Database in dirty state, forcing version to retry")
			if fErr := m.Force(int(version)); fErr != nil {
				releaseLockLua.Run(ctx, rdb, []string{migrateLockKey}, owner)
				return fmt.Errorf("force version: %w", fErr)
			}
		}

		log.Info().Msg("Acquired migration lock, running migrations...")
		if err := runMigrate(m); err != nil {
			// Mark failure
			rdb.Set(ctx, migrateFailKey, err.Error(), migrateDoneTTL)
			releaseLockLua.Run(ctx, rdb, []string{migrateLockKey}, owner)
			return err
		}
		// Mark done
		rdb.Set(ctx, migrateDoneKey, "1", migrateDoneTTL)
		releaseLockLua.Run(ctx, rdb, []string{migrateLockKey}, owner)
		return nil
	}

	// Another worker is running migrations, wait
	log.Info().Msg("Another worker is running migrations, waiting...")
	return waitForMigration(ctx, rdb)
}

func runMigrate(m *migrate.Migrate) error {
	err := m.Up()
	if errors.Is(err, migrate.ErrNoChange) {
		log.Info().Msg("Database schema is up to date, no migrations to run")
		return nil
	}
	if err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}
	log.Info().Msg("Database migration completed successfully")
	return nil
}

func waitForMigration(ctx context.Context, rdb redis.UniversalClient) error {
	deadline := time.Now().Add(migrateWaitTimeout)
	for time.Now().Before(deadline) {
		exists, _ := rdb.Exists(ctx, migrateDoneKey).Result()
		if exists > 0 {
			log.Info().Msg("Migration completed by another worker, proceeding with startup")
			return nil
		}
		failMsg, _ := rdb.Get(ctx, migrateFailKey).Result()
		if failMsg != "" {
			return fmt.Errorf("migration failed on another worker: %s", failMsg)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(migrateWaitInterval):
		}
	}
	return fmt.Errorf("timed out waiting for migration after %v", migrateWaitTimeout)
}
