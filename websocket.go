package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// broadcastUpdate envoie une mise à jour de tuile à tous les clients WebSocket connectés
func broadcastUpdate(update TileUpdate) {
	start := time.Now()

	// Create a structured message with type and payload
	message := WSMessage{
		Type:    "tile_update",
		Payload: update,
	}

	messageJSON, err := json.Marshal(message)
	if err != nil {
		log.WithFields(logrus.Fields{
			"error": err,
		}).Error("❌ Error while serializing tile update")
		return
	}

	clientsMutex.RLock()
	clientCount := len(clients)
	log.WithFields(logrus.Fields{
		"user_id":      update.UserID,
		"client_count": clientCount,
		"tile_x":       update.Tile.X,
		"tile_y":       update.Tile.Y,
		"color":        update.Tile.Color,
	}).Debug("📡 Broadcasting tile update to connected clients")

	successCount := 0
	errorCount := 0
	clientsToRemove := []*websocket.Conn{}

	for client, clientUserID := range clients {
		client.SetWriteDeadline(time.Now().Add(WriteWait))
		if err := client.WriteMessage(websocket.TextMessage, messageJSON); err != nil {
			errorCount++
			log.WithFields(logrus.Fields{
				"client_id": clientUserID,
				"error":     err,
			}).Warn("❌ Failed to broadcast update to client")

			// Mark this client for removal if it's a connection error
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) ||
				err.Error() == "write: broken pipe" ||
				err.Error() == "i/o timeout" {
				clientsToRemove = append(clientsToRemove, client)
			}
		} else {
			successCount++
		}
	}
	clientsMutex.RUnlock()

	// Remove disconnected clients
	if len(clientsToRemove) > 0 {
		clientsMutex.Lock()
		for _, client := range clientsToRemove {
			userID := clients[client]
			delete(clients, client)
			client.Close()
			log.WithFields(logrus.Fields{
				"user_id": userID,
			}).Info("🧹 Disconnected client removed after broadcast failure")
		}
		remainingClients := len(clients)
		clientsMutex.Unlock()

		// Update metrics
		activeWebSocketConnections.Set(float64(remainingClients))

		// Broadcast the new user count
		go broadcastUsersCount()
	}

	duration := time.Since(start)
	log.WithFields(logrus.Fields{
		"success_count":   successCount,
		"total_clients":   clientCount,
		"error_count":     errorCount,
		"removed_clients": len(clientsToRemove),
		"duration_ms":     float64(duration.Microseconds()) / 1000.0,
	}).Info("✨ Broadcast complete")
}

// broadcastUsersCount envoie le nombre d'utilisateurs en ligne à tous les clients
func broadcastUsersCount() {
	// Get the number of users from Redis
	count, err := rdb.SCard(ctx, RedisUsersKey).Result()
	if err != nil {
		log.WithFields(logrus.Fields{
			"error": err,
		}).Error("❌ Error while retrieving user count from Redis")
		return
	}

	log.WithFields(logrus.Fields{
		"count": count,
	}).Debug("📊 Online user count retrieved from Redis")

	// Create the message
	message := WSMessage{
		Type:    "users_count",
		Payload: count,
	}

	messageJSON, err := json.Marshal(message)
	if err != nil {
		log.WithFields(logrus.Fields{
			"error": err,
		}).Error("❌ Error while serializing users_count message")
		return
	}

	// Broadcast the message to all connected clients
	clientsMutex.RLock()
	clientCount := len(clients)
	for client, _ := range clients {
		client.SetWriteDeadline(time.Now().Add(WriteWait))
		client.WriteMessage(websocket.TextMessage, messageJSON)
		// We ignore errors - disconnected clients will be cleaned up elsewhere
	}
	clientsMutex.RUnlock()

	log.WithFields(logrus.Fields{
		"users_count":  count,
		"client_count": clientCount,
	}).Debug("✅ User count broadcast completed")
}

