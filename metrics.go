package main

import (
	"encoding/json"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
)

// Prometheus metrics
var (
	tileUpdatesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "canvas_tile_updates_total",
			Help: "Total number of tile updates by result",
		},
		[]string{"result"},
	)

	tileUpdateLatency = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "canvas_tile_update_latency_seconds",
			Help:    "Latency of tile update operations",
			Buckets: prometheus.LinearBuckets(0.001, 0.01, 10),
		},
	)

	activeTileUpdates = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "canvas_active_tile_updates",
			Help: "Number of tile updates in progress",
		},
	)

	activeWebSocketConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "canvas_active_websocket_connections",
			Help: "Number of active WebSocket connections",
		},
	)

	canvasSizeBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "canvas_size_bytes",
			Help: "Size of canvas in bytes when serialized to JSON",
		},
	)

	redisOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "canvas_redis_operations_total",
			Help: "Total number of Redis operations by type and result",
		},
		[]string{"operation", "result"},
	)

	userCooldownRejections = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "canvas_cooldown_rejections_total",
			Help: "Total number of tile placements rejected due to cooldown",
		},
	)

	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "canvas_http_requests_total",
			Help: "Total number of HTTP requests by endpoint and status",
		},
		[]string{"endpoint", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "canvas_http_request_duration_seconds",
			Help:    "Duration of HTTP requests by endpoint",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"endpoint"},
	)
)

// updateCanvasSizeMetric updates the canvas size metric
func updateCanvasSizeMetric() {
	canvasMutex.RLock()
	canvasJSON, err := json.Marshal(canvas)
	canvasMutex.RUnlock()

	if err == nil {
		canvasSizeBytes.Set(float64(len(canvasJSON)))
		log.WithFields(logrus.Fields{
			"bytes": len(canvasJSON),
		}).Debug("Canvas size updated")
	}
}
