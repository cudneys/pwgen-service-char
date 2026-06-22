package generator

import (
	"context"
	"strings"
	"testing"
)

func TestGenerateCharWithinCharset(t *testing.T) {
	const charset = "abcXYZ123"
	g := New(charset)

	for i := 0; i < 1000; i++ {
		got, err := g.GenerateChar(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len([]rune(got)) != 1 {
			t.Fatalf("expected a single character, got %q", got)
		}
		if !strings.Contains(charset, got) {
			t.Fatalf("character %q not in charset %q", got, charset)
		}
	}
}

func TestNewEmptyUsesDefault(t *testing.T) {
	g := New("")
	if g.Size() != len([]rune(DefaultCharset)) {
		t.Fatalf("expected default charset size %d, got %d", len([]rune(DefaultCharset)), g.Size())
	}
}

func TestGenerateCharCoversCharset(t *testing.T) {
	const charset = "ab"
	g := New(charset)

	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		c, err := g.GenerateChar(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen[c] = true
	}
	for _, r := range charset {
		if !seen[string(r)] {
			t.Errorf("character %q was never produced across 500 draws", string(r))
		}
	}
}
