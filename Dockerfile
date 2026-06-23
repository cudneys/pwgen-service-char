# syntax=docker/dockerfile:1

# ---- build stage -----------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Version stamped into the binary. The release pipeline passes the git tag
# (e.g. --build-arg VERSION=1.2.3); local builds fall back to "dev".
ARG VERSION=dev

# Cache module downloads separately from the source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build a fully static, stripped binary so it can run on `scratch`.
#   CGO_ENABLED=0 -> no libc dependency
#   -ldflags "-s -w" -> drop debug info / symbol table for a smaller binary
#   -X .../telemetry.version=$VERSION -> stamp the build version into traces
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X github.com/cudneys/pwgen-service-char/internal/telemetry.version=${VERSION}" \
        -o /pwgen .

# ---- runtime stage ---------------------------------------------------------
# `scratch` is the most minimal base possible: it contains nothing but our
# binary. CA certificates are copied in so outbound TLS (e.g. OTLP export to a
# remote collector) works.
FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /pwgen /pwgen

# Run as an unprivileged user (numeric, since scratch has no /etc/passwd).
USER 65534:65534

EXPOSE 8080
ENV PWGEN_ADDR=":8080"

ENTRYPOINT ["/pwgen"]
