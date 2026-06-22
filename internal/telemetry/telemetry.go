// Package telemetry wires up OpenTelemetry tracing for the service.
//
// It configures a global TracerProvider, a text-map propagator and exposes a
// process-wide Tracer that the rest of the application uses to instrument its
// endpoints and functions.
//
// Exporter selection is driven by the environment so the same binary can talk
// to a real OTEL agent/collector in production while remaining runnable in a
// bare development environment:
//
//   - If OTEL_EXPORTER_OTLP_ENDPOINT (or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT) is
//     set, traces are exported over OTLP/gRPC to that agent.
//   - Otherwise traces are written to stdout, so spans are always observable.
package telemetry

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// ServiceName is the logical name reported for every span emitted by this
// service. It is also used as the instrumentation scope for Tracer.
const ServiceName = "pwgen-service-char"

// version is the reported service version. It can be overridden at build time
// via -ldflags "-X .../internal/telemetry.version=<v>".
var version = "0.1.0"

// Tracer returns the process-wide tracer used to instrument functions.
//
// It is safe to call before Setup has run: until a provider is installed the
// returned tracer is a no-op, so instrumentation never panics.
func Tracer() trace.Tracer {
	return otel.Tracer(ServiceName)
}

// Setup installs the global TracerProvider and propagator and returns a
// shutdown function that flushes any buffered spans. The shutdown function is
// safe to call exactly once during graceful termination.
func Setup(ctx context.Context, logger *slog.Logger) (shutdown func(context.Context) error, err error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(ServiceName),
			semconv.ServiceVersion(version),
		),
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithHost(),
	)
	// Detectors may legitimately fail in minimal runtimes (e.g. process-owner
	// detection on a `scratch` image with no /etc/passwd). resource.New still
	// returns a usable resource in that case, so this is a warning, not fatal.
	if err != nil {
		logger.Warn("partial otel resource detection", slog.Any("error", err))
	}
	if res == nil {
		res = resource.Default()
	}

	exporter, kind, err := newExporter(ctx)
	if err != nil {
		return nil, err
	}
	logger.Info("otel exporter configured", slog.String("exporter", kind))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// newExporter builds a span exporter based on the environment, returning the
// exporter and a short label describing which kind was selected.
func newExporter(ctx context.Context) (sdktrace.SpanExporter, string, error) {
	if otlpEndpointConfigured() {
		exp, err := otlptracegrpc.New(ctx)
		if err != nil {
			return nil, "", err
		}
		return exp, "otlp/grpc", nil
	}

	exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, "", err
	}
	return exp, "stdout", nil
}

// otlpEndpointConfigured reports whether an OTLP agent endpoint has been
// provided through the standard OpenTelemetry environment variables.
func otlpEndpointConfigured() bool {
	for _, k := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
	} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}