// startUserCountBroadcaster démarre une goroutine qui diffuse périodiquement le nombre d'utilisateurs
func startUserCountBroadcaster() {
	log.Info("🚀 Starting periodic user count broadcaster")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		broadcastUsersCount()
	}
}

// websocketHandler gère la mise à niveau de la connexion HTTP vers WebSocket
func websocketHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		log.Warn("❌ Connection attempt without userId")
		http.Error(w, "Missing userId parameter", http.StatusBadRequest)
		return
	}

	log.WithFields(logrus.Fields{
		"user_id":     userID,
		"remote_addr": r.RemoteAddr,
	}).Info("🔌 New WebSocket connection request")

	// Check if the user is already connected and close the old connection
	var oldConnection *websocket.Conn
	clientsMutex.Lock()
	for ws, id := range clients {
		if id == userID {
			oldConnection = ws
			break
		}
	}
	clientsMutex.Unlock()

	if oldConnection != nil {
		log.WithFields(logrus.Fields{
			"user_id": userID,
		}).Info("🔄 Closing old connection for user")

		// Properly close the old connection
		oldConnection.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Nouvelle connexion établie"))
		oldConnection.Close()

		// Wait a bit for the closure to be processed
		time.Sleep(100 * time.Millisecond)
	}

	// Configure the upgrader
	upgrader.CheckOrigin = func(r *http.Request) bool {
		return true
	}

	// Upgrade the HTTP connection to WebSocket
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.WithFields(logrus.Fields{
			"error":   err,
			"user_id": userID,
		}).Error("❌ Error during WebSocket connection upgrade")
		return
	}

	// Configure WebSocket timeouts
	ws.SetReadLimit(1024)
	ws.SetReadDeadline(time.Now().Add(PongWait))
	ws.SetPongHandler(func(string) error {
		log.WithFields(logrus.Fields{
			"user_id": userID,
		}).Debug("🏓 Pong received from client")
		ws.SetReadDeadline(time.Now().Add(PongWait))
		return nil
	})

	// Add the user to Redis with TTL
	err = rdb.SAdd(ctx, RedisUsersKey, userID).Err()
	if err != nil {
		log.WithFields(logrus.Fields{
			"error":   err,
			"user_id": userID,
		}).Error("❌ Error while adding user to Redis")
	} else {
		// Set a TTL on the user set
		if err := rdb.Expire(ctx, RedisUsersKey, 30*time.Second).Err(); err != nil {
			log.WithFields(logrus.Fields{
				"error": err,
			}).Error("❌ Error while setting TTL on user set")
		}

		log.WithFields(logrus.Fields{
			"user_id": userID,
			"ttl":     30 * time.Second,
		}).Debug("✅ User added to Redis with TTL")
	}

	// Register the client
	clientsMutex.Lock()
	clients[ws] = userID
	clientCount := len(clients)
	clientsMutex.Unlock()

	// Update metrics
	activeWebSocketConnections.Set(float64(clientCount))

	log.WithFields(logrus.Fields{
		"user_id":      userID,
		"client_count": clientCount,
	}).Info("✅ New WebSocket connection established")

	// Start the ping goroutine
	go func() {
		ticker := time.NewTicker(PingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ws.SetWriteDeadline(time.Now().Add(WriteWait))
				if err := ws.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
					log.WithFields(logrus.Fields{
						"error":   err,
						"user_id": userID,
					}).Debug("❌ Error while sending ping, closing connection")
					return
				}

				log.WithFields(logrus.Fields{
					"user_id": userID,
				}).Debug("🏓 Ping sent to client")
			}
		}
	}()

	// Broadcast updated user count
	go broadcastUsersCount()

	// Send initial canvas state
	canvasMutex.RLock()
	canvasData := canvas
	tileCount := len(canvas.Tiles)
	canvasMutex.RUnlock()

	// Create structured message with canvas state
	canvasMessage := WSMessage{
		Type:    "canvas_state",
		Payload: canvasData,
	}

	initialState, err := json.Marshal(canvasMessage)
	if err != nil {
		log.WithFields(logrus.Fields{
			"error":   err,
			"user_id": userID,
		}).Error("❌ Error while serializing initial canvas")
	} else {
		ws.SetWriteDeadline(time.Now().Add(WriteWait))
		if err := ws.WriteMessage(websocket.TextMessage, initialState); err != nil {
			log.WithFields(logrus.Fields{
				"error":   err,
				"user_id": userID,
			}).Error("❌ Error while sending initial state")
		} else {
			log.WithFields(logrus.Fields{
				"user_id":    userID,
				"tile_count": tileCount,
				"bytes":      len(initialState),
			}).Debug("✅ Initial canvas state sent")
		}
	}

	// Send server information
	hostname, _ := os.Hostname()
	serverInfo := WSMessage{
		Type: "server_info",
		Payload: map[string]string{
			"hostname": hostname,
			"address":  r.Host,
		},
	}

	serverInfoJSON, err := json.Marshal(serverInfo)
	if err == nil {
		ws.SetWriteDeadline(time.Now().Add(WriteWait))
		if err := ws.WriteMessage(websocket.TextMessage, serverInfoJSON); err != nil {
			log.WithFields(logrus.Fields{
				"error":   err,
				"user_id": userID,
			}).Error("❌ Error while sending server information")
		} else {
			log.WithFields(logrus.Fields{
				"user_id":  userID,
				"hostname": hostname,
			}).Debug("✅ Server information sent")
		}
	}

	// Start message handling for this client
	go handleWebSocketClient(ws, userID)
}

