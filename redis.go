package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
)

// loadCanvasFromRedis loads the canvas state from Redis
func loadCanvasFromRedis() {
	start := time.Now()
	log.Debug("Attempting to load canvas from Redis")

	canvasJSON, err := rdb.Get(ctx, RedisCanvasKey).Result()
	if err == redis.Nil {
		// Le canvas n'existe pas encore, c'est normal
		log.Info("No existing canvas found in Redis, starting fresh")
		redisOperationsTotal.WithLabelValues("get", "miss").Inc()
		return
	} else if err != nil {
		log.Errorf("Error loading canvas from Redis: %v", err)
		redisOperationsTotal.WithLabelValues("get", "error").Inc()
		return
	}

	redisOperationsTotal.WithLabelValues("get", "success").Inc()
	log.Debugf("Canvas data retrieved from Redis (%d bytes)", len(canvasJSON))

	// Unmarshaller le canvas
	var loadedCanvas Canvas
	if err := json.Unmarshal([]byte(canvasJSON), &loadedCanvas); err != nil {
		log.Errorf("Error unmarshaling canvas: %v", err)
		return
	}

	// Mettre à jour notre canvas
	canvasMutex.Lock()
	canvas = loadedCanvas
	canvasMutex.Unlock()

	duration := time.Since(start)
	tileUpdateLatency.Observe(duration.Seconds())

	log.Infof("Canvas loaded from Redis successfully with %d tiles in %.3fs",
		len(loadedCanvas.Tiles), duration.Seconds())

	// Mettre à jour la métrique de taille du canvas
	updateCanvasSizeMetric()
}

// saveCanvasToRedis saves the current canvas state to Redis
func saveCanvasToRedis() error {
	start := time.Now()
	log.Debug("💾 Saving canvas to Redis")

	canvasMutex.RLock()
	canvasJSON, err := json.Marshal(canvas)
	tileCount := len(canvas.Tiles)
	canvasMutex.RUnlock()

	if err != nil {
		log.WithFields(logrus.Fields{
			"error": err,
		}).Error("❌ Error marshaling canvas")
		redisOperationsTotal.WithLabelValues("set", "error").Inc()
		return fmt.Errorf("error marshaling canvas: %v", err)
	}

	log.WithFields(logrus.Fields{
		"size_bytes": len(canvasJSON),
	}).Debug("🔄 Canvas serialized to JSON")

	err = rdb.Set(ctx, RedisCanvasKey, canvasJSON, 0).Err()
	if err != nil {
		log.WithFields(logrus.Fields{
			"error": err,
		}).Error("❌ Error saving canvas to Redis")
		redisOperationsTotal.WithLabelValues("set", "error").Inc()
		return err
	}

	duration := time.Since(start)
	redisOperationsTotal.WithLabelValues("set", "success").Inc()

	log.WithFields(logrus.Fields{
		"tile_count":  tileCount,
		"duration_ms": float64(duration.Microseconds()) / 1000.0,
		"size_bytes":  len(canvasJSON),
	}).Info("✅ Canvas saved to Redis")

	return nil
}

// subscribeToRedisUpdates subscribes to Redis pub/sub for canvas updates
func subscribeToRedisUpdates() {
	log.Infof("Starting Redis subscription to channel %s", RedisChannel)

	pubsub := rdb.Subscribe(ctx, RedisChannel)
	defer pubsub.Close()

	log.Info("Redis subscription established")

	ch := pubsub.Channel()
	msgCount := 0

	for msg := range ch {
		msgCount++
		start := time.Now()
		log.Debugf("Received message #%d from Redis channel", msgCount)

		var update TileUpdate
		if err := json.Unmarshal([]byte(msg.Payload), &update); err != nil {
			log.Errorf("Error unmarshaling update: %v", err)
			tileUpdatesTotal.WithLabelValues("parse_error").Inc()
			continue
		}

		log.Debugf("Message decoded: User %s placed tile at (%d,%d) with color %s",
			update.UserID, update.Tile.X, update.Tile.Y, update.Tile.Color)

		// Appliquer la mise à jour
		applyTileUpdate(update)

		// Diffuser la mise à jour à tous les clients WebSocket
		broadcastUpdate(update)

		duration := time.Since(start)
		log.Debugf("Update processed and broadcasted in %.3fs", duration.Seconds())
		tileUpdateLatency.Observe(duration.Seconds())
		tileUpdatesTotal.WithLabelValues("success").Inc()
	}

	log.Info("Redis subscription loop exited")
}
