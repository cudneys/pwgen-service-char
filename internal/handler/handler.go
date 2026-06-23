// Package handler implements the HTTP layer of the service using Fiber v3.
//
// Each handler opens its own OpenTelemetry span (a child of the request span
// created by the OTel middleware) and logs structured JSON via slog.
package handler

import (
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/cudneys/pwgen-service-char/internal/generator"
	"github.com/cudneys/pwgen-service-char/internal/telemetry"
	"github.com/gofiber/fiber/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Bounds for the fault injector used by the distributed-tracing demo. Roughly
// once every faultMin..faultMax requests, GenerateChar returns a random 5xx so
// the trace data shows occasional errors.
const (
	faultMin = 500
	faultMax = 1000
)

// Bounds for the latency injector used by the distributed-tracing demo. Roughly
// once every latencyEveryMin..latencyEveryMax requests, GenerateChar sleeps for
// a random duration in [latencySleepMin, latencySleepMax] so the trace data
// shows occasional slow spans.
const (
	latencyEveryMin = 1000
	latencyEveryMax = 3000
	latencySleepMin = 10 * time.Millisecond
	//latencySleepMax = 750 * time.Millisecond
	latencySleepMax = 1.2 * time.Second
)

// Handler holds the dependencies shared by the HTTP endpoints.
type Handler struct {
	gen    *generator.Generator
	logger *slog.Logger

	// Fault-injection state. Every request increments count; when it reaches
	// threshold the request fails with a random 5xx, then both are reset with
	// a fresh random threshold so the failure cadence varies.
	mu        sync.Mutex
	count     int
	threshold int

	// Latency-injection state, mirroring the fault injector: every request
	// increments latencyCount; when it reaches latencyThreshold the request
	// sleeps for a random duration, then both are reset with a fresh threshold.
	latencyMu        sync.Mutex
	latencyCount     int
	latencyThreshold int
}

// New constructs a Handler with the given generator and logger.
func New(gen *generator.Generator, logger *slog.Logger) *Handler {
	return &Handler{
		gen:              gen,
		logger:           logger,
		threshold:        nextThreshold(),
		latencyThreshold: nextLatencyThreshold(),
	}
}

// nextThreshold picks a random request count in [faultMin, faultMax] after
// which the next injected fault fires.
func nextThreshold() int {
	return faultMin + rand.IntN(faultMax-faultMin+1)
}

// nextLatencyThreshold picks a random request count in
// [latencyEveryMin, latencyEveryMax] after which the next latency spike fires.
func nextLatencyThreshold() int {
	return latencyEveryMin + rand.IntN(latencyEveryMax-latencyEveryMin+1)
}

// shouldInjectFault advances the request counter and reports whether this
// request should fail. When it returns true it also returns a random status
// code in [500, 599] and resets the counter with a fresh threshold.
func (h *Handler) shouldInjectFault() (bool, int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.count++
	if h.count < h.threshold {
		return false, 0
	}

	h.count = 0
	h.threshold = nextThreshold()
	return true, 500 + rand.IntN(100)
}

// shouldInjectLatency advances the latency counter and reports whether this
// request should be delayed. When it returns true it also returns a random
// sleep duration in [latencySleepMin, latencySleepMax] and resets the counter
// with a fresh threshold.
func (h *Handler) shouldInjectLatency() (bool, time.Duration) {
	h.latencyMu.Lock()
	defer h.latencyMu.Unlock()

	h.latencyCount++
	if h.latencyCount < h.latencyThreshold {
		return false, 0
	}

	h.latencyCount = 0
	h.latencyThreshold = nextLatencyThreshold()
	span := latencySleepMax - latencySleepMin
	return true, latencySleepMin + time.Duration(rand.Int64N(int64(span)+1))
}

// Register mounts all routes onto the given Fiber app.
func (h *Handler) Register(app *fiber.App) {
	app.Get("/healthz", h.Health)
	app.Get("/generate", h.GenerateChar)
}

// Health is a liveness probe. It is intentionally not traced: the OTel
// middleware skips /healthz, and starting a span here would create an
// orphaned root span on every probe.
func (h *Handler) Health(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// GenerateChar returns a single securely-random character as JSON:
//
//	{"character": "x"}
func (h *Handler) GenerateChar(c fiber.Ctx) error {
	ctx, span := telemetry.Tracer().Start(c.Context(), "handler.GenerateChar")
	defer span.End()

	// Demo fault injection: occasionally fail with a random 5xx so distributed
	// traces show a few errors.
	if inject, status := h.shouldInjectFault(); inject {
		err := fmt.Errorf("injected fault: HTTP %d", status)
		span.SetAttributes(
			attribute.Bool("demo.injected_fault", true),
			attribute.Int("http.response.status_code", status),
		)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		h.logger.ErrorContext(ctx, "injected demo fault",
			slog.String("trace_id", span.SpanContext().TraceID().String()),
			slog.Int("status", status),
		)
		return c.Status(status).JSON(fiber.Map{"error": "injected demo fault"})
	}

	// Demo latency injection: occasionally sleep so distributed traces show a
	// few slow spans.
	if inject, delay := h.shouldInjectLatency(); inject {
		span.SetAttributes(
			attribute.Bool("demo.injected_latency", true),
			attribute.Int64("demo.injected_latency_ms", delay.Milliseconds()),
		)
		h.logger.InfoContext(ctx, "injected demo latency",
			slog.String("trace_id", span.SpanContext().TraceID().String()),
			slog.Int64("delay_ms", delay.Milliseconds()),
		)
		time.Sleep(delay)
	}

	char, err := h.gen.GenerateChar(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		h.logger.ErrorContext(ctx, "failed to generate character",
			slog.String("trace_id", span.SpanContext().TraceID().String()),
			slog.Any("error", err),
		)
		return c.Status(fiber.StatusInternalServerError).
			JSON(fiber.Map{"error": "failed to generate character"})
	}

	h.logger.InfoContext(ctx, "generated character",
		slog.String("trace_id", span.SpanContext().TraceID().String()),
	)

	return c.JSON(fiber.Map{"character": char})
}
