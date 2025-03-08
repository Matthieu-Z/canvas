package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// instrumentHandler wraps an HTTP handler with metrics
func instrumentHandler(path string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// For WebSocket endpoints, pass through directly
		if path == "/ws" && r.Header.Get("Upgrade") == "websocket" {
			next(w, r)
			httpRequestsTotal.WithLabelValues(path, "101").Inc()
			return
		}

		start := time.Now()

		// Use only the custom ResponseWriter
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next(rw, r) // Single call to handler

		duration := time.Since(start).Seconds()
		statusCode := fmt.Sprintf("%d", rw.statusCode)

		httpRequestsTotal.WithLabelValues(path, statusCode).Inc()
		httpRequestDuration.WithLabelValues(path).Observe(duration)

		log.WithFields(logrus.Fields{
			"method":   r.Method,
			"path":     path,
			"duration": duration,
		}).Debug("HTTP request completed")
	}
}

// getCanvasHandler returns the current state of the canvas
func getCanvasHandler(w http.ResponseWriter, r *http.Request) {
	log.Infof("Canvas state requested from %s", r.RemoteAddr)

	canvasMutex.RLock()
	tileCount := len(canvas.Tiles)
	canvasJSON, err := json.Marshal(canvas)
	canvasMutex.RUnlock()

	if err != nil {
		log.Errorf("Error serializing canvas: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(canvasJSON)

	log.Infof("Canvas with %d tiles sent to client (%d bytes)", tileCount, len(canvasJSON))
}

// placeTileHandler handles placing a tile on the canvas
func placeTileHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	activeTileUpdates.Inc()
	defer activeTileUpdates.Dec()

	log.WithFields(logrus.Fields{
		"remote_addr": r.RemoteAddr,
		"method":      r.Method,
	}).Info("📥 Received tile placement request")

	var tileUpdate TileUpdate
	if err := json.NewDecoder(r.Body).Decode(&tileUpdate); err != nil {
		log.WithFields(logrus.Fields{
			"error": err,
		}).Error("❌ Invalid request body")
		tileUpdatesTotal.WithLabelValues("invalid_request").Inc()
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	log.WithFields(logrus.Fields{
		"user_id": tileUpdate.UserID,
		"x":       tileUpdate.Tile.X,
		"y":       tileUpdate.Tile.Y,
		"color":   tileUpdate.Tile.Color,
	}).Debug("🎨 Decoded tile placement request")

	// Check if user can place a tile
	userMutex.RLock()
	lastPlace, exists := userLastPlace[tileUpdate.UserID]
	userMutex.RUnlock()

	now := time.Now()
	if exists && now.Sub(lastPlace) < cfg.TileCooldown {
		waitTime := cfg.TileCooldown - now.Sub(lastPlace)
		log.Warnf("User %s on cooldown, must wait %.1f seconds",
			tileUpdate.UserID, waitTime.Seconds())

		userCooldownRejections.Inc()
		tileUpdatesTotal.WithLabelValues("cooldown").Inc()
		http.Error(w, fmt.Sprintf("Please wait %.0f seconds before placing another tile", waitTime.Seconds()), http.StatusTooManyRequests)
		return
	}

	// Validate coordinates
	if tileUpdate.Tile.X < 0 || tileUpdate.Tile.X >= cfg.CanvasWidth ||
		tileUpdate.Tile.Y < 0 || tileUpdate.Tile.Y >= cfg.CanvasHeight {
		log.Warnf("Invalid coordinates (%d,%d) from user %s",
			tileUpdate.Tile.X, tileUpdate.Tile.Y, tileUpdate.UserID)

		tileUpdatesTotal.WithLabelValues("invalid_coordinates").Inc()
		http.Error(w, "Invalid tile coordinates", http.StatusBadRequest)
		return
	}

	// Update the user's last place time
	userMutex.Lock()
	userLastPlace[tileUpdate.UserID] = now
	userMutex.Unlock()

	log.Infof("User %s placing tile at (%d,%d) with color %s",
		tileUpdate.UserID, tileUpdate.Tile.X, tileUpdate.Tile.Y, tileUpdate.Tile.Color)

	// Apply the update locally
	applyTileUpdate(tileUpdate)

	// Save to Redis
	if err := saveCanvasToRedis(); err != nil {
		log.Errorf("Error saving canvas to Redis: %v", err)
	}

	// Publish update to Redis
	updateJSON, _ := json.Marshal(tileUpdate)
	if err := rdb.Publish(ctx, RedisChannel, updateJSON).Err(); err != nil {
		log.Errorf("Error publishing update to Redis: %v", err)
		redisOperationsTotal.WithLabelValues("publish", "error").Inc()
	} else {
		redisOperationsTotal.WithLabelValues("publish", "success").Inc()
		log.Debug("Update published to Redis")
	}

	duration := time.Since(start)
	tileUpdateLatency.Observe(duration.Seconds())
	tileUpdatesTotal.WithLabelValues("success").Inc()

	log.Infof("Tile placed successfully in %.3fs", duration.Seconds())

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Tile placed successfully"))
}

// applyTileUpdate applies a tile update to the canvas
func applyTileUpdate(update TileUpdate) {
	canvasMutex.Lock()
	defer canvasMutex.Unlock()

	key := fmt.Sprintf("%d,%d", update.Tile.X, update.Tile.Y)
	canvas.Tiles[key] = update.Tile

	log.Debugf("Applied tile at position (%d,%d) with color %s from user %s",
		update.Tile.X, update.Tile.Y, update.Tile.Color, update.UserID)
}
