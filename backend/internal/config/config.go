package config

import (
	"os"
	"runtime"
	"strconv"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log"
)

type MysqlConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

func (c MysqlConfig) DSN() string {
	cfg := mysqldriver.Config{
		User:                 c.User,
		Passwd:               c.Password,
		Net:                  "tcp",
		Addr:                 c.Host + ":" + strconv.Itoa(c.Port),
		DBName:               c.Database,
		AllowNativePasswords: true,
		Params: map[string]string{
			"charset":   "utf8mb4",
			"parseTime": "True",
			"loc":       "UTC",
		},
	}
	return cfg.FormatDSN()
}

// DSNWithoutDB returns DSN without database name for initial database creation
func (c MysqlConfig) DSNWithoutDB() string {
	cfg := mysqldriver.Config{
		User:                 c.User,
		Passwd:               c.Password,
		Net:                  "tcp",
		Addr:                 c.Host + ":" + strconv.Itoa(c.Port),
		AllowNativePasswords: true,
		Params: map[string]string{
			"charset":   "utf8mb4",
			"parseTime": "True",
			"loc":       "UTC",
		},
	}
	return cfg.FormatDSN()
}

type RedisConfig struct {
	Password string
	DB       int
	Addrs    []string
	Cluster  bool
}

// IsCluster returns true when Redis Cluster mode is explicitly enabled or
// when multiple seed addresses are configured.
func (c RedisConfig) IsCluster() bool {
	return c.Cluster || len(c.Addrs) > 1
}

type DBPoolConfig struct {
	MaxIdleConns    int
	MaxOpenConns    int
	ConnMaxLifetime int // seconds
}

type ServerConfig struct {
	Host           string
	Port           int
	Workers        int
	LogLevel       string
	AccessLog      bool
	WSPingInterval int
	WSPingTimeout  int
	SecureCookie   bool
}

type QueueConfig struct {
	Type         string
	MaxStreamLen int
	BlockMs      int
}

type StorageConfig struct {
	Type           string
	Endpoint       string
	PublicEndpoint string
	AccessKey      string
	SecretKey      string
	Bucket         string
	Region         string
	TTL            int
	PublicRead     bool
}

type OtlpConfig struct {
	Enabled        bool
	ServiceName    string
	Endpoint       string
	Insecure       bool
	MetricsEnabled bool
	TracesEnabled  bool
	LogsEnabled    bool
}

type CorsConfig struct {
	Origins []string
	Enabled bool
}

type AppConfig struct {
	MySQL   MysqlConfig
	Redis   RedisConfig
	Server  ServerConfig
	Queue   QueueConfig
	Storage StorageConfig
	OTLP    OtlpConfig
	CORS    CorsConfig
	DBPool  DBPoolConfig
}

var validOSSTypes = map[string]bool{"s3": true, "ifly_gateway": true}

func Load() *AppConfig {
	// Load .env from the backend directory (or parent)
	_ = godotenv.Load()

	ossType := getEnv("OSS_TYPE", "s3")
	if !validOSSTypes[ossType] {
		log.Fatal().Str("OSS_TYPE", ossType).Msg("Invalid OSS_TYPE")
	}

	publicEndpoint := os.Getenv("OSS_PUBLIC_ENDPOINT")
	if publicEndpoint == "" {
		publicEndpoint = getEnv("OSS_ENDPOINT", "http://localhost:9000")
	}

	cfg := &AppConfig{
		MySQL: MysqlConfig{
			Host:     getEnv("MYSQL_HOST", "127.0.0.1"),
			Port:     getEnvInt("MYSQL_PORT", 3306),
			User:     getEnv("MYSQL_USER", "root"),
			Password: getEnv("MYSQL_PASSWORD", ""),
			Database: getEnv("MYSQL_DATABASE", "astron_claw"),
		},
		Redis: RedisConfig{
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvInt("REDIS_DB", 0),
			Addrs:    splitCSV(getEnv("REDIS_ADDRS", "127.0.0.1:6379")),
			Cluster:  getEnvBool("REDIS_CLUSTER", false),
		},
		Server: ServerConfig{
			Host:           getEnv("SERVER_HOST", "0.0.0.0"),
			Port:           getEnvInt("SERVER_PORT", 8765),
			Workers:        getEnvInt("SERVER_WORKERS", runtime.NumCPU()+1),
			LogLevel:       getEnv("SERVER_LOG_LEVEL", "info"),
			AccessLog:      getEnvBool("SERVER_ACCESS_LOG", true),
			WSPingInterval: getEnvInt("WS_PING_INTERVAL", 10),
			WSPingTimeout:  getEnvInt("WS_PING_TIMEOUT", 10),
			SecureCookie:   getEnvBool("COOKIE_SECURE", false),
		},
		Queue: QueueConfig{
			Type:         getEnv("QUEUE_TYPE", "redis_stream"),
			MaxStreamLen: getEnvInt("QUEUE_MAX_STREAM_LEN", 1000),
			BlockMs:      getEnvInt("QUEUE_BLOCK_MS", 5000),
		},
		Storage: StorageConfig{
			Type:           ossType,
			Endpoint:       getEnv("OSS_ENDPOINT", "http://localhost:9000"),
			PublicEndpoint: publicEndpoint,
			AccessKey:      getEnv("OSS_ACCESS_KEY", "minioadmin"),
			SecretKey:      getEnv("OSS_SECRET_KEY", "minioadmin"),
			Bucket:         getEnv("OSS_BUCKET", "astron-claw-media"),
			Region:         getEnv("OSS_REGION", "us-east-1"),
			TTL:            getEnvInt("OSS_TTL", 157788000),
			PublicRead:     getEnvBool("OSS_PUBLIC_READ", true),
		},
		OTLP: OtlpConfig{
			Enabled:        getEnvBool("OTLP_ENABLED", false),
			ServiceName:    getEnv("OTLP_SERVICE_NAME", "astron-claw"),
			Endpoint:       getEnv("OTLP_ENDPOINT", "localhost:4317"),
			Insecure:       getEnvBool("OTLP_INSECURE", false),
			MetricsEnabled: true,
			TracesEnabled:  false,
			LogsEnabled:    false,
		},
		CORS: CorsConfig{
			Origins: splitCSV(getEnv("CORS_ORIGINS", "*")),
			Enabled: getEnvBool("CORS_ENABLED", true),
		},
		DBPool: DBPoolConfig{
			MaxIdleConns:    getEnvInt("DB_MAX_IDLE_CONNS", 10),
			MaxOpenConns:    getEnvInt("DB_MAX_OPEN_CONNS", 15),
			ConnMaxLifetime: getEnvInt("DB_CONN_MAX_LIFETIME", 3600),
		},
	}

	log.Info().
		Str("redis", strings.Join(cfg.Redis.Addrs, ",")).
		Str("mysql", cfg.MySQL.Host+":"+strconv.Itoa(cfg.MySQL.Port)+"/"+cfg.MySQL.Database).
		Str("storage", cfg.Storage.Type).
		Bool("otlp", cfg.OTLP.Enabled).
		Msg("Config loaded")

	return cfg
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if v := os.Getenv(key); v != "" {
		return strings.ToLower(v) == "true"
	}
	return defaultVal
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
