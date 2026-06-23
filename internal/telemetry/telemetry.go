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
//
// When OTEL_EXPORTER_OTLP_TOKEN is set, its value is sent as a bearer token in
// the Authorization header of every OTLP export. In Kubernetes this is sourced
// from the "token" key of the "otel-bearer-token" secret.
//
// POD_NAME and POD_NAMESPACE, when set (via the Kubernetes downward API), are
// attached to every span as the k8s.pod.name and k8s.namespace.name resource
// attributes.
//
// OTEL_SERVICE_NAME overrides the logical service name reported for spans and
// logs, so the same binary deployed for different character sets (lowercase,
// uppercase, symbol, number) can report distinct names.
package telemetry

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// defaultServiceName is reported when OTEL_SERVICE_NAME is not set.
const defaultServiceName = "pwgen-service-char"

// ServiceName returns the logical name reported for every span and log emitted
// by this service; it is also used as the instrumentation scope for Tracer.
//
// The value is read from the OTEL_SERVICE_NAME environment variable so each
// deployment can report a distinct name (e.g. "lowercase", "uppercase",
// "symbol", "number"), falling back to defaultServiceName when unset. This is
// the same variable the OTel SDK reads via resource.WithFromEnv, so the span
// resource attribute and this value always agree.
func ServiceName() string {
	if v := os.Getenv("OTEL_SERVICE_NAME"); v != "" {
		return v
	}
	return defaultServiceName
}

// version is the reported service version, attached to every span via the
// service.version resource attribute. It is stamped at build time from the git
// tag via -ldflags "-X .../internal/telemetry.version=<v>" (see Dockerfile);
// unstamped builds report "dev".
var version = "dev"

// Tracer returns the process-wide tracer used to instrument functions.
//
// It is safe to call before Setup has run: until a provider is installed the
// returned tracer is a no-op, so instrumentation never panics.
func Tracer() trace.Tracer {
	return otel.Tracer(ServiceName())
}

// Setup installs the global TracerProvider and propagator and returns a
// shutdown function that flushes any buffered spans. The shutdown function is
// safe to call exactly once during graceful termination.
func Setup(ctx context.Context, logger *slog.Logger) (shutdown func(context.Context) error, err error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(ServiceName()),
		semconv.ServiceVersion(version),
	}
	attrs = append(attrs, kubernetesAttributes()...)

	res, err := resource.New(ctx,
		resource.WithAttributes(attrs...),
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
		var opts []otlptracegrpc.Option
		if token := os.Getenv("OTEL_EXPORTER_OTLP_TOKEN"); token != "" {
			opts = append(opts, otlptracegrpc.WithHeaders(map[string]string{
				"Authorization": "Bearer " + token,
			}))
		}
		exp, err := otlptracegrpc.New(ctx, opts...)
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

// kubernetesAttributes returns resource attributes describing the Kubernetes
// pod the service runs in. They are sourced from the POD_NAME and POD_NAMESPACE
// environment variables, which are populated from the downward API in the
// deployment manifest. Attributes are only included when their variable is set,
// so the service still runs cleanly outside Kubernetes.
func kubernetesAttributes() []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if name := os.Getenv("POD_NAME"); name != "" {
		attrs = append(attrs, semconv.K8SPodName(name))
	}
	if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
		attrs = append(attrs, semconv.K8SNamespaceName(namespace))
	}
	return attrs
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