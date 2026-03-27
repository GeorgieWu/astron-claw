package infra

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"astron-claw/backend/internal/config"
)

var validDBName = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
var sqlOpen = sql.Open
var detectDDLPrivileges = detectMySQLDDLPrivileges

type ddlPrivileges struct {
	CanCreateDatabase bool
	CanManageSchema   bool
}

func InitDB(ctx context.Context, cfg config.MysqlConfig, pool config.DBPoolConfig) (*gorm.DB, error) {
	// Ensure database exists
	if err := ensureDatabase(ctx, cfg); err != nil {
		return nil, fmt.Errorf("ensure database: %w", err)
	}

	sqlDB, err := sqlOpen("mysql", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}

	sqlDB.SetMaxIdleConns(pool.MaxIdleConns)
	sqlDB.SetMaxOpenConns(pool.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Duration(pool.ConnMaxLifetime) * time.Second)

	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("mysql ping: %w", err)
	}

	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("gorm open: %w", err)
	}

	log.Info().
		Str("host", cfg.Host).
		Int("port", cfg.Port).
		Str("database", cfg.Database).
		Msg("MySQL connected")

	return db, nil
}

func ensureDatabase(ctx context.Context, cfg config.MysqlConfig) error {
	if !validDBName.MatchString(cfg.Database) {
		return fmt.Errorf("invalid database name: %q", cfg.Database)
	}

	privileges, err := detectDDLPrivileges(ctx, cfg)
	if err != nil {
		return fmt.Errorf("detect ddl privileges: %w", err)
	}
	if !privileges.CanCreateDatabase {
		log.Warn().
			Str("database", cfg.Database).
			Msg("MySQL user lacks CREATE DATABASE privilege, skipping database creation")
		return nil
	}

	db, err := sqlOpen("mysql", cfg.DSNWithoutDB())
	if err != nil {
		return err
	}
	defer db.Close()

	query := fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%s` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		cfg.Database,
	)
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("create database: %w", err)
	}

	log.Info().Str("database", cfg.Database).Msg("Ensured database exists")
	return nil
}

func detectMySQLDDLPrivileges(ctx context.Context, cfg config.MysqlConfig) (ddlPrivileges, error) {
	if !validDBName.MatchString(cfg.Database) {
		return ddlPrivileges{}, fmt.Errorf("invalid database name: %q", cfg.Database)
	}

	db, err := sqlOpen("mysql", cfg.DSNWithoutDB())
	if err != nil {
		return ddlPrivileges{}, fmt.Errorf("open privilege check db: %w", err)
	}
	defer db.Close()

	grants, err := currentMySQLGrants(ctx, db)
	if err != nil {
		return ddlPrivileges{}, err
	}

	return ddlPrivileges{
		CanCreateDatabase: hasCreateDatabasePrivilege(grants),
		CanManageSchema:   hasDatabaseDDLPrivileges(grants, cfg.Database),
	}, nil
}

func currentMySQLGrants(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SHOW GRANTS FOR CURRENT_USER()")
	if err != nil {
		return nil, fmt.Errorf("show grants: %w", err)
	}
	defer rows.Close()

	var grants []string
	for rows.Next() {
		var grant string
		if err := rows.Scan(&grant); err != nil {
			return nil, fmt.Errorf("scan grant: %w", err)
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate grants: %w", err)
	}
	return grants, nil
}

func hasCreateDatabasePrivilege(grants []string) bool {
	for _, grant := range grants {
		privileges, scope, ok := parseGrant(grant)
		if !ok || scope != "*.*" {
			continue
		}
		if grantIncludes(privileges, "ALL PRIVILEGES") || grantIncludes(privileges, "CREATE") {
			return true
		}
	}
	return false
}

func hasDatabaseDDLPrivileges(grants []string, database string) bool {
	required := map[string]bool{
		"CREATE": false,
		"ALTER":  false,
		"DROP":   false,
		"INDEX":  false,
	}

	for _, grant := range grants {
		privileges, scope, ok := parseGrant(grant)
		if !ok || !grantScopeMatchesDatabase(scope, database) {
			continue
		}
		if grantIncludes(privileges, "ALL PRIVILEGES") {
			return true
		}
		for privilege := range required {
			if grantIncludes(privileges, privilege) {
				required[privilege] = true
			}
		}
	}

	for _, granted := range required {
		if !granted {
			return false
		}
	}
	return true
}

func parseGrant(grant string) ([]string, string, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(grant))
	if !strings.HasPrefix(normalized, "GRANT ") {
		return nil, "", false
	}

	onIdx := strings.Index(normalized, " ON ")
	if onIdx == -1 {
		return nil, "", false
	}
	toIdx := strings.Index(normalized[onIdx+4:], " TO ")
	if toIdx == -1 {
		return nil, "", false
	}
	toIdx += onIdx + 4

	privilegePart := strings.TrimSpace(normalized[len("GRANT "):onIdx])
	if privilegePart == "" {
		return nil, "", false
	}

	rawPrivileges := strings.Split(privilegePart, ",")
	privileges := make([]string, 0, len(rawPrivileges))
	for _, privilege := range rawPrivileges {
		privilege = strings.TrimSpace(privilege)
		if privilege != "" {
			privileges = append(privileges, privilege)
		}
	}

	scope := strings.ReplaceAll(strings.TrimSpace(normalized[onIdx+4:toIdx]), "`", "")
	scope = strings.ReplaceAll(scope, " ", "")
	return privileges, scope, true
}

func grantScopeMatchesDatabase(scope, database string) bool {
	scope = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(scope), "`", ""))
	dbScope := strings.ToUpper(database) + ".*"
	return scope == "*.*" || scope == dbScope
}

func grantIncludes(privileges []string, want string) bool {
	for _, privilege := range privileges {
		if privilege == want {
			return true
		}
	}
	return false
}

func CloseDB(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Error().Err(err).Msg("Failed to get sql.DB for closing")
		return
	}
	if err := sqlDB.Close(); err != nil {
		log.Error().Err(err).Msg("Failed to close MySQL")
	} else {
		log.Info().Msg("MySQL connection pool closed")
	}
}
