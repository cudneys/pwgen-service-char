# syntax=docker/dockerfile:1

# ---- build stage -----------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache module downloads separately from the source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build a fully static, stripped binary so it can run on `scratch`.
#   CGO_ENABLED=0 -> no libc dependency
#   -ldflags "-s -w" -> drop debug info / symbol table for a smaller binary
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w" \
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