// handleWebSocketClient gère une connexion WebSocket pour un client
func handleWebSocketClient(conn *websocket.Conn, userID string) {
	// Add the user to Redis with TTL
	err := rdb.SAdd(ctx, RedisUsersKey, userID).Err()
	if err != nil {
		log.WithFields(logrus.Fields{
			"error":   err,
			"user_id": userID,
		}).Error("❌ Error while adding user to Redis")
	} else {
		// Set a TTL on the user set
		if err := rdb.Expire(ctx, RedisUsersKey, 30*time.Second).Err(); err != nil {
			log.WithFields(logrus.Fields{
				"error": err,
			}).Error("❌ Error while setting TTL on user set")
		}

		log.WithFields(logrus.Fields{
			"user_id": userID,
			"ttl":     30 * time.Second,
		}).Debug("✅ User added to Redis with TTL")
	}

	// Add the client to the clients map
	clientsMutex.Lock()
	clients[conn] = userID
	clientCount := len(clients)
	clientsMutex.Unlock()

	// Update metrics
	activeWebSocketConnections.Set(float64(clientCount))

	log.WithFields(logrus.Fields{
		"user_id":      userID,
		"client_count": clientCount,
	}).Info("👋 New WebSocket client connected")

	// Broadcast updated user count
	go broadcastUsersCount()

	// Configure ping/pong
	conn.SetReadDeadline(time.Now().Add(PongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(PongWait))
		// Refresh the user's TTL in Redis on each pong
		rdb.Expire(ctx, RedisUsersKey, 30*time.Second).Err()
		return nil
	})

	// Start periodic ping
	go func() {
		ticker := time.NewTicker((PongWait * 9) / 10) // PingPeriod
		defer ticker.Stop()
		for {
			<-ticker.C
			conn.SetWriteDeadline(time.Now().Add(WriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.WithFields(logrus.Fields{
					"error":   err,
					"user_id": userID,
				}).Debug("❌ Error while sending ping, closing connection")
				return
			}

			// Refresh the user's TTL in Redis on each ping
			rdb.Expire(ctx, RedisUsersKey, 30*time.Second).Err()
		}
	}()

	// Message reading loop
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.WithFields(logrus.Fields{
					"error":   err,
					"user_id": userID,
				}).Info("❌ WebSocket read error")
			} else {
				log.WithFields(logrus.Fields{
					"user_id": userID,
				}).Debug("👋 WebSocket client disconnected normally")
			}
			break
		}

		// Process the message
		var wsMessage WSMessage
		if err := json.Unmarshal(message, &wsMessage); err != nil {
			log.WithFields(logrus.Fields{
				"error":   err,
				"user_id": userID,
			}).Error("❌ Error while decoding WebSocket message")
			continue
		}

		// Process the message based on its type
		switch wsMessage.Type {
		case "place_tile":
			handlePlaceTile(conn, wsMessage, userID)
		default:
			log.WithFields(logrus.Fields{
				"type":    wsMessage.Type,
				"user_id": userID,
			}).Warn("⚠️ Unknown WebSocket message type")
		}
	}

	// Cleanup on disconnection
	clientsMutex.Lock()
	delete(clients, conn)
	remainingClients := len(clients)
	clientsMutex.Unlock()

	// Update metrics
	activeWebSocketConnections.Set(float64(remainingClients))

	// Check if this is the user's last connection
	shouldRemoveUser := true
	clientsMutex.RLock()
	for _, id := range clients {
		if id == userID {
			shouldRemoveUser = false
			break
		}
	}
	clientsMutex.RUnlock()

	// If it's the last connection, remove the user from Redis
	if shouldRemoveUser {
		if err := rdb.SRem(ctx, RedisUsersKey, userID).Err(); err != nil {
			log.WithFields(logrus.Fields{
				"error":   err,
				"user_id": userID,
			}).Error("❌ Error while removing user from Redis")
		} else {
			log.WithFields(logrus.Fields{
				"user_id": userID,
			}).Debug("✅ User removed from Redis after disconnection")
		}

		// Broadcast updated user count
		go broadcastUsersCount()
	}

	log.WithFields(logrus.Fields{
		"user_id":      userID,
		"client_count": remainingClients,
	}).Info("👋 WebSocket client disconnected")
}

