// Package handler implements the HTTP layer of the service using Fiber v3.
//
// Each handler opens its own OpenTelemetry span (a child of the request span
// created by the OTel middleware) and logs structured JSON via slog.
package handler

import (
	"log/slog"

	"github.com/cudneys/pwgen-service-char/internal/generator"
	"github.com/cudneys/pwgen-service-char/internal/telemetry"
	"github.com/gofiber/fiber/v3"
	"go.opentelemetry.io/otel/codes"
)

// Handler holds the dependencies shared by the HTTP endpoints.
type Handler struct {
	gen    *generator.Generator
	logger *slog.Logger
}

// New constructs a Handler with the given generator and logger.
func New(gen *generator.Generator, logger *slog.Logger) *Handler {
	return &Handler{gen: gen, logger: logger}
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
