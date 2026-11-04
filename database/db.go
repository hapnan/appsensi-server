package database

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	// Default pool limits tuned for direct cloud PostgreSQL (e.g. Render Basic/Starter max_connections=25),
	// keeping warm idle connections alive so pgx per-connection prepared statement caches stay hot.
	defaultMaxOpenConns    = 20
	defaultMaxIdleConns    = 10
	defaultConnMaxLifetime = 30 * time.Minute
	defaultConnMaxIdleTime = 5 * time.Minute
	startupPingTimeout     = 10 * time.Second
)

var DB *gorm.DB

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envPositiveInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func resolvePoolLimits(maxOpenRaw, maxIdleRaw int) (maxOpen, maxIdle int) {
	maxOpen = maxOpenRaw
	if maxOpen <= 0 {
		maxOpen = defaultMaxOpenConns
	}
	maxIdle = maxIdleRaw
	if maxIdle <= 0 {
		maxIdle = defaultMaxIdleConns
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	return maxOpen, maxIdle
}

func InitDB() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		// Fallback for local development
		dsn = "host=localhost user=hapnan password=Snaiper123 dbname=appsensi port=5432 sslmode=disable TimeZone=Asia/Jakarta"
	}

	dialector := postgres.New(postgres.Config{
		DSN:                  dsn,
		PreferSimpleProtocol: envBool("DB_SIMPLE_PROTOCOL"),
	})

	db, err := gorm.Open(dialector, &gorm.Config{
		// Avoid wrapping single-statement INSERT/UPDATE/DELETE queries in BEGIN...COMMIT (saves 2 RTTs per write).
		// Multi-statement atomic operations still use explicit db.Transaction(...).
		SkipDefaultTransaction: true,
	})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Failed to obtain underlying sql.DB: %v", err)
	}

	maxOpen, maxIdle := resolvePoolLimits(
		envPositiveInt("DB_MAX_OPEN_CONNS", defaultMaxOpenConns),
		envPositiveInt("DB_MAX_IDLE_CONNS", defaultMaxIdleConns),
	)
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(defaultConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(defaultConnMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(context.Background(), startupPingTimeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	if envBool("DB_AUTO_MIGRATE") {
		if err := migrateIfSchemaChanged(db); err != nil {
			log.Fatalf("Failed to migrate database schemas: %v", err)
		}
	}

	DB = db
}
