package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// Constants for the application
const (
	RedisCanvasKey = "canvas"
	RedisChannel   = "canvas_updates"
	RedisUsersKey  = "online_users"   // Clé pour stocker les utilisateurs en ligne
	UserTTL        = 10 * time.Minute // TTL pour les entrées utilisateurs dans Redis
	PingInterval   = 10 * time.Second // Réduit de 30s à 10s pour une détection plus rapide
	PongWait       = 15 * time.Second // Réduit de 60s à 15s pour détecter les déconnexions plus rapidement
	WriteWait      = 5 * time.Second  // Réduit de 10s à 5s pour accélérer les timeouts d'écriture
)

var (
	log = logrus.New()
	cfg *Config
)

// Tile represents a single tile on the canvas
type Tile struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Color string `json:"color"`
}

// TileUpdate represents a tile update with user info
type TileUpdate struct {
	UserID string `json:"userId"`
	Tile   Tile   `json:"tile"`
}

// Canvas represents the entire board
type Canvas struct {
	Width  int             `json:"width"`
	Height int             `json:"height"`
	Tiles  map[string]Tile `json:"tiles"` // Key is "x,y"
}

// responseWriter wraps http.ResponseWriter to capture the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Message types for WebSocket
type WSMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// Global variables
var (
	canvas        Canvas
	canvasMutex   sync.RWMutex
	userLastPlace = make(map[string]time.Time)
	userMutex     sync.RWMutex
	clients       = make(map[*websocket.Conn]string) // WebSocket client -> UserID
	clientsMutex  sync.RWMutex
	rdb           *redis.Client
	ctx           = context.Background()
	upgrader      = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins for MVP
		},
	}
)
