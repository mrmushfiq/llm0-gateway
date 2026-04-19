package providers

import (
	"testing"
)

// ── OpenAI ────────────────────────────────────────────────────────────────────

func TestOpenAIProvider_ValidateModel(t *testing.T) {
	p := &OpenAIProvider{}
	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-4o", true},
		{"gpt-4o-mini", true},
		{"gpt-4-turbo", true},
		{"gpt-3.5-turbo", true},
		{"gpt-5.4", true},
		{"gpt-5.4-mini", true},
		{"chatgpt-4o-latest", true},
		{"o1-preview", true},
		{"o3-mini", true},
		{"o4-mini", true},
		{"claude-3-opus", false},
		{"gemini-2.5-pro", false},
		{"llama3:8b", false},
		{"", false},
	}
	for _, tc := range cases {
		got := p.ValidateModel(tc.model)
		if got != tc.want {
			t.Errorf("OpenAI.ValidateModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

// ── Anthropic ─────────────────────────────────────────────────────────────────

func TestAnthropicProvider_ValidateModel(t *testing.T) {
	p := &AnthropicProvider{}
	cases := []struct {
		model string
		want  bool
	}{
		{"claude-3-opus-20240229", true},
		{"claude-sonnet-4-6", true},
		{"claude-haiku-4-5-20251001", true},
		{"claude-opus-4-7", true},
		{"claude-2", true},
		{"gpt-4o", false},
		{"gemini-2.5-pro", false},
		{"llama3:8b", false},
		{"", false},
	}
	for _, tc := range cases {
		got := p.ValidateModel(tc.model)
		if got != tc.want {
			t.Errorf("Anthropic.ValidateModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

// ── Gemini ────────────────────────────────────────────────────────────────────

func TestGeminiProvider_ValidateModel(t *testing.T) {
	p := &GeminiProvider{}
	cases := []struct {
		model string
		want  bool
	}{
		{"gemini-2.5-pro", true},
		{"gemini-2.5-flash", true},
		{"gemini-2.0-flash-lite", true},
		{"gemini-1.5-pro", true},
		{"gpt-4o", false},
		{"claude-3-opus", false},
		{"llama3:8b", false},
		{"", false},
	}
	for _, tc := range cases {
		got := p.ValidateModel(tc.model)
		if got != tc.want {
			t.Errorf("Gemini.ValidateModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

// ── Ollama ────────────────────────────────────────────────────────────────────

func TestOllamaProvider_ValidateModel_AlwaysTrue(t *testing.T) {
	p := &OllamaProvider{}
	models := []string{
		"gemma4:4b",
		"qwen3.5:14b",
		"llama3:8b",
		"mistral:7b",
		"gpt-4o",   // even "wrong" names return true — Ollama is permissive
		"",
	}
	for _, model := range models {
		if !p.ValidateModel(model) {
			t.Errorf("OllamaProvider.ValidateModel(%q) = false, want true", model)
		}
	}
}