// handlePlaceTile processes a tile placement request
func handlePlaceTile(conn *websocket.Conn, wsMessage WSMessage, userID string) {
	start := time.Now()
	activeTileUpdates.Inc()

	// Extract tile data from payload
	var tileData map[string]interface{}
	payloadJSON, err := json.Marshal(wsMessage.Payload)
	if err != nil {
		log.WithFields(logrus.Fields{
			"error":   err,
			"user_id": userID,
		}).Error("❌ Error while serializing payload")
		activeTileUpdates.Dec()
		return
	}

	if err := json.Unmarshal(payloadJSON, &tileData); err != nil {
		log.WithFields(logrus.Fields{
			"error":   err,
			"user_id": userID,
		}).Error("❌ Error while decoding tile data")
		activeTileUpdates.Dec()
		return
	}

	// Check that the necessary fields are present
	x, xOk := tileData["x"]
	y, yOk := tileData["y"]
	color, colorOk := tileData["color"]

	if !xOk || !yOk || !colorOk || x == nil || y == nil || color == nil {
		log.WithFields(logrus.Fields{
			"user_id": userID,
			"payload": tileData,
		}).Error("❌ Invalid data format: fields x, y and color are required")
		activeTileUpdates.Dec()
		return
	}

	// Convert coordinates to float64
	xFloat, xOk := x.(float64)
	yFloat, yOk := y.(float64)
	colorStr, colorOk := color.(string)

	if !xOk || !yOk || !colorOk {
		log.WithFields(logrus.Fields{
			"user_id":    userID,
			"x_type":     fmt.Sprintf("%T", x),
			"y_type":     fmt.Sprintf("%T", y),
			"color_type": fmt.Sprintf("%T", color),
		}).Error("❌ Invalid data types: x and y must be numbers, color must be a string")
		activeTileUpdates.Dec()
		return
	}

	tileUpdate := TileUpdate{
		UserID: userID,
		Tile: Tile{
			X:     int(xFloat),
			Y:     int(yFloat),
			Color: colorStr,
		},
	}

	// Validate the color
	if tileUpdate.Tile.Color == "" {
		log.WithFields(logrus.Fields{
			"user_id": userID,
			"x":       tileUpdate.Tile.X,
			"y":       tileUpdate.Tile.Y,
		}).Warn("❌ Attempt to place tile with empty color")
		activeTileUpdates.Dec()
		return
	}

	log.WithFields(logrus.Fields{
		"user_id": userID,
		"tile_x":  tileUpdate.Tile.X,
		"tile_y":  tileUpdate.Tile.Y,
		"color":   tileUpdate.Tile.Color,
	}).Debug("🎨 Tile placement requested")

	// Check cooldown
	userMutex.RLock()
	lastPlace, exists := userLastPlace[tileUpdate.UserID]
	userMutex.RUnlock()

	now := time.Now()
	if exists && now.Sub(lastPlace) < cfg.TileCooldown {
		// Ignore this update, user is in cooldown
		waitTime := cfg.TileCooldown - now.Sub(lastPlace)
		log.WithFields(logrus.Fields{
			"user_id":     userID,
			"wait_time_s": waitTime.Seconds(),
		}).Warn("⏳ User in cooldown")

		errorMsg := struct {
			Error       string `json:"error"`
			WaitSeconds int    `json:"waitSeconds"`
		}{
			Error:       "cooldown",
			WaitSeconds: int(waitTime.Seconds()),
		}
		errorJSON, _ := json.Marshal(errorMsg)

		conn.SetWriteDeadline(time.Now().Add(WriteWait))
		if err := conn.WriteMessage(websocket.TextMessage, errorJSON); err != nil {
			log.WithFields(logrus.Fields{
				"error":   err,
				"user_id": userID,
			}).Warn("❌ Error while sending cooldown message")
		}

		activeTileUpdates.Dec()
		userCooldownRejections.Inc()
		tileUpdatesTotal.WithLabelValues("cooldown").Inc()
		return
	}

	// Update the user's last placement time
	userMutex.Lock()
	userLastPlace[tileUpdate.UserID] = now
	userMutex.Unlock()

	log.WithFields(logrus.Fields{
		"user_id": userID,
		"tile_x":  tileUpdate.Tile.X,
		"tile_y":  tileUpdate.Tile.Y,
		"color":   tileUpdate.Tile.Color,
	}).Info("✅ Tile placement accepted")

	// Apply the update
	applyTileUpdate(tileUpdate)

	// Save to Redis
	if err := saveCanvasToRedis(); err != nil {
		log.WithFields(logrus.Fields{
			"error":   err,
			"user_id": userID,
		}).Error("❌ Error while saving canvas to Redis")
	}

	// Publish the update to Redis
	updateJSON, _ := json.Marshal(tileUpdate)
	if err := rdb.Publish(ctx, RedisChannel, updateJSON).Err(); err != nil {
		log.WithFields(logrus.Fields{
			"error":   err,
			"user_id": userID,
		}).Error("❌ Error while publishing update to Redis")
		redisOperationsTotal.WithLabelValues("publish", "error").Inc()
	} else {
		redisOperationsTotal.WithLabelValues("publish", "success").Inc()
		log.WithFields(logrus.Fields{
			"user_id": userID,
		}).Debug("✅ Update published to Redis")
	}

	activeTileUpdates.Dec()
	duration := time.Since(start)
	tileUpdateLatency.Observe(duration.Seconds())
	tileUpdatesTotal.WithLabelValues("success").Inc()

	log.WithFields(logrus.Fields{
		"user_id":     userID,
		"duration_ms": float64(duration.Microseconds()) / 1000.0,
	}).Debug("✅ Tile update processed")
}
