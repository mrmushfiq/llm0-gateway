package cache

import (
	"testing"

	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
)

// newTestCache returns a zero-value ExactCache (no Redis/DB needed for key tests).
func newTestCache() *ExactCache {
	return &ExactCache{}
}

func messages(content ...string) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(content))
	for i, c := range content {
		out[i] = openai.ChatCompletionMessage{Role: "user", Content: c}
	}
	return out
}

// ── Determinism ───────────────────────────────────────────────────────────────

func TestCacheKey_SameInputs_SameKey(t *testing.T) {
	c := newTestCache()
	pid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	msgs := messages("hello world")

	k1, err := c.CacheKey(pid, "openai", "gpt-4o", msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	k2, err := c.CacheKey(pid, "openai", "gpt-4o", msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k1 != k2 {
		t.Errorf("same inputs produced different keys: %q vs %q", k1, k2)
	}
}

// ── Sensitivity ───────────────────────────────────────────────────────────────

func TestCacheKey_DifferentProject_DifferentKey(t *testing.T) {
	c := newTestCache()
	msgs := messages("hello")
	k1, _ := c.CacheKey(uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"), "openai", "gpt-4o", msgs)
	k2, _ := c.CacheKey(uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"), "openai", "gpt-4o", msgs)
	if k1 == k2 {
		t.Error("different project IDs should produce different cache keys")
	}
}

func TestCacheKey_DifferentModel_DifferentKey(t *testing.T) {
	c := newTestCache()
	pid := uuid.New()
	msgs := messages("hello")
	k1, _ := c.CacheKey(pid, "openai", "gpt-4o", msgs)
	k2, _ := c.CacheKey(pid, "openai", "gpt-4o-mini", msgs)
	if k1 == k2 {
		t.Error("different models should produce different cache keys")
	}
}

func TestCacheKey_DifferentProvider_DifferentKey(t *testing.T) {
	c := newTestCache()
	pid := uuid.New()
	msgs := messages("hello")
	k1, _ := c.CacheKey(pid, "openai", "gpt-4o", msgs)
	k2, _ := c.CacheKey(pid, "anthropic", "gpt-4o", msgs)
	if k1 == k2 {
		t.Error("different providers should produce different cache keys")
	}
}

func TestCacheKey_DifferentMessages_DifferentKey(t *testing.T) {
	c := newTestCache()
	pid := uuid.New()
	k1, _ := c.CacheKey(pid, "openai", "gpt-4o", messages("hello"))
	k2, _ := c.CacheKey(pid, "openai", "gpt-4o", messages("goodbye"))
	if k1 == k2 {
		t.Error("different message content should produce different cache keys")
	}
}

// ── Format ────────────────────────────────────────────────────────────────────

func TestCacheKey_IsHex64Chars(t *testing.T) {
	c := newTestCache()
	key, err := c.CacheKey(uuid.New(), "openai", "gpt-4o", messages("test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// SHA-256 → 32 bytes → 64 hex chars
	if len(key) != 64 {
		t.Errorf("expected 64-char hex key, got len=%d: %q", len(key), key)
	}
	for _, ch := range key {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			t.Errorf("key contains non-hex character %q in %q", ch, key)
			break
		}
	}
}
