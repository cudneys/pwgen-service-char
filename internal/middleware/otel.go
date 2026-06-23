// Package middleware provides Fiber v3 middleware that instruments every
// incoming HTTP request with an OpenTelemetry server span.
package middleware

import (
	"github.com/cudneys/pwgen-service-char/internal/telemetry"
	"github.com/gofiber/fiber/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// OTel returns Fiber middleware that starts a SERVER span for every request,
// propagates trace context from inbound headers, and records standard HTTP
// semantic-convention attributes plus the final response status.
//
// The span's context is stored on the Fiber Ctx so downstream handlers and the
// functions they call become children of the request span.
func OTel() fiber.Handler {
	tracer := telemetry.Tracer()
	propagator := otel.GetTextMapPropagator()

	return func(c fiber.Ctx) error {
		// Skip tracing for the health endpoint: it is hit frequently by kubelet
		// probes and produces no useful spans, so excluding it keeps trace
		// volume focused on real traffic.
		if c.Path() == "/healthz" {
			return c.Next()
		}

		// Continue any trace started upstream by extracting W3C context.
		ctx := propagator.Extract(c.Context(), &headerCarrier{c: c})

		// The matched route is not resolved until c.Next() runs, so start the
		// span named by the request path and refine it to the route template
		// afterwards.
		ctx, span := tracer.Start(ctx, c.Method()+" "+c.Path(),
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(c.Method()),
				semconv.URLPath(c.Path()),
				semconv.URLScheme(c.Scheme()),
				semconv.ServerAddress(c.Hostname()),
				semconv.ClientAddress(c.IP()),
			),
		)
		defer span.End()

		// Make the span the parent of everything the handler does.
		c.SetContext(ctx)

		err := c.Next()

		// Now that the route is resolved, name the span by its template
		// (e.g. "GET /generate") so spans group by endpoint, not by raw URL.
		if route := c.Route().Path; route != "" {
			span.SetName(c.Method() + " " + route)
			span.SetAttributes(semconv.HTTPRoute(route))
		}

		status := c.Response().StatusCode()
		span.SetAttributes(semconv.HTTPResponseStatusCode(status))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else if status >= 500 {
			span.SetStatus(codes.Error, "server error")
		} else {
			span.SetStatus(codes.Ok, "")
		}

		return err
	}
}

// headerCarrier adapts Fiber's request headers to the TextMapCarrier interface
// so the propagator can read inbound trace context.
type headerCarrier struct {
	c fiber.Ctx
}

func (h *headerCarrier) Get(key string) string {
	return h.c.Get(key)
}

func (h *headerCarrier) Set(key, value string) {
	h.c.Request().Header.Set(key, value)
}

func (h *headerCarrier) Keys() []string {
	headers := h.c.GetReqHeaders()
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	return keys
}

// ensure headerCarrier satisfies the propagation interface.
var _ propagation.TextMapCarrier = (*headerCarrier)(nil)
