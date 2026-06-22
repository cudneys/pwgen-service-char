// Command pwgen-service-char is a small microservice that securely generates a
// single randomly-chosen character.
//
// It is built on the Fiber v3 web framework, instruments every endpoint and
// internal function with OpenTelemetry tracing, and emits structured JSON logs
// through log/slog.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cudneys/pwgen-service-char/internal/generator"
	"github.com/cudneys/pwgen-service-char/internal/handler"
	"github.com/cudneys/pwgen-service-char/internal/middleware"
	"github.com/cudneys/pwgen-service-char/internal/telemetry"
	"github.com/gofiber/fiber/v3"
)

func main() {
	logger := newLogger()
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("service exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

// run wires up telemetry, the Fiber app and graceful shutdown.
func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Install the OpenTelemetry tracing agent.
	shutdownTracing, err := telemetry.Setup(ctx, logger)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			logger.Error("error flushing traces", slog.Any("error", err))
		}
	}()

	gen := generator.New(getenv("PWGEN_CHARSET", generator.DefaultCharset))
	h := handler.New(gen, logger)

	app := fiber.New(fiber.Config{
		AppName: telemetry.ServiceName,
	})
	app.Use(middleware.OTel())
	h.Register(app)

	addr := getenv("PWGEN_ADDR", ":8080")
	logger.Info("starting server",
		slog.String("addr", addr),
		slog.String("service", telemetry.ServiceName),
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Listen(addr, fiber.ListenConfig{DisableStartupMessage: true})
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining connections")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return app.ShutdownWithContext(shutdownCtx)
	}
}

// newLogger builds a JSON slog logger writing to stdout. The level can be set
// via the LOG_LEVEL environment variable (debug, info, warn, error).
func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(getenv("LOG_LEVEL", "info"))); err != nil {
		level = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(handler).With(
		slog.String("service", telemetry.ServiceName),
	)
}

// getenv returns the environment variable named by key, or fallback if unset.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
