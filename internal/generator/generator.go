// Package generator produces a single, securely-random character drawn from a
// configurable character set.
//
// Randomness is sourced exclusively from crypto/rand, and selection is
// performed with crypto/rand.Int which uses rejection sampling internally to
// avoid the modulo bias that a naive `rand % len` would introduce. Every
// public method is instrumented with an OpenTelemetry span.
package generator

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"

	"github.com/cudneys/pwgen-service-char/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// DefaultCharset is a strong, password-friendly character set spanning
// lowercase, uppercase, digits and common symbols.
const DefaultCharset = "abcdefghijklmnopqrstuvwxyz" +
	"ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
	"0123456789" +
	"!@#$%^&*()-_=+[]{};:,.<>?"

// ErrEmptyCharset is returned when a Generator is constructed without any
// characters to choose from.
var ErrEmptyCharset = errors.New("generator: charset must not be empty")

// Generator securely selects a single random character from its charset.
//
// A Generator is immutable after construction and safe for concurrent use by
// multiple goroutines.
type Generator struct {
	charset []rune
}

// New returns a Generator backed by the given charset. If charset is empty,
// DefaultCharset is used. The charset is interpreted as a sequence of runes so
// multi-byte characters are selected as whole characters.
func New(charset string) *Generator {
	if charset == "" {
		charset = DefaultCharset
	}
	return &Generator{charset: []rune(charset)}
}

// Size reports the number of distinct characters the Generator can produce.
func (g *Generator) Size() int { return len(g.charset) }

// GenerateChar returns a single, cryptographically-random character from the
// configured charset. The operation is recorded as the "generator.GenerateChar"
// span on the supplied context.
func (g *Generator) GenerateChar(ctx context.Context) (string, error) {
	_, span := telemetry.Tracer().Start(ctx, "generator.GenerateChar")
	defer span.End()

	n := len(g.charset)
	span.SetAttributes(attribute.Int("pwgen.charset.size", n))

	if n == 0 {
		span.RecordError(ErrEmptyCharset)
		span.SetStatus(codes.Error, ErrEmptyCharset.Error())
		return "", ErrEmptyCharset
	}

	// crypto/rand.Int returns a uniform value in [0, n) using rejection
	// sampling, eliminating modulo bias.
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "secure random source failed")
		return "", err
	}

	char := string(g.charset[idx.Int64()])
	span.SetStatus(codes.Ok, "")
	return char, nil
}
