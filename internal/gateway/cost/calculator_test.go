package cost

import (
	"testing"
)

// newTestCalculator returns a Calculator backed by defaultPricing (no DB).
func newTestCalculator() *Calculator {
	return &Calculator{
		db:      nil,
		pricing: defaultPricing(),
	}
}

// ptr is a helper to take a pointer to an int literal.
func ptr(i int) *int { return &i }

// ── CalculateCost ─────────────────────────────────────────────────────────────

func TestCalculateCost_KnownModel(t *testing.T) {
	calc := newTestCalculator()

	// gpt-4o: $5 / 1M input, $15 / 1M output
	// 1000 input + 500 output
	// = (1000/1_000_000)*5 + (500/1_000_000)*15
	// = 0.005 + 0.0075 = 0.0125
	got, err := calc.CalculateCost("openai", "gpt-4o", 1000, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := 0.0125
	if abs(got-want) > 1e-9 {
		t.Errorf("CalculateCost gpt-4o: got %.10f, want %.10f", got, want)
	}
}

func TestCalculateCost_Ollama_AlwaysZero(t *testing.T) {
	calc := newTestCalculator()
	got, err := calc.CalculateCost("ollama", "gemma4:4b", 999_999, 999_999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Errorf("Ollama cost should be 0, got %v", got)
	}
}

func TestCalculateCost_UnknownModel_ReturnsError(t *testing.T) {
	calc := newTestCalculator()
	_, err := calc.CalculateCost("openai", "gpt-99-ultra", 100, 100)
	if err == nil {
		t.Error("expected error for unknown model, got nil")
	}
}

func TestCalculateCost_NormalizedDateSuffix(t *testing.T) {
	calc := newTestCalculator()
	// "claude-3-haiku-20240307" should normalize to "claude-3-haiku" which
	// exists in defaultPricing (stripping the -20240229 suffix logic covers -20240307 too
	// via the generic "-202" trimmer).
	_, err := calc.CalculateCost("anthropic", "claude-3-haiku-20240307", 100, 100)
	if err != nil {
		t.Errorf("normalized model lookup failed: %v", err)
	}
}

func TestCalculateCost_ZeroTokens(t *testing.T) {
	calc := newTestCalculator()
	got, err := calc.CalculateCost("openai", "gpt-4o-mini", 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Errorf("zero tokens should yield zero cost, got %v", got)
	}
}

// ── EstimateCostForRequest ────────────────────────────────────────────────────

func TestEstimateCostForRequest_WithMaxTokens(t *testing.T) {
	calc := newTestCalculator()
	// gpt-4o-mini: $0.15/1M in, $0.60/1M out
	// 200 input, max_tokens=100
	// = (200/1M)*0.15 + (100/1M)*0.60 = 0.00003 + 0.00006 = 0.00009
	got, err := calc.EstimateCostForRequest("openai", "gpt-4o-mini", 200, ptr(100))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := (200.0/1_000_000)*0.15 + (100.0/1_000_000)*0.60
	if abs(got-want) > 1e-12 {
		t.Errorf("got %.12f, want %.12f", got, want)
	}
}

func TestEstimateCostForRequest_NoMaxTokens_ClampsOutput(t *testing.T) {
	calc := newTestCalculator()
	// 10 input tokens → 2× = 20 output, but clamped to min 100
	got, err := calc.EstimateCostForRequest("openai", "gpt-4o-mini", 10, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should use 100 output tokens (floor clamp)
	want, _ := calc.CalculateCost("openai", "gpt-4o-mini", 10, 100)
	if abs(got-want) > 1e-12 {
		t.Errorf("got %.12f, want %.12f (expected floor-clamped output)", got, want)
	}
}

func TestEstimateCostForRequest_NoMaxTokens_ClampsHigh(t *testing.T) {
	calc := newTestCalculator()
	// 5000 input → 2× = 10000, clamped to 2000
	got, err := calc.EstimateCostForRequest("openai", "gpt-4o-mini", 5000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want, _ := calc.CalculateCost("openai", "gpt-4o-mini", 5000, 2000)
	if abs(got-want) > 1e-12 {
		t.Errorf("got %.12f, want %.12f (expected ceiling-clamped output)", got, want)
	}
}

func TestEstimateCostForRequest_Ollama_AlwaysZero(t *testing.T) {
	calc := newTestCalculator()
	got, err := calc.EstimateCostForRequest("ollama", "llama3:8b", 1000, ptr(500))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Errorf("Ollama estimate should be 0, got %v", got)
	}
}

// ── normalizeModelName ────────────────────────────────────────────────────────

func TestNormalizeModelName(t *testing.T) {
	calc := newTestCalculator()
	cases := []struct {
		input string
		want  string
	}{
		{"gpt-4o-0613", "gpt-4o"},
		{"claude-3-haiku-20240307", "claude-3-haiku"},
		{"claude-sonnet-4-20250514", "claude-sonnet-4"},
		{"gpt-4-1106", "gpt-4"},
		{"gpt-4o-mini", "gpt-4o-mini"}, // no suffix, unchanged
	}
	for _, tc := range cases {
		got := calc.normalizeModelName(tc.input)
		if got != tc.want {
			t.Errorf("normalizeModelName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
