package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config represents the application configuration
type Config struct {
	// Server
	Port         string
	Host         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration

	// Canvas
	CanvasWidth  int
	CanvasHeight int
	TileCooldown time.Duration

	// Redis
	RedisHost          string
	RedisPort          string
	RedisPassword      string
	RedisDB            int
	RedisMaxRetries    int
	RedisMinRetryDelay time.Duration
	RedisMaxRetryDelay time.Duration
	RedisPoolSize      int
	RedisMinIdleConns  int

	// WebSocket
	WSReadBufferSize   int
	WSWriteBufferSize  int
	WSHandshakeTimeout time.Duration
	WSMaxMessageSize   int64
	WSPingInterval     time.Duration
	WSPongWait         time.Duration

	// Rate Limiting
	RateLimit         int
	RateLimitDuration time.Duration

	// Logging
	LogLevel string

	// Security
	AllowAllOrigins bool
	AllowedOrigins  []string
	MaxConnPerIP    int
}

// LoadConfig charge la configuration depuis les variables d'environnement
func LoadConfig() *Config {
	config := &Config{
		// Server
		Port:         getEnv("PORT", "8080"),
		Host:         getEnv("HOST", "0.0.0.0"),
		ReadTimeout:  time.Duration(getEnvAsInt("READ_TIMEOUT_SEC", 5)) * time.Second,
		WriteTimeout: time.Duration(getEnvAsInt("WRITE_TIMEOUT_SEC", 5)) * time.Second,
		IdleTimeout:  time.Duration(getEnvAsInt("IDLE_TIMEOUT_SEC", 60)) * time.Second,

		// Canvas
		CanvasWidth:  getEnvAsInt("CANVAS_WIDTH", 10),
		CanvasHeight: getEnvAsInt("CANVAS_HEIGHT", 10),
		TileCooldown: time.Duration(getEnvAsInt("TILE_COOLDOWN_SECONDS", 5)) * time.Second,

		// Redis
		RedisHost:          getEnv("REDIS_HOST", "redis"),
		RedisPort:          getEnv("REDIS_PORT", "6379"),
		RedisPassword:      getEnv("REDIS_PASSWORD", ""),
		RedisDB:            getEnvAsInt("REDIS_DB", 0),
		RedisMaxRetries:    getEnvAsInt("REDIS_MAX_RETRIES", 3),
		RedisMinRetryDelay: time.Duration(getEnvAsInt("REDIS_MIN_RETRY_DELAY_MS", 100)) * time.Millisecond,
		RedisMaxRetryDelay: time.Duration(getEnvAsInt("REDIS_MAX_RETRY_DELAY_MS", 500)) * time.Millisecond,
		RedisPoolSize:      getEnvAsInt("REDIS_POOL_SIZE", 100),
		RedisMinIdleConns:  getEnvAsInt("REDIS_MIN_IDLE_CONNS", 10),

		// WebSocket
		WSReadBufferSize:   getEnvAsInt("WS_READ_BUFFER_SIZE", 1024),
		WSWriteBufferSize:  getEnvAsInt("WS_WRITE_BUFFER_SIZE", 1024),
		WSHandshakeTimeout: time.Duration(getEnvAsInt("WS_HANDSHAKE_TIMEOUT_SEC", 10)) * time.Second,
		WSMaxMessageSize:   int64(getEnvAsInt("WS_MAX_MESSAGE_SIZE", 512)),
		WSPingInterval:     time.Duration(getEnvAsInt("WS_PING_INTERVAL_SEC", 30)) * time.Second,
		WSPongWait:         time.Duration(getEnvAsInt("WS_PONG_WAIT_SEC", 60)) * time.Second,

		// Rate Limiting
		RateLimit:         getEnvAsInt("RATE_LIMIT", 10),
		RateLimitDuration: time.Duration(getEnvAsInt("RATE_LIMIT_DURATION_SEC", 1)) * time.Second,

		// Logging
		LogLevel: getEnv("LOG_LEVEL", "info"),

		// Security
		AllowAllOrigins: getEnvAsBool("ALLOW_ALL_ORIGINS", true),
		AllowedOrigins:  getEnvAsStringSlice("ALLOWED_ORIGINS", []string{"*"}),
		MaxConnPerIP:    getEnvAsInt("MAX_CONN_PER_IP", 5),
	}

	return config
}

// getEnv retrieves an environment variable with a default value
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// getEnvAsInt retrieves an environment variable as an integer
func getEnvAsInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// getEnvAsBool retrieves an environment variable as a boolean
func getEnvAsBool(key string, defaultValue bool) bool {
	if value, exists := os.LookupEnv(key); exists {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}

func getEnvAsStringSlice(key string, defaultValue []string) []string {
	if value, exists := os.LookupEnv(key); exists {
		return strings.Split(value, ",")
	}
	return defaultValue
}
