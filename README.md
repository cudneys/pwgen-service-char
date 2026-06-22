# pwgen-service-char

A small Go microservice that securely generates a **single, randomly-chosen character**.

- **Framework:** [Fiber v3](https://github.com/gofiber/fiber)
- **Randomness:** `crypto/rand` (uniform, no modulo bias via `rand.Int`)
- **Tracing:** OpenTelemetry — every endpoint *and* internal function is wrapped in a span
- **Logging:** `log/slog` emitting structured JSON

## Endpoints

| Method | Path        | Description                                   |
| ------ | ----------- | --------------------------------------------- |
| `GET`  | `/generate` | Returns one secure random char: `{"character":"x"}` |
| `GET`  | `/healthz`  | Liveness probe: `{"status":"ok"}`             |

## Run

```bash
go run .
# in another shell
curl localhost:8080/generate
```

## Configuration (environment variables)

| Variable                       | Default                         | Purpose                                              |
| ------------------------------ | ------------------------------- | ---------------------------------------------------- |
| `PWGEN_ADDR`                   | `:8080`                         | Listen address                                       |
| `PWGEN_CHARSET`                | letters + digits + symbols      | Characters to choose from                            |
| `LOG_LEVEL`                    | `info`                          | `debug` \| `info` \| `warn` \| `error`               |
| `OTEL_EXPORTER_OTLP_ENDPOINT`  | *(unset)*                       | If set, exports traces via OTLP/gRPC to that agent   |

### Tracing

The OpenTelemetry tracing agent is installed and configured at startup
(`internal/telemetry`). Behaviour:

- **With** `OTEL_EXPORTER_OTLP_ENDPOINT` (or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`)
  set, spans are exported over **OTLP/gRPC** to your collector/agent.
- **Without** it, spans are printed to **stdout** so the service is observable
  out of the box.

Incoming W3C `traceparent` headers are propagated, so requests join an existing
distributed trace. Span hierarchy per request:

```
GET /generate                (server span, from OTel middleware)
└── handler.GenerateChar      (HTTP handler span)
    └── generator.GenerateChar (secure-random function span)
```

## Layout

```
main.go                       entrypoint, server lifecycle, graceful shutdown
internal/telemetry            OTEL provider/exporter/propagator setup
internal/middleware           Fiber v3 OTEL server-span middleware
internal/handler              HTTP handlers (Fiber v3)
internal/generator            crypto/rand single-character generation
```

## Test

```bash
go test ./...
```
