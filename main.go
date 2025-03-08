package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

func init() {
	// Load configuration
	cfg = LoadConfig()

	// Configure logrus
	level, err := logrus.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = logrus.InfoLevel
	}

	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:    true,
		TimestampFormat:  "2006-01-02 15:04:05.000",
		ForceColors:      true,
		DisableColors:    false,
		PadLevelText:     true,
		QuoteEmptyFields: true,
	})
	log.SetOutput(os.Stdout)
	log.SetLevel(level)
}

func main() {
	log.Info("Starting canvas application MVP")
	log.Infof("Canvas dimensions: %dx%d, Cooldown period: %v", cfg.CanvasWidth, cfg.CanvasHeight, cfg.TileCooldown)

	// Initialize canvas
	canvas = Canvas{
		Width:  cfg.CanvasWidth,
		Height: cfg.CanvasHeight,
		Tiles:  make(map[string]Tile),
	}
	log.Debug("Canvas initialized")

	// Gestion du graceful shutdown
	done := make(chan bool)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Connect to Redis
	redisAddr := fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort)
	log.Infof("Connecting to Redis at %s", redisAddr)
	rdb = redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	// Ping Redis to verify connection
	pong, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Errorf("Failed to connect to Redis: %v", err)
		panic(err)
	}
	log.Infof("Redis connection established: %s", pong)

	// Try to load canvas from Redis
	log.Info("Attempting to load canvas state from Redis")
	loadCanvasFromRedis()

	// Set up Redis subscription for updates
	log.Info("Setting up Redis subscription for canvas updates")
	go subscribeToRedisUpdates()

	// Start the periodic user count broadcaster
	log.Info("Starting periodic user count broadcaster")
	go startUserCountBroadcaster()

	// Create router
	r := mux.NewRouter()
	log.Debug("HTTP router created")

	// API endpoints
	r.HandleFunc("/api/canvas", instrumentHandler("/api/canvas", getCanvasHandler)).Methods("GET")
	r.HandleFunc("/api/place", instrumentHandler("/api/place", placeTileHandler)).Methods("POST")
	r.HandleFunc("/ws", instrumentHandler("/ws", websocketHandler))

	// Add metrics endpoint
	r.Handle("/metrics", promhttp.Handler())

	// Add health check endpoint
	r.HandleFunc("/health", instrumentHandler("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})).Methods("GET")

	// Serve static files
	r.PathPrefix("/").Handler(http.FileServer(http.Dir("./static")))
	log.Info("Routes configured")

	// Start server
	addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Start the server in a goroutine
	go func() {
		log.Infof("Server starting on http://%s", addr)
		log.Infof("Metrics available at http://%s/metrics", addr)

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Update initial canvas size metric
	updateCanvasSizeMetric()

	// Attendre le signal de shutdown
	<-quit
	log.Info("Server is shutting down...")

	// Contexte avec timeout pour le shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fermer proprement le serveur
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	// Sauvegarder l'état final dans Redis
	if err := saveCanvasToRedis(); err != nil {
		log.Errorf("Error saving final state to Redis: %v", err)
	}

	// Fermer la connexion Redis
	if err := rdb.Close(); err != nil {
		log.Errorf("Error closing Redis connection: %v", err)
	}

	// Fermer toutes les connexions WebSocket
	clientsMutex.Lock()
	for client := range clients {
		client.Close()
	}
	clientsMutex.Unlock()

	log.Info("Server stopped gracefully")
	close(done)
}